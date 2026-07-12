# Strategy 03 — OCR First, Then Structure

Depends on `00-extraction-experiment-platform.md`.

## Idea

Split "read the pixels" from "understand the receipt" into two calls:

1. **OCR pass** — get raw text out of the receipt image via something
   cheaper/more deterministic than a full vision-LLM call.
2. **Structuring pass** — feed the OCR'd text (no image) to a **text-only**
   chat completion asking it to fit the text into the same
   `extract_receipt` schema used today.

The hypothesis: a dedicated OCR engine is more consistent at raw character
recognition than a general vision-LLM doing recognition-and-reasoning in one
pass, and a text-only structuring call is cheaper/faster (no image tokens)
and has an easier job (parsing already-correct text vs. reading pixels and
parsing simultaneously).

## Two OCR options — pick one, don't build both

**Option A: local Tesseract (recommended default).** Free, deterministic,
no extra API key/cost, runs entirely server-side.

- Requires `tesseract-ocr` (+ language data) installed in the backend Docker
  image — new `apt-get install tesseract-ocr` line in
  `docker/backend.Dockerfile`'s build stage (and the prod distroless stage,
  which is more involved: distroless has no package manager, so the prod
  image needs the tesseract binary + its data files copied in from a build
  stage, or prod OCR needs a non-distroless base — this is a real
  prod-image architecture question, not a one-liner, and should be resolved
  before committing to this approach for anything beyond local dev/bench).
- New `internal/ocr` package: shell out to the `tesseract` CLI (no
  well-maintained pure-Go OCR engine exists; `gosseract` Go bindings need
  cgo + the same system library, which doesn't remove the Docker-image
  problem and adds a cgo build-complexity tax on top — shelling out to the
  CLI is simpler and matches this repo's "prefer boring, explicit
  subprocess calls over fragile bindings" instinct implied by how other
  external tools are integrated here). Write image bytes to a temp file
  (`os.CreateTemp`), invoke `tesseract <path> stdout`, capture stdout,
  clean up the temp file, wrap in a timeout via `exec.CommandContext`.
- Receipt-specific tuning likely needed: thermal-paper receipts are a known
  weak spot for Tesseract's default settings — expect to spend real time on
  `--psm` (page segmentation mode) flags and possibly a light
  preprocessing pass (this overlaps with doc 04's image-cleanup work —
  worth building 04's binarization step first and feeding *that* output to
  Tesseract rather than the raw photo, since Tesseract's accuracy is
  heavily dependent on contrast/binarization already).

**Option B: cheap vision-LLM transcription call.** Reuses existing
`openaicompat` plumbing entirely — just a second, smaller/cheaper model
call with a "transcribe all text on this receipt verbatim, no
interpretation" prompt and no forced tool schema (plain text response).
Simpler to implement (zero new infra, zero Docker changes) but doesn't test
the "OCR is more reliable than a VLM" hypothesis as sharply, since it's
still a VLM doing the reading — just a possibly-cheaper one. Only worth
doing if Option A's Docker/prod-image complexity turns out to be a bigger
blocker than expected.

**Recommendation: build Option A for the bench/experiment path only
first** (dev Docker image, `extractbench` CLI) — don't solve the prod
distroless image problem until/unless bench results show this strategy is
worth shipping. That defers the hardest infra question until it's known to
be worth answering.

## Structuring call

Text-only chat completion, same forced `extract_receipt` schema/tool as
today, prompt changed to describe the input as noisy raw OCR text (mention
that OCR sometimes drops characters or misreads similarly-shaped glyphs,
ask it to use context — item pricing patterns, receipt structure
conventions — to reconstruct likely-correct values). This reuses the
`Client.ExtractWithSchema`-style refactor called out in doc 01 (a text-only
variant needs no `image_url` content part at all, everything else about the
tool-call/parsing plumbing is identical).

`MaxCalls() == 1` in terms of the *structuring* LLM call — OCR itself isn't
an LLM call under Option A (free/local), so cost-cap/reservation sizing
treats this the same as baseline for spend purposes, just with extra
non-LLM latency for the Tesseract subprocess.

## Scope of work

- `internal/ocr` package + Tesseract Docker/dev-image wiring: this is the
  bulk of the effort and the main source of schedule risk (subprocess
  reliability, timeout handling, language-data packaging, PSM-mode tuning
  against actual receipt photos).
- Text-only structuring call: small, once `ExtractWithSchema` exists from
  doc 01.
- Strategy wiring: small.

## Estimate

Medium–large, dominated by OCR tuning and infra, not by application code:

- Docker/dev-image Tesseract install + `internal/ocr` subprocess wrapper +
  timeout/error handling: ~1 day.
- PSM-mode/preprocessing tuning against real corpus receipts to get
  Tesseract's raw output legible enough to be useful: **this is the
  unpredictable part** — could be half a day if default settings are
  already decent, could be 2+ days of trial and error against thermal-paper
  noise. Budget generously and time-box it — if OCR output is still garbage
  after a day of tuning, that's itself a useful negative result (skip to
  doc 04, or abandon this strategy).
- Text-only structuring call + strategy wiring: ~0.5 day.
- Prod distroless image OCR packaging (deferred, only if bench results
  justify shipping this): unscoped, flag as a follow-up decision, not part
  of this estimate.

**Total: ~2–3.5 days for the bench-only version**, highly dependent on how
well Tesseract copes with the actual receipt photos in the corpus — the
biggest unknown of all four strategies.

## Test plan

- `internal/ocr` unit/integration test: run Tesseract against a couple of
  corpus images, assert non-empty output and rough presence of expected
  substrings (e.g. the known restaurant name) — can't assert exact text
  output deterministically across Tesseract versions, so keep assertions
  loose.
- `extractbench -strategy=ocr_first` against the corpus — the real
  judgment call is eyeballing a few OCR dumps directly (add a `-dump-ocr`
  bench flag that prints raw OCR text per file) before trusting the
  structuring call's output at all.
