# Strategy 04 — Image Preprocessing (Clean-Up Before the Vision Call)

Depends on `00-extraction-experiment-platform.md`.

## Idea

Keep the existing single vision-LLM call and schema entirely as-is (reuse
`baseline`'s call shape) — the only change is what image bytes get sent.
Insert a deterministic image-processing pass between "read the uploaded
file" (`receipt.go`'s `s.Receipts.Open(*sess.ReceiptImagePath)`) and "call
the LLM," producing a higher-contrast, cropped, deskewed version of the
receipt for the model to look at instead of the raw photo.

This is the cheapest strategy to test in terms of *added LLM cost* — it's
pure image processing, no extra model call at all (unless the optional
crop-detection step below uses one) — but the most open-ended in terms of
"how much processing is actually worth doing."

## Two phases — ship phase 1 alone first, evaluate before phase 2

**Phase 1: contrast/binarization (no new dependency, deterministic, free).**

- Grayscale conversion (Go stdlib `image`, trivial).
- Contrast enhancement / adaptive thresholding (Otsu's method is the
  standard choice for "make text pop against a variable-lighting
  background" — a well-known, implementable-in-~50-lines algorithm, no
  external library required; `golang.org/x/image` isn't even needed for
  this, plain `image.Gray` manipulation suffices).
- Optional mild sharpen/denoise pass.
- Re-encode as JPEG (reuse `internal/receipts`' existing JPEG-encoding
  helper rather than duplicating quality/encoder settings).
- This is a pure, synchronous, in-process Go function:
  `internal/imageprep.Clean(jpegBytes []byte) ([]byte, error)`. No Docker
  changes, no new binary dependency, easy to unit-test with golden-image
  comparisons (store a couple of known input/output pairs in
  `internal/imageprep/testdata/`, assert byte-identical or
  perceptual-hash-similar output — exact byte match is fragile across Go
  version/JPEG encoder changes, prefer a perceptual-similarity check like a
  simple average-hash if this gets flaky).

**Phase 2 (only pursue if phase 1's bench results are promising but not
enough): crop-to-receipt-boundary.** Only relevant if some corpus photos
have significant background clutter (table, hand, other objects) rather
than being cropped-tight photos already — check the actual corpus first
before scoping this at all, it may be unnecessary.

- Classical CV contour/edge detection (e.g. via GoCV/OpenCV bindings) is the
  "textbook" approach but is a heavy, painful dependency in this stack
  specifically: OpenCV needs a system library + cgo, which means Docker
  image changes on the same order of pain as strategy 03's Tesseract
  problem, *times two* since GoCV's build is notoriously fragile even with
  the system lib present. **Do not reach for GoCV** unless phase 1 alone
  clearly isn't enough and cropping is clearly the missing piece.
- Cheaper alternative: a *cheap, fast, non-reasoning* vision-LLM call (small
  model, minimal output — just 4 corner coordinates or a bounding box) that
  reuses existing `openaicompat` plumbing entirely, no new system
  dependency. This makes phase 2 an *extra LLM call* (small, cheap, but
  real) — factor that into `MaxCalls()` if built (`MaxCalls() == 2`: one
  cheap bbox call + one full extraction call).

## Scope of work

Phase 1 only (recommended starting scope):

- `internal/imageprep` package: grayscale + Otsu threshold + re-encode.
- Wire into a strategy: `internal/extraction/preprocess.Strategy` — calls
  `imageprep.Clean`, then delegates to the same call path `baseline` uses
  (literally reuse `baseline`'s internals with the image byte swap — this
  is the smallest possible diff of any of the four strategies against the
  existing pipeline).
- `MaxCalls() == 1`, same cost profile as baseline exactly.

## Estimate

Small (phase 1 only):

- Otsu threshold + grayscale + re-encode implementation: ~0.5–1 day
  (algorithm itself is well-documented and short; most of the time is
  eyeballing output against real corpus photos to tune threshold
  sensitivity/sharpening so it actually looks *better* to a human, not just
  "different").
- Strategy wiring: trivial once `imageprep.Clean` exists, well under an
  hour.
- Golden-image unit tests: ~0.5 day.

**Total: ~1–1.5 days for phase 1.** Phase 2 (crop detection, either GoCV or
a cheap-model bbox call) is explicitly out of scope for the initial pass —
re-estimate only if phase 1's bench results warrant it, since it could add
anywhere from ~1 day (cheap-model bbox call, reuses existing plumbing) to
several days (GoCV path, new system dependency, fragile builds) depending
on which approach gets picked.

## Risks / unknowns

- Aggressive binarization can *hurt* a VLM's ability to read the image if
  it strips information the model was actually using well already (e.g.
  color cues, subtle grayscale gradients around faint thermal-print text) —
  this is genuinely an empirical question, not something to assume the
  answer to. The bench tool is what settles it, not intuition.
- No guarantee the failure mode you're trying to fix (wrong price
  interpretation, misread digits) is actually image-quality-driven at all
  — if the corpus's known-bad receipts are already high-contrast, sharp
  photos, this strategy has nothing to fix and 01/02 are the more relevant
  bets. Worth checking the known-bad corpus images by eye before investing
  here.

## Test plan

- Golden-image unit tests for `imageprep.Clean` (perceptual-hash comparison
  against known-good output, per phase 1 above).
- `extractbench -strategy=image_preprocess` against the corpus, specifically
  comparing against `baseline` on the same known-bad images — does cleanup
  alone move the needle without any prompt/schema/call-count change?
