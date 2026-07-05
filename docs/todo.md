# Cher — Build TODO

Tracks progress against the approved plan (`docs/design_decisions.md` has the why; this is the what/when).

## 1. Repo & dev env scaffold
- [x] `.mise.toml` (pin go, node; tasks: dev/migrate/test-backend/test-frontend/build)
- [x] `.gitignore` / `.dockerignore` (exclude `.env`, `secrets/`, `uploads/`, `node_modules`, build artifacts)
- [x] `.env.example`
- [x] `docker-compose.yml` + `docker-compose.override.yml` (postgres, redis, mailpit, backend, frontend)
- [x] `docker/backend.Dockerfile` (dev + prod multi-stage)
- [x] `docker/frontend.Dockerfile` (dev + prod multi-stage, nginx same-origin proxy) — scaffolded, frontend app code still pending (step 10)
- [x] `backend/cmd/server/main.go` health endpoint (`/healthz`) — confirmed: postgres/redis/mailpit/backend healthy, `curl /healthz` → 200, air hot reload working
  - note: bumped Go pin from 1.22 to 1.25 (chi/pgx/migrate/go-redis/go-webauthn all require 1.24+ now); backend dev host port moved to 8081 to avoid clashing with frontend's prod nginx port 8080
  - note: migrations live under `backend/internal/db/migrations/` (not top-level `backend/migrations/`) since `go:embed` can't reference parent directories

## 2. Identity foundation
- [x] `backend/internal/db/migrations/0001_identity.up.sql` (+`.down.sql`) (users, sessions, webauthn_credentials, otp_codes)
- [x] `internal/config` — env + `_FILE` secret loading
- [x] `internal/db` — pgx pool, migration runner (embedded via `go:embed`, runs on every boot)
- [x] `internal/auth/session.go` — issue/verify/renew, cookie transport (+ header mode)
- [x] `Identify` middleware — anonymous auto-provision, `ANON_ACCOUNTS_ENABLED` gate
- [x] `GET /api/me` endpoint
- [x] Manual check: cold request gets cookie + anonymous user with zero client action — confirmed via curl (Set-Cookie on first call, reused silently on second, `hasEmail`/`hasPasskey` both false)
- [x] Manual check: `ANON_ACCOUNTS_ENABLED=false` → 401 `login_required` with no session; reverted, confirmed anon mode restored
  - note: golang-migrate's iofs source requires `{version}_{name}.up.sql`/`.down.sql` naming, not bare `.sql` — renamed accordingly
  - note: header-transport mode returns the new session token via an `X-Anon-Session-Token` *response header* (CORS-exposed), not a JSON body field as the plan sketch said — simpler, keeps `Identify` generic across all handlers

## 3. Bill data model
- [x] `backend/internal/db/migrations/0002_bills.up.sql` (+`.down.sql`) (bill_sessions, people, dishes, portions, extraction_runs) — money columns are `BIGINT` cents, not `NUMERIC`
- [x] `internal/store` — repository layer (CRUD for sessions/people/dishes/portions, share-token generation/lookup, `GetBreakdown` wiring into `internal/split`)
- [x] `internal/split/calculate.go` — pure split math, integer cents, largest-remainder rounding
- [x] Unit tests: even split, uneven shares, zero-share/unassigned dish, rounding reconciliation — all passing (`go test ./internal/split/...`)

## 4. Email OTP + merge
- [x] `internal/email` — Provider interface + SMTP impl
- [x] `internal/auth/otp.go` — generate/verify, resend cooldown + max-attempts, Postgres-backed
- [x] `internal/auth/merge.go` — `AttachEmailOrMerge` (no-collision update + collision merge w/ advisory lock)
- [x] `POST /api/auth/otp/request`, `POST /api/auth/otp/verify`, `POST /api/auth/logout`
- [x] Manual check: requested OTP via curl, read code from mailpit's API, verified, confirmed `hasEmail: true`; wrong code correctly 400s
- [x] Manual check: simulated collision (two anon identities verify same email) — confirmed via direct SQL that both sessions repoint to one canonical `users` row, no orphaned duplicate
  - note: per-IP OTP request rate limiting deferred to step 6 (`internal/ratelimit`, not built yet) — only the per-email resend cooldown is enforced so far

## 5. Passkeys (gated by `PASSKEY_ACCOUNTS_ENABLED`)
- [x] `internal/auth/webauthn.go` (go-webauthn v0.17.4) — register/login ceremonies, ceremony challenges stored in Postgres (`webauthn_ceremonies`, 2min TTL) not in-memory, so an air hot-reload mid-ceremony doesn't strand the user
- [x] `POST /api/auth/passkey/register/{options,verify}`, `POST /api/auth/passkey/login/{options,verify}`
- [x] Manual check: confirmed `PASSKEY_ACCOUNTS_ENABLED=false` (default) → 404 on register/options; `=true` → real WebAuthn creation challenge returned (valid RP info, pubKeyCredParams, challenge)
- [ ] Full register→login round trip needs a real browser + authenticator (can't fake WebAuthn attestation via curl) — **deferred to frontend integration testing**, per your direction
  - note: full `webauthn.Credential` (incl. attestation/flags) stored as JSONB (`credential_json`), not decomposed columns — the library needs the whole struct back to validate future logins correctly (migration 0003 alters the original schema sketch)
  - note: WebAuthnID uses the user's UUID text as raw bytes (not binary-decoded) — simpler, still well under the 64-byte spec limit

## 6. Rate limiting & LLM spend cap
- [x] `internal/ratelimit` — Redis fixed-window counters per IP (global, create-session, extract, otp-request, invalid-view-token)
- [x] LLM daily spend cap (Redis key, `LLM_DAILY_SPEND_CAP_CENTS`, `ReserveLLMSpend`/`AdjustLLMSpend`/`PeekLLMSpend`, reserve-then-rollback-if-over-cap pattern)
- [x] Per-session `extract_count` cap already exists in `internal/store` (belt-and-suspenders) — will be exercised together with the Redis cap once the extract endpoint is wired (step 9)
- [x] Tests against real Redis: windows reset (ran inside the backend container against the compose `redis` service), spend cap blocks + rolls back at threshold — both passing
  - note: Redis `EXPIRE` has whole-second granularity — fine for our real windows (all ≥1 min) but had to fix the test's synthetic short window accordingly
  - note: middleware fails open on a Redis error (logs, lets the request through) rather than taking the whole API down over a Redis hiccup; the Postgres `extract_count` cap is there specifically as the backstop for the one route (extraction) where failing open matters for cost
  - note: global per-IP limit (60/min) wired onto the whole `/api` group now; per-route limits (create-session, extract, invalid-view-token) will attach as those endpoints get built in step 9 — only OTP-request's is wired so far since that endpoint already exists

## 7. LLM provider (receipt extraction)
- [x] `internal/llm` — `Provider` interface + shared `openaicompat` wire-format client
- [x] `internal/llm/fireworks` — default impl (Kimi K2.7, json_schema response format)
- [x] `internal/llm/openai` — sibling impl proving swappability (both are ~15-line wrappers around `openaicompat.Client`)
- [x] Unit tests against a mocked HTTP server (request shape, auth header, response parsing, non-200 handling, unknown-field rejection) — all passing
- [ ] Manual curl smoke test against 2-3 real receipt photos — **blocked on a real `FIREWORKS_API_KEY`**, per your call to defer this; confirmed live that the endpoint fails *gracefully* (502, generic message, no upstream detail leaked, attempt still recorded in `extraction_runs`) when the key/network isn't available

## 8. Receipt upload pipeline
- [x] `internal/receipts` — size cap, magic-byte sniff, decompression-bomb guard, re-encode to JPEG q80 (strips EXIF/GPS)
- [x] Storage: random-hex filename under a per-session subdirectory, filename never client-derived
- [x] Manual check: uploaded a real JPEG end-to-end, fetched it back via both the owner route and the public share route, confirmed `file` reports valid JPEG both times

## 9. httpapi wiring
- [x] All bill endpoints (sessions, people, dishes/bulk, portions, breakdown, receipt, extract, share) — implemented and live-tested via curl
- [x] Public view mounted as its own `chi.Router` group (`/api/view`) with no `Identify` middleware in its chain — verified the public JSON response is hand-built (not reusing the owner DTO) so it can't accidentally leak the internal session UUID
- [x] Owner-check baked into every `internal/store` query's WHERE clause (not a separate check-then-act step)
- [x] Security headers + CORS policy
- [x] Full manual e2e via curl: create session → add 2 people → add 2 dishes → assign uneven shares (2:1) → set total paid → breakdown math verified by hand → share link → public view (confirmed no owner id/internal id leaked) → uploaded + fetched a real receipt image both ways → confirmed a *second* anonymous identity gets 404 (not data) on the first user's session
  - bug found + fixed: `store.Person`/`Dish`/`Portion` and `split.PersonBreakdown`/`Result` had no JSON tags, so the API was serializing Go's capitalized field names instead of the camelCase the rest of the API uses — added tags
  - bug found + fixed: the ratelimit package's own test (`TestReserveLLMSpendRollsBackRejection`) was writing to the **same Redis key the running app uses for today's real LLM spend cap** (no test/prod key separation), and had pushed it to 1,000,000+ cents, which then made the live `/extract` endpoint immediately 503 with "daily budget reached." Fixed the test to clean up its own delta via `t.Cleanup`, and manually reset the polluted key. Worth revisiting if a proper test-isolation mechanism (e.g. a key-namespace parameter) is wanted later — flagged, not fully solved
  - note: LLM-extracted JSON uses snake_case keys (`price_cents`, `restaurant_name`) since that's the schema handed to the model, while the rest of the API is camelCase — minor inconsistency, easy to reconcile at frontend-integration time, not fixed now

## 10. Frontend scaffold
- [ ] Vite + React + Tailwind v4 + shadcn subset (Button, Input, Drawer/vaul, Badge, Tabs) + wouter + lucide-react
- [ ] `state/bill.ts` reducer + localStorage sync
- [ ] `lib/split.ts` (mirrors backend cents math)
- [ ] `theme.css` design tokens
- [ ] `auth/useMe.ts` + fetch wrapper (`credentials: 'include'`)
- [ ] Welcome screen (3 BigActionCards + "Your bills" history + SaveHistoryBanner)
- [ ] People screen
- [ ] Items screen (manual + receipt review, shared ItemEditorList)

## 11. Assign screen (focus + rail)
- [ ] DishRow list, sticky PeopleRail, PersonChip
- [ ] Selection state machine (dish-mode / person-mode), shared `ADJUST_SHARES` action
- [ ] ShareStepper, "Split evenly" pill
- [ ] Exit gate warning on unassigned dishes
- [ ] Total paid drawer (reachable from Assign + Results)
- [ ] Results screen (PersonResultCard accordion, Share link CTA)

## 12. Receipt + share end-to-end
- [ ] Wire upload → extract → ItemsReview end to end
- [ ] SharedView (`/s/:token`) code-split chunk, chrome-free
- [ ] Share link creation/rotation

## 13. Prod hardening
- [ ] nginx same-origin proxy prod build
- [ ] distroless/non-root backend prod image
- [ ] docker secrets wiring (LLM key, SMTP password, DB creds)
- [ ] Daily cleanup job (expired sessions, otp_codes, stale bill_sessions + receipt files)

## Final verification pass
- [ ] `docker compose up` full flow, zero login: create bill → receipt → extract → assign → breakdown → share link → incognito view
- [ ] Second anon identity gets 404 on someone else's bill, not leakage
- [ ] `ANON_ACCOUNTS_ENABLED=false` → every route 401s without session
- [ ] Assign screen manually tested on narrow mobile viewport emulation
