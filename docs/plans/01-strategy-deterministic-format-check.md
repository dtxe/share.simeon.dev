# Strategy 01 — Deterministic Format Check

Depends on the shared plumbing in `00-extraction-experiment-platform.md`
(`extraction.Strategy` interface, `CheckSubtotal` helper, bench CLI).

## Idea

Today's prompt asks the model to both transcribe *and* self-reconcile
("verify item-sum equals subtotal, fix whichever was misread") in one shot —
which is exactly the kind of judgment call a bigger thinking budget helps
with, and the one we want to avoid depending on. Split those two jobs:

1. **Extraction call**: low/no thinking budget, prompt explicitly forbids
   self-correction — transcribe restaurant name, date, subtotal, total, and
   each line item's name/quantity/price **exactly as printed**, with no
   instruction about what "price" means (unit vs. line-total). This is
   deliberately ambiguous on the model's end — we want the raw printed
   number, not its interpretation of it.
2. **Programmatic check** (pure Go, no network): given `items[]` with
   `{name, quantity, printedPriceCents}` and the extracted `subtotalCents`,
   try two interpretations:
   - `sum(printedPriceCents)` — price already is the line total.
   - `sum(printedPriceCents * quantity)` — price is a per-unit price.
   Whichever sums to `subtotalCents` (within a small tolerance, e.g. ±1 cent
   for multi-item rounding) wins; that tells you the receipt's price format.
   If **neither** matches, mark `SubtotalMatched: false` and fall back to
   treating `printedPriceCents` as the line total (today's UI already treats
   `unitPriceCents = round(priceCents*quantity)` as the per-dish price, so
   picking a sane default here matters for what actually lands in `dishes`).

No second LLM call in this strategy — `MaxCalls() == 1`. It's a pure
prompt/schema change on the extraction side plus new Go logic after.

## Why this might help

The single most common failure mode already called out in the prompt
comment ("recording the line total as priceCents instead of the per-item
unit price") is exactly the ambiguity this strategy sidesteps by not asking
the model to resolve it at all — resolve it deterministically instead, where
"deterministic" means "provably consistent with the printed subtotal," not
"the model's best guess under time pressure."

## Scope of work

**New schema/prompt** (`internal/extraction/deterministic/` package, or a
new prompt+schema variant inside `openaicompat` — recommend a **new small
package** wrapping `openaicompat.Client` rather than modifying the existing
prompt/schema, since the existing baseline strategy must keep working
byte-for-byte):

- New extraction schema: item `{name, quantity, printedPriceCents}` (rename
  from `priceCents` to make the "no interpretation" intent explicit in code,
  not just docs).
  - `internal/llm/openaicompat/client.go`'s `extractionSchema` and prompt
    are private to that package — either (a) export a schema/prompt
    variant constructor from `openaicompat`, or (b) since the request/response
    plumbing (HTTP call, base64 image part, tool-call decode,
    `finish_reason:"length"` handling) is identical and worth reusing, add a
    `Client.ExtractWithSchema(ctx, image, mimeType, prompt string, schema
    json.RawMessage, thinkingBudget *int) (json.RawMessage, llm.Usage,
    error)` lower-level method, and have both the existing `ExtractReceipt`
    and the new strategy call through it. This is the one piece of *shared
    client* refactor this strategy needs — recommend doing it, since
    duplicating the ~150 lines of request-building/response-parsing in
    `client.go` for every new strategy would get unmaintainable fast across
    four experiments.
- `thinkingBudget`: pass a small value (or `nil`/disabled) instead of the
  hardcoded 2000 — this needs `thinkingConfigForModel`
  (`client.go:269-279`) to take a budget parameter instead of being a fixed
  per-model constant. Needs a live curl check first per
  `docs/agent_lessons.md`'s existing warning: confirm Fireworks actually
  accepts a small non-zero `budget_tokens` (e.g. 200) for MiniMax M3/Kimi —
  don't assume `0` or omitting `thinking` entirely behaves as "low thinking"
  without checking, some APIs reject `budget_tokens` below a minimum.
- New prompt: drop the self-reconciliation instruction entirely; explicitly
  say "record the price exactly as printed, do not compute or convert it."

**New Go verification logic**:

- `internal/extraction/verify.go`'s `CheckSubtotal` (shared per doc 00) plus
  this strategy's own "try both interpretations, pick the winner" logic —
  call it `ResolvePriceFormat(items, subtotalCents) (resolved []llm.ExtractedItem, matched bool)`.
  Pure function, fully unit-testable with table-driven cases: exact match on
  line-total interpretation, exact match on per-unit interpretation, off-by-
  rounding (largest-remainder-style cent distribution across many items),
  no match at all, single-item receipts, zero items.

**Strategy wiring**: `internal/extraction/deterministic.Strategy` type
implementing the `extraction.Strategy` interface from doc 00, `MaxCalls()
== 1`.

## Estimate

Small–medium. Roughly:
- `Client.ExtractWithSchema` refactor + budget-param plumbing: half a day
  (mostly careful refactor of existing tested code, low risk since baseline
  strategy's tests pin exact request/response shape already).
- New prompt/schema + strategy wiring: quick, well under a day.
- `ResolvePriceFormat`/`CheckSubtotal`: quick, it's the easiest deterministic
  piece in the whole project and the most valuable to get right since 02
  reuses it too.
- Live Fireworks curl check for low-thinking-budget acceptance: 30 min,
  do this **before** writing the strategy code, per the existing gotcha in
  `docs/agent_lessons.md` about model-specific parameter acceptance being
  underdocumented.

**Total: ~1 day.** Cheapest of the four strategies — no new external
dependency, no multi-call cost/latency increase, reuses the same single
vision-LLM-call shape as baseline.

## Test plan

- Unit tests for `ResolvePriceFormat`/`CheckSubtotal` — the bulk of real
  test coverage here, pure functions, no mocking needed.
- `openaicompat`-style `httptest` mock test confirming the new prompt/schema
  is sent and a low `thinking.budget_tokens` value appears in the request
  body (same assertion style as `client_test.go`'s existing
  `TestExtractReceiveReasoningConfigIsModelAware`).
- `extractbench -strategy=deterministic_check` against the saved corpus,
  compared to `-strategy=baseline` — the real signal.
