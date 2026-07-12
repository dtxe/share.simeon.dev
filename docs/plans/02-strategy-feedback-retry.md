# Strategy 02 — Feedback Retry

Depends on `00-extraction-experiment-platform.md` and reuses
`CheckSubtotal` from `01-strategy-deterministic-format-check.md`.

## Idea

Keep today's pipeline (single vision call, current prompt/schema, current
thinking budget) exactly as-is for the first attempt. After it returns, run
the same `CheckSubtotal` used by strategy 01. If it doesn't match, make a
**second** call in the same conversation — append the first call's tool
response and a new user message pointing out the specific discrepancy
("your extraction says items sum to $X but the receipt's subtotal is $Y —
re-examine the image, the most likely error is treating a line-total as a
per-unit price on one or more items — return corrected values"), and ask it
to call `extract_receipt` again.

This is the most direct test of "would more targeted thinking fix it" —
instead of a bigger blind thinking budget, give the model the exact
arithmetic evidence that something's wrong and let it re-look at the image
with that in hand.

`MaxCalls() == 2` (only calls the second time if the first mismatches — cost
is close to strategy baseline's on the common case where extraction is
already correct).

## Scope of work

**Multi-turn support in the shared client.** This is the one piece that
doesn't exist today and is genuinely new plumbing: `openaicompat.Client`
currently builds exactly one user message per call
(`client.go` request construction, single-shot). Needs a way to pass prior
conversation turns (the original user message + image, the assistant's tool
call, a tool-result message, then a new user message) so the second call is
a real continuation, not a fresh blind request.

- Extend `Client` with something like `ExtractReceiptWithHistory(ctx, image
  []byte, mimeType string, priorTurns []chatMessage, feedback string)
  (*llm.Result, error)` — or, better, factor the request-building into
  `buildMessages(image, mimeType, priorTurns...)` so both the single-call
  and multi-turn paths share the same tool/schema/parsing code, and only the
  message list construction differs.
- The **image** needs to stay in context for the second call (the model
  needs to re-look at it, not just reason from the mismatch text) — re-send
  the same base64 `image_url` part in the follow-up user message rather than
  relying on it staying in a stateless HTTP API's "memory" (there is none —
  each `chat/completions` call is independent, the "conversation" is just a
  longer `messages` array we construct ourselves and resend in full every
  call). This roughly doubles the request payload size on the second call
  (image re-sent) — worth noting for latency, not just cost.

**Strategy wiring**: `internal/extraction/feedback.Strategy`, calls
`ExtractReceiptWithHistory`-equivalent once, checks subtotal, and only on
mismatch builds the second call. Reports both attempts via `RunResult.Attempts`
(2 entries when retried, 1 when not) so `extraction_runs` shows exactly what
happened.

**Cost caps**: `MaxCalls() == 2`, so per doc 00's reservation sizing, this
strategy reserves 2x the flat per-call estimate even on the common
non-retry path. That's a real, if small, extra conservatism cost — call it
out in the PR description when this lands, since it changes the effective
daily-cap headroom compared to `baseline`/strategy 01 (both `MaxCalls()==1`)
even when the second call never fires.

**Handler timeout**: doc 00 already covers bumping `context.WithTimeout` to
`60s * MaxCalls()` — this strategy is the reason that's needed (120s worst
case here vs. 60s for baseline/01).

## Estimate

Medium. The multi-turn message-history plumbing is the real work; the
retry-decision logic itself is trivial once `CheckSubtotal` exists (built in
strategy 01 — build 01 first, this strategy is materially cheaper once that
shared helper exists).

- `buildMessages`/history refactor of `openaicompat.Client`: ~1 day,
  moderate risk since it touches the same request-construction code
  `baseline`/01 depend on — needs careful test coverage to confirm the
  existing single-call tests still pass unchanged after the refactor.
- Feedback-message wording + retry strategy wiring: quick, well under a day.
- Live testing against a receipt that actually triggers the mismatch path
  (need at least one corpus image where the first call is known to get it
  wrong) — this is why the test corpus in doc 00 should prioritize
  already-known-bad receipts.

**Total: ~1.5–2 days.**

## Risks / unknowns

- No guarantee a second look with the same model fixes the same misread —
  if the model consistently misreads a given digit/glyph, pointing out
  "the math doesn't add up" doesn't necessarily tell it *which* item is
  wrong, only that something is. Worth checking a handful of known-bad
  corpus receipts by hand before investing further — if the second call's
  corrected numbers are frequently still wrong, this strategy isn't worth
  keeping over 01 alone.
- Doubles worst-case latency and (on mismatch) cost — acceptable for an
  interactive single-user upload flow, but confirm the frontend's existing
  spinner/shimmer state (`BillWorkspace.tsx`) reads fine at up to ~2x the
  usual wait.

## Test plan

- `httptest`-mocked test asserting the second request's `messages` array
  actually contains the first attempt's image + tool call + the feedback
  text (this is the part most likely to have a subtle bug — e.g. forgetting
  to re-attach the image, or getting message role ordering wrong).
- `extractbench -strategy=feedback_retry` against the corpus, specifically
  the known-bad subset — does attempt 2's subtotal match more often than
  attempt 1's did on the same set baseline gets wrong?
