# Share — Build TODO

_Committed as work completes (per your instruction). Backend foundation through step 9 is in commit `859271e`; full frontend + bugfixes in `7ebfebb`._

Tracks progress against the approved plan (`docs/plan.md` is the original; `docs/design_decisions.md` has the why for what changed; `docs/agent_lessons.md` has gotchas worth knowing before touching this repo again; this file is the what/when).

## 1. Repo & dev env scaffold
- [x] `.mise.toml` (pin go, node; tasks: dev/migrate/test-backend/test-frontend/build)
- [x] `.gitignore` / `.dockerignore` (exclude `.env`, `secrets/`, `uploads/`, `node_modules`, build artifacts)
- [x] `.env.example`
- [x] `docker-compose.yml` + `docker-compose.override.yml` (postgres, redis, backend, frontend)
- [x] `docker/backend.Dockerfile` (dev + prod multi-stage)
- [x] `docker/frontend.Dockerfile` (dev + prod multi-stage, Caddy same-origin proxy)
- [x] `backend/cmd/server/main.go` health endpoint (`/healthz`) — confirmed: postgres/redis/backend healthy, `curl /healthz` → 200, air hot reload working
  - note: bumped Go pin from 1.22 to 1.25 (chi/pgx/migrate/go-redis/go-webauthn all require 1.24+ now); backend dev host port moved to 8081 to avoid clashing with frontend's prod Caddy port 8080
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
- [x] Manual check: requested OTP via curl, read code from the configured SMTP test inbox, verified, confirmed `hasEmail: true`; wrong code correctly 400s
- [x] Manual check: simulated collision (two anon identities verify same email) — confirmed via direct SQL that both sessions repoint to one canonical `users` row, no orphaned duplicate
  - note: per-IP OTP request rate limiting deferred to step 6 (`internal/ratelimit`, not built yet) — only the per-email resend cooldown is enforced so far

## 5. Passkeys (gated by `PASSKEY_ACCOUNTS_ENABLED`)
- [x] `internal/auth/webauthn.go` (go-webauthn v0.17.4) — register/login ceremonies, ceremony challenges stored in Postgres (`webauthn_ceremonies`, 2min TTL) not in-memory, so an air hot-reload mid-ceremony doesn't strand the user
- [x] `POST /api/auth/passkey/register/{options,verify}`, `POST /api/auth/passkey/login/{options,verify}`
- [x] Manual check: confirmed `PASSKEY_ACCOUNTS_ENABLED=false` (default) → 404 on register/options; `=true` → real WebAuthn creation challenge returned (valid RP info, pubKeyCredParams, challenge)
- [ ] Full register→login round trip needs a real browser + authenticator (can't fake WebAuthn attestation via curl) — frontend wiring is now implemented; remaining verification is the live browser/authenticator pass
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
- [x] `internal/llm/fireworks` — default impl (MiniMax M3, forced function-call response shape)
- [x] `internal/llm/openai` — sibling impl proving swappability (both are ~15-line wrappers around `openaicompat.Client`)
- [x] Unit tests against a mocked HTTP server (request shape, auth header, response parsing, non-200 handling, unknown-field rejection) — all passing
- [x] Manual curl smoke test against a real receipt photo — completed with the live Fireworks key from docker secrets; extraction returned structured JSON for a real receipt image

## 8. Receipt upload pipeline
- [x] `internal/receipts` — size cap, magic-byte sniff, decompression-bomb guard, re-encode to JPEG q80 (strips EXIF/GPS)
- [x] Unit tests cover receipt upper bounds: uploads over 10 MiB and decoded images over 40MP are rejected server-side
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
  - note: LLM-extracted JSON originally used snake_case keys since that mirrored the schema handed to the model; reconciled to camelCase (both the Go struct tags and the JSON schema sent to the model) while starting frontend integration, so the whole API is consistently camelCase now

## 10. Frontend scaffold
- [x] Vite + React (TS) + Tailwind v4 (`@tailwindcss/vite`) + wouter + lucide-react + `@tanstack/react-query` + vaul
  - simplified from the original plan's "minimal shadcn subset": no shadcn catalog was installed. The app uses Tailwind plus small local primitives (`Button`, `Section`, `SegmentedControl`, `Stepper`, `Avatar`) and keeps `vaul` only for bottom drawers (`ProfileDrawer`, `ShareLinkDrawer`) since accessible bottom sheets are fiddly to hand-roll.
- [x] `lib/api.ts` — typed fetch wrapper (`credentials:'include'`) covering every backend endpoint
- [x] `lib/split.ts` — local preview math mirroring backend cents/largest-remainder formula, reconciled against the server before anything is shown as final
- [x] `index.css` design tokens (warm paper background, white cards, green accent, fixed people palette, JetBrains Mono receipt typography) — no separate `state/bill.ts` reducer/localStorage layer, since the backend is the store of record and React Query replaced the originally-planned local-first reducer
- [x] `auth/useMe.ts`, `useAuthActions.ts`, and `ProfileDrawer` (email OTP request/verify, passkey register/login, logout) reachable from the app header
- [x] `AppHeader` + `/history` — bill history is a separate lightweight route; anonymous users are nudged to add email/passkey from the profile/history flow, not a welcome-screen banner
- [x] Latest redesign shipped: `/` and `/bill/:id` both render the single `BillWorkspace`; legacy step URLs redirect into it (`/people`, `/items`, `/assign` → `/bill/:id`; `/results` → `/bill/:id/settle`)

## 11. Bill workspace + settle flow
- [x] `BillWorkspace` accordion sections: receipt/items, people, total paid, assignment. Completion state auto-opens the first incomplete section and collapses completed sections unless the user manually toggled them.
- [x] Receipt/items section: upload/manual entry share one editable list; upload immediately runs extraction; re-scan warns before replacing items when portions exist.
- [x] Extraction loading motion: spinner next to "Reading your receipt…" plus shimmer placeholder rows while the LLM parses and no dishes have landed yet.
- [x] People section: bulk paste one name per line, inline rename/delete, confirmation before deleting a person with existing portions.
- [x] Total paid section: inline input (not a drawer), subtotal default action, receipt-derived value hint, and tax/tip delta caption.
- [x] Assignment section: segmented **By item** / **By person** modes over one shared shares map. By item expands a dish to per-person steppers and a "Split evenly" action; by person expands a person to per-dish steppers.
- [x] Exit gate warning on unassigned dishes (offers to split remainder evenly)
- [x] `Settle` screen (`/bill/:id/settle`): title editing, receipt-style subtotal/tax-tip/total summary, `PersonBreakdownCard` accordions, receipt thumbnail, fixed Edit/Create share actions, and `ShareLinkDrawer`.

## 12. Receipt + share end-to-end
- [x] Receipt/items section wired to `POST /sessions/:id/extract`; graceful failure path tested live (no API key configured)
- [x] SharedView (`/s/:token`) — chrome-free, live-verified in a **separate cookie-less browser session** (confirmed zero cookies sent, correct names/amounts rendered, no owner/session id or edit capability exposed); now mirrors the settle presentation with person breakdown cards when public dish detail is available
- [x] Share link creation — live-verified end to end (Settle → Share link → copy → open in fresh session → correct public breakdown)

### Bugs found via live browser testing (all fixed)
- **Nil-slice → `null` JSON crash**: several `internal/store` list functions and `split.Compute`'s `UnassignedDishIDs` used `var out []T`, which Go serializes as JSON `null` (not `[]`) when empty. The frontend's `.map()` over an empty dish/people list crashed the whole app on a brand-new bill. Fixed by initializing every list-returning function with `T{}` instead of `var`.
- **Public share view leaked no data, but initially had no names to show**: `GET /api/view/{token}` only returned `result.people[].personId` (a raw UUID) with no name, so the public page would have shown "Person 1" instead of "Alice." Added `ListPeoplePublic` (unchecked — the view token itself is the authorization) and a hand-built minimal `{id, name, sortOrder}` projection in the public handler, deliberately not reusing `store.Person` directly since that struct also carries `sessionId`, which must never appear on this endpoint.
- **`TotalPaidDrawer` stale prefill**: `useState(() => …)` only runs on first mount; since the drawer component instance persists across open/close (only `Drawer.Root`'s `open` prop toggles), reopening it after the subtotal changed still showed the old value. Fixed with a `useEffect` that resyncs the input whenever `open` becomes true.
- **Results screen line-item / header mismatch**: `PersonResultCard`'s per-dish line items showed the raw pre-tip subtotal share, while the header total was the tip-scaled final amount — so "$8 + $6" was displayed above a "$15.56" total. Fixed by scaling each line item by the same `totalPaid/subtotal` factor as the header.
- **LLM JSON field naming**: reconciled snake_case (`price_cents`) to camelCase (`priceCents`) across the Go struct tags, the JSON schema sent to the model, and the frontend types, so the whole API is consistently camelCase (caught while wiring the frontend's `api.ts` types against the actual backend response).

## 13. Prod hardening
- [x] **Caddy** same-origin proxy prod build (correction from the original plan, which assumed nginx — your call). Swapped `docker/frontend.Dockerfile`'s prod stage to `caddy:2-alpine`, replaced `docker/nginx.conf` with `docker/Caddyfile`. Built and ran both prod images standalone against the compose network: homepage 200, `/api/*` correctly proxied to the real backend (not just serving the SPA shell), client-side routes (`/bill/:id/assign`) correctly fall back to `index.html`. Prod JS bundle: 328.64 kB / **101.06 kB gzipped** — under the plan's 120KB budget.
  - **bug found + fixed**: first Caddyfile attempt used bare `try_files {path} /index.html` + `file_server` alongside a `@api`-matched `reverse_proxy` — but Caddy's default *directive* order runs `try_files` before `reverse_proxy` regardless of the order they're written in the file, so `/api/*` requests were being silently rewritten to `/index.html` before `reverse_proxy` ever got a chance to match them (confirmed live: `curl .../api/me` returned the SPA's HTML, not JSON). Fixed by wrapping both in explicit `handle` blocks, which are mutually exclusive and evaluated in file order — see `docs/agent_lessons.md`.
  - **font/CSP follow-up fixed**: the redesign uses Google-hosted JetBrains Mono, so prod CSP now permits `https://fonts.googleapis.com` in `style-src` and `https://fonts.gstatic.com` in `font-src`; without both, prod would silently fall back even though dev worked.
  - **process note**: `docker compose build <service>` retags the shared `<project>-<service>:latest` image (`share-<service>` as of the Cher→Share rename) regardless of which target you build, so testing the prod target this way temporarily overwrites the tag the running dev container was built from. Restored both by rebuilding through `docker compose up -d --build` (which uses the dev override again) immediately after the prod-image tests.
- [x] distroless/non-root backend prod image — built and run standalone against the compose network, confirmed `/healthz` → 200
- [x] docker secrets wiring — `docker-compose.yml` now has a top-level `secrets:` block (`postgres_password`, `llm_api_key`, `smtp_pass`, files under gitignored `./secrets/`), mounted into both `postgres` (native `POSTGRES_PASSWORD_FILE`) and `backend`. Since compose can't assemble `DATABASE_URL` from a secret file itself, `internal/config` grew `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD[_FILE]`/`DB_NAME` fields and builds the DSN from parts when `DATABASE_URL` isn't set directly. Verified live: `docker compose up -d --build backend postgres redis`, backend connected via the secret-derived DSN, `/healthz` → `ok`, `/api/me` → real JSON. See `docs/agent_lessons.md` for the "don't set both `POSTGRES_PASSWORD` and `POSTGRES_PASSWORD_FILE`" gotcha.
- [x] Daily cleanup job (`internal/cleanup`, hourly sweep) — expired sessions, otp_codes, webauthn_ceremonies, and stale bill_sessions (+ their orphaned receipt files via `receipts.Storage.Delete`); confirmed live in the running dev stack, it actually cleaned up 2 expired OTP codes and 1 stale WebAuthn ceremony left over from earlier manual testing

## Final verification pass
- [x] `docker compose up` full flow, zero login: create bill → receipt → extract → assign → breakdown → share link → incognito view — done live via curl (backend) and a real browser (frontend, agent-browser at 390×844); extract confirmed to fail gracefully (no `FIREWORKS_API_KEY` configured) rather than crash
- [x] Second anon identity gets 404 on someone else's bill, not leakage — verified via curl (backend) and confirmed the public share view never exposes owner/session id regardless
- [x] `ANON_ACCOUNTS_ENABLED=false` → every route 401s without session — verified, then reverted
- [x] Bill workspace manually tested on narrow mobile viewport emulation — verified live in-browser at 390×844 (agent-browser), including both assignment modes

Only remaining item anywhere in this file: full WebAuthn browser round-trip with a real authenticator. Live Fireworks extraction against a real photo is now verified against the live stack.
