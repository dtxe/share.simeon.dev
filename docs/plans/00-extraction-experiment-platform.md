# Receipt Extraction — Experiment Platform

## Context

MiniMax M3 (via Fireworks, `internal/llm/openaicompat`) sometimes misreads
receipts — most often confusing a line-total with a per-unit price, or
misreading digits on noisy thermal-paper photos. The current pipeline is a
single vision-LLM call with a forced `extract_receipt` tool call and a
prompt-level self-check ("verify item sum equals subtotal, fix if not") —
there is no programmatic verification, no retry, and no alternative
extraction path. We want to try four different extraction strategies and
compare them against real receipts, without committing to one before we know
which actually helps.

This doc defines the shared plumbing all four strategies plug into. Each
strategy has its own detail doc:

- `01-strategy-deterministic-format-check.md` — low-thinking raw transcription + programmatic subtotal check
- `02-strategy-feedback-retry.md` — current pipeline + second call on subtotal mismatch
- `03-strategy-ocr-first.md` — OCR pass, then text-only structuring call
- `04-strategy-image-preprocess.md` — deterministic image cleanup before the existing vision call

## Non-goals

- No production shadow-traffic / live A/B infra. This app has low volume and
  one operator (you) deciding which strategy wins — a Redis-backed experiment
  assignment service would be over-engineering. "Switchable" means an env var
  that picks one strategy at a time, plus an offline bench tool to compare
  them against a fixed corpus of saved receipts.
- No UI changes. Strategy comparison happens via a CLI tool and
  `extraction_runs` rows, not a dashboard.

## Architecture

### `internal/extraction` — Strategy interface

New package, sits above `internal/llm.Provider` (the interface is unchanged;
`Provider` stays the low-level "one chat-completions call" abstraction).

```go
package extraction

type Attempt struct {
    Provider   string // e.g. "fireworks:accounts/fireworks/models/minimax-m3"
    Model      string
    PromptTok  int
    CompleteTok int
    CostCents  int
    RawJSON    []byte // the parsed ExtractedReceipt, marshaled back
    Err        error  // non-nil if this attempt failed
}

type RunResult struct {
    Receipt          llm.ExtractedReceipt
    Attempts         []Attempt // one entry per LLM call made
    SubtotalMatched  bool
    SubtotalDiffCents int
}

type Strategy interface {
    Name() string
    // MaxCalls is the worst-case number of LLM calls this strategy can make
    // for one Run — used to size the upfront spend-cap reservation.
    MaxCalls() int
    Run(ctx context.Context, image []byte, mimeType string) (*RunResult, error)
}
```

Each strategy owns its own prompt/schema/thinking-budget choices internally
— it's free to call `openaicompat.Client` however it wants (single call,
two calls, text-only call, whatever). The four strategy docs describe what
each one actually does inside `Run`.

### Selection: `EXTRACTION_STRATEGY` env var

Mirrors the existing `LLM_PROVIDER` pattern (`internal/config`,
`cmd/server/main.go`'s switch at lines 72-80):

- `internal/config.Config` gets `ExtractionStrategy string` (default
  `"baseline"`).
- `main.go` gets a second switch, alongside the existing `LLM_PROVIDER`
  switch, that constructs the chosen `extraction.Strategy`, wiring in the
  already-constructed `llm.Provider` (and a second cheap provider/OCR client
  where a strategy needs one — see docs 03/04).
- `Server` gets an `Extractor extraction.Strategy` field. `baseline` is a
  ~10-line wrapper that calls `s.LLM.ExtractReceipt` once and reports
  `SubtotalMatched` via the same math the other strategies use (see below) —
  it changes zero behavior, it's the strategy-shaped skin around what exists
  today, and is the first thing to build/land so the abstraction is proven
  before any real new strategy work starts.

### Shared subtotal-check helper

Strategies 01/02 (and baseline's reporting) all need "does sum(items) match
subtotal." Put this once in `internal/extraction/verify.go`, not duplicated:

```go
// returns matched, diffCents, and which interpretation matched if either did
func CheckSubtotal(items []llm.ExtractedItem, subtotalCents int64) (matched bool, diffCents int64)
```

Pure function, trivially unit-testable, no network — this is the first real
deterministic piece of the whole effort and should be built once, shared by
every strategy that needs it.

### `handleExtract` changes (`internal/httpapi/receipt.go`)

Today: `s.LLM.ExtractReceipt(...)` once, one `extraction_runs` insert, one
Redis reservation sized as a flat 1-cent placeholder.

Change to:

1. Reserve `s.Extractor.MaxCalls() * estimatedCentsPerCall` against the daily
   cap (was a flat 1 cent for exactly 1 call — strategies that make 2+ calls
   need the reservation sized for the worst case so a mid-run rejection can't
   happen after the first call already spent money).
2. Call `s.Extractor.Run(ctx, imageBytes, mimeType)`.
3. `AdjustLLMSpend` by `sum(attempt.CostCents) - reserved` — true up once,
   same pattern as today, just fed from the aggregated attempts.
4. Insert **one `extraction_runs` row per `Attempt`**, not one per handler
   call (see migration below) — tagged with strategy name and attempt index,
   so a feedback-retry strategy's first (wrong) and second (corrected) calls
   are both visible, not just the final result.
5. `store.IncrementExtractCount` (the per-session cap) still increments by
   exactly **1 per `handleExtract` call**, regardless of how many LLM calls
   the strategy made internally — it's counting user-visible "extract"
   clicks, not LLM calls, and that semantics shouldn't change.
6. Handler's `context.WithTimeout` bumps from 60s to `60s *
   s.Extractor.MaxCalls()` (feedback-retry and OCR-first strategies need
   more than one 60s budget in series).

### `extraction_runs` migration

New migration `0004_extraction_runs_variants.up.sql` (+ `.down.sql`):

```sql
ALTER TABLE extraction_runs
  ADD COLUMN strategy TEXT NOT NULL DEFAULT 'baseline',
  ADD COLUMN attempt INT NOT NULL DEFAULT 1,
  ADD COLUMN prompt_tokens INT,
  ADD COLUMN completion_tokens INT,
  ADD COLUMN cost_cents INT,
  ADD COLUMN subtotal_matched BOOLEAN,
  ADD COLUMN subtotal_diff_cents INT;
```

Also add `store.InsertExtractionRun(ctx, ...)` and use it from the handler
instead of today's raw `s.Pool.Exec` in `receipt.go:179` — small cleanup
while touching this code, keeps the DB access pattern consistent with the
rest of `internal/store`.

### Bench CLI — the actual "experiment" tool

`backend/cmd/extractbench/main.go`. This is what you'll actually run
repeatedly while iterating — not the live app.

```
go run ./cmd/extractbench -strategy=deterministic_check -dir=testdata/receipts
```

- Walks a directory of saved receipt JPEGs (`testdata/receipts/*.jpg`).
- Optional sibling `testdata/receipts/expected.json` — `{"receipt1.jpg":
  {"subtotalCents": 4523}}` — ground truth for the tricky ones you've
  already seen MiniMax get wrong, hand-entered once by reading the photo
  yourself.
- Calls the chosen `extraction.Strategy` directly (constructs it the same
  way `main.go` does, reusing `internal/config.Load()` for API keys/model),
  bypassing the HTTP server, session model, and rate limiter entirely — it's
  a local CLI you run by hand, not an exposed endpoint.
- Prints one row per file: filename, extracted subtotal, computed subtotal,
  match ✓/✗, expected subtotal diff (if ground truth present), total tokens,
  cost¢, latency, attempt count.
- Prints an aggregate summary line: match rate, total cost, total time.

This is the actual mechanism for "switchable to experiment with" — flip
`-strategy`, rerun against the same corpus, diff the two runs' output by eye
or `diff`.

### Test corpus

`backend/testdata/receipts/` — a handful (10-20) of real, already-uploaded
receipt photos, prioritizing the ones you've already seen misread. Gitignore
the actual JPEGs (they're real receipts, don't want them in a public repo
history even on a private app) but check in `expected.json` and a
`README.md` in that dir noting how to populate it locally. Add the ignore
rule to `.gitignore`.

## Build order

1. `internal/extraction` package + `CheckSubtotal` + `baseline` strategy +
   `handleExtract` refactor + migration + `store.InsertExtractionRun`. Zero
   behavior change, but proves the whole plumbing compiles and existing
   tests/manual e2e still pass. **Land this first, alone.**
2. `extractbench` CLI, pointed at `baseline` — confirms the offline harness
   works before any new strategy exists to differentiate.
3. Strategy 01 (deterministic format check) — cheapest, purely additive,
   biggest likely signal for the "wrong price interpretation" failure mode.
4. Bench 01 against baseline on the corpus. Only then decide whether to
   invest in 02/03/04 — the docs for those are “what next if 01 isn't
   enough,” not a mandatory sequence.

## Verification

- `go build ./...`, `go vet ./...`, `go test ./...` after each step.
- After step 1: full existing manual e2e (create bill → extract → assign →
  settle) against a real receipt via the live dev stack, confirm identical
  behavior to before the refactor (same items, same `extraction_runs` row
  shape plus the new columns defaulting sanely).
- After step 3+: `extractbench -strategy=baseline` vs `-strategy=<new>` on
  the same corpus, compare match-rate/cost/latency columns directly.
