# Cher — Bill Split App Bootstrap

> **As-built note:** this file is the original planning document, kept for historical context. It does not reflect every decision made during implementation — see `docs/design_decisions.md` for the current rationale and `docs/todo.md` for what's actually built/tested. Known drifts from this plan:
> - Prod ingress uses **Caddy**, not nginx (automatic HTTPS, simpler config) — everywhere below that says "nginx" should read "Caddy."
> - Migrations live at `backend/internal/db/migrations/` (not top-level `backend/migrations/`) — `go:embed` can't reference parent directories.
> - Frontend has no `state/bill.ts` local reducer/localStorage layer — superseded once the identity model settled on real persisted sessions from the first user action; the frontend is a thin React Query client over the live API instead (see design_decisions.md).
> - The bearer edit-token idea from the first design pass never shipped — ownership is session+`owner_user_id`, exactly as the "Reconciliation" note below already anticipated.
> - Frontend skipped shadcn's actual component set (Button/Input/Badge/Tabs); kept only `vaul` for the two drawers.
> - API is consistently camelCase, including LLM-extracted JSON (this plan's `internal/llm` sketch predates that reconciliation).
> - Go pinned to 1.25, not 1.22 (see `docs/agent_lessons.md`).

## Context

Empty repo (`/srv/cher-app`, no git yet). Building from scratch: mobile-first web app to split restaurant bills between friends. User provides receipt (photo or manual entry) + people, assigns portions per dish, app computes weighted split, produces a public read-only share link with the breakdown + receipt image. Uses Kimi K2.7 on Fireworks AI for receipt OCR/extraction, but the LLM provider must stay swappable (interface-based, not hardcoded).

Three design passes ran in parallel up front: overall architecture, frontend UI/UX (delegated to Fable), backend security (delegated to Fable). Identity/auth then went through several rounds of user direction, each one delegated back to the same Fable security agent for a decisive design rather than a menu of options. **Final answer, superseding all earlier identity sketches in this process:** every user starts fully anonymous and the app works completely without ever authenticating (create, assign, share); email and passkeys are optional, never-mandatory upgrades purely for cross-device recall of bill history.

Redis is used narrowly for rate-limit and LLM-spend-cap counters only (ephemeral, no backup needed). Users, sessions, OTP codes, and passkey credentials live in Postgres — deliberately, since the app's fixed stack is Go + Postgres and none of that identity data needs Redis's properties.

Dev environment: **mise** pins Go/Node versions and defines dev tasks, on top of docker compose as the actual runtime. A local SMTP catcher (mailpit) lets OTP emails be read in dev without real email credentials.

## Repo Layout

```
/srv/cher-app
├── .mise.toml                  # pins go, node; [tasks] for dev/build/migrate/test
├── docker-compose.yml
├── docker-compose.override.yml # dev hot-reload bind mounts, mailpit
├── .env.example
├── secrets/                    # gitignored; docker secrets (SMTP password, LLM key, DB creds)
├── .gitignore / .dockerignore  # must exclude .env, secrets/
├── docker/
│   ├── backend.Dockerfile      # multi-stage: dev (air reload) / prod (distroless, non-root)
│   └── frontend.Dockerfile     # multi-stage: dev (vite) / prod (Caddy, same-origin proxy)
├── backend/
│   ├── cmd/server/main.go
│   ├── internal/
│   │   ├── config/             # env + _FILE secret loading
│   │   ├── db/                 # pgx pool, migration runner (golang-migrate, embed.FS)
│   │   ├── auth/
│   │   │   ├── session.go      # unified session issue/verify/renew; cookie or header transport
│   │   │   ├── otp.go          # OTP generation/verification (Postgres-backed), rate limits
│   │   │   ├── webauthn.go     # passkey register/login ceremonies (gated by PASSKEY_ACCOUNTS_ENABLED)
│   │   │   └── merge.go        # mergeAnonymousInto — shared by OTP and passkey login
│   │   ├── email/               # Provider interface (SMTP default) for OTP delivery
│   │   ├── ratelimit/           # Redis-backed per-IP counters + LLM daily spend cap
│   │   ├── httpapi/             # chi handlers; httpapi/public/ mounted separately for view-token routes
│   │   ├── store/                # repository layer
│   │   ├── split/                # pure calculation package, integer cents, unit-tested
│   │   ├── llm/                   # provider interface + fireworks/ and openai/ impls
│   │   └── receipts/              # upload validation, EXIF-strip/re-encode, storage
│   └── migrations/
│       ├── 0001_identity.sql   # users, sessions, otp_codes, webauthn_credentials
│       └── 0002_bills.sql      # bill_sessions, people, dishes, portions, extraction_runs
├── frontend/
│   ├── vite.config.ts           # dev proxy /api -> backend:8080
│   ├── src/
│   │   ├── auth/                # useMe() (GET /api/me), passkey-save banner, login drawer
│   │   ├── state/bill.ts        # reducer + localStorage sync for in-progress bill editing
│   │   ├── lib/split.ts         # mirrors backend cents math, for instant local preview only
│   │   ├── theme.css
│   │   ├── screens/             # Welcome (+history), People, Items, Assign, Results, SharedView
│   │   └── components/          # DishRow, PersonChip, PeopleRail, ShareStepper, SaveHistoryBanner
└── uploads/ (dev bind mount target; named volume in prod)
```

## Identity Model: anonymous by default, email/passkey as optional upgrades

**Core decision:** anonymous is not a special case — it's a real `users` row with no email, using the *exact same* session mechanism as an authenticated user. There is one `Identify` middleware; it never branches on "tier." Every request that reaches a handler already has a `user_id` in context — auto-provisioned on first visit if needed. The only thing tiers affect is client-side nudge UI (an optional "save your history" banner) and one operator-facing switch to disable anonymous access entirely.

**Schema** (`0001_identity.sql`):
```sql
CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email         CITEXT UNIQUE,        -- nullable; unique only among non-null
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash   BYTEA NOT NULL UNIQUE,   -- sha256(token), same pattern used for the public view token
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  user_agent   TEXT,
  created_ip   INET
);
CREATE INDEX ON sessions (expires_at);  -- cleanup sweep

CREATE TABLE webauthn_credentials (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  credential_id BYTEA NOT NULL UNIQUE,
  public_key    BYTEA NOT NULL,
  sign_count    BIGINT NOT NULL DEFAULT 0,
  transports    TEXT[],
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at  TIMESTAMPTZ
);

CREATE TABLE otp_codes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email       CITEXT NOT NULL,
  code_hash   BYTEA NOT NULL,   -- sha256(6-digit code); strength comes from attempt/rate limits, not hash cost
  attempts    INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at  TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ
);
CREATE INDEX ON otp_codes (email, expires_at);
```

Session token: `crypto/rand` 32 bytes, base64url, sha256-hashed at rest, unique-indexed — same generation pattern as the public view token below. One code path (`internal/auth/session.go`) issues/verifies/renews sessions regardless of tier.

**Transport — decisive pick: httpOnly cookie by default.** `ANON_IDENTITY_TRANSPORT=cookie` (`HttpOnly; Secure; SameSite=Lax; Path=/`). Reasoning: the app renders LLM-extracted text and receipt data in the DOM, so an XSS-readable identity value (localStorage, or a non-httpOnly cookie) is a real exfiltration risk that an httpOnly cookie closes off; it also means anonymous and authenticated sessions share one code path with zero special-casing. A `header` mode (`ANON_IDENTITY_TRANSPORT=header`) is still supported for deployments that split API/SPA origins — identical `sessions` table and token, just returned once as JSON and echoed by the client as `X-Anon-Session-Token` instead of arriving via `Set-Cookie`. CSRF: `SameSite=Lax` plus a same-origin CORS policy blocks the classic form-post vector; mutating routes additionally require a custom header (`X-Requested-With: cher-app`) a foreign origin can't attach without failing our CORS preflight.

**`Identify` middleware:**
```
if !ANON_ACCOUNTS_ENABLED and no valid session:
    401 {"error":"login_required"}
read token from cookie or header per ANON_IDENTITY_TRANSPORT
if valid: touch last_seen_at (throttled), slide expires_at, ctx.user_id = session.user_id
else if ANON_ACCOUNTS_ENABLED: create users row (email=null) + sessions row (TTL=ANON_SESSION_TTL_DAYS), set cookie/return token, ctx.user_id = new id
else: 401
```
No route needs a separate `RequireEmail`/`RequirePasskey` guard — every bill operation works identically for a bare anonymous row. The only consumer of tier state is `GET /api/me` (`{hasEmail, hasPasskey}`), driving the optional history-screen banner.

**Env var surface:**
```
ANON_ACCOUNTS_ENABLED=true              # tier-0 master switch; false = every route needs email or passkey identity
ANON_IDENTITY_TRANSPORT=cookie          # cookie | header
ANON_SESSION_COOKIE_NAME=cher_sid
ANON_SESSION_HEADER_NAME=X-Anon-Session-Token   # used only when transport=header
ANON_SESSION_TTL_DAYS=730                # 2yr, sliding
SESSION_TOUCH_MIN_INTERVAL_HOURS=24      # throttle sliding-expiry writes

PASSKEY_ACCOUNTS_ENABLED=false           # tier-1 gate, default OFF
PASSKEY_RP_ID / PASSKEY_RP_NAME / PASSKEY_ORIGIN

EMAIL_OTP_ENABLED=true                   # tier-2 gate, default ON, still never mandatory to use
OTP_CODE_TTL_SECONDS=600
OTP_MAX_ATTEMPTS=5
OTP_RESEND_COOLDOWN_SECONDS=60
OTP_REQUEST_RATE_PER_IP_PER_HOUR=10
EMAIL_PROVIDER=smtp                      # swappable, same pattern as LLM_PROVIDER
SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASSWORD_FILE
```

**Tier 1 — anonymous passkeys, in-place upgrade:** `POST /api/auth/passkey/register` (requires an existing session, gated by `PASSKEY_ACCOUNTS_ENABLED`) attaches a new credential to the **current** `user_id` — no separate signup, no email required. This is literally "anonymous user taps a 'secure your history with a passkey' banner" turning into a durable credential on the same row. `POST /api/auth/passkey/login` resolves `credential_id → user_id` and repoints the session; because passkeys sync via the platform credential manager (iCloud Keychain, Google Password Manager), logging in on a second device with the synced passkey lands on the *same* `users.id` — real cross-device continuity without ever collecting an email. If that second device already had its own separate anonymous history, it's folded in via the same merge routine as OTP (next section).

**Tier 2 — email OTP, exact merge behavior:**
- **No collision** (current anonymous row `U2` verifies an email nobody else has): `UPDATE users SET email = $1 WHERE id = $2` — in place, nothing else moves, since bill ownership already hangs off that row's id.
- **Collision** (`U2` verifies an email already attached to `U1`, e.g. from a different earlier device): auto-merge in one transaction, advisory-locked on the email to serialize concurrent verifications:
  ```sql
  BEGIN;
  SELECT pg_advisory_xact_lock(hashtext($email));
  UPDATE bill_sessions        SET owner_user_id = $U1 WHERE owner_user_id = $U2;
  UPDATE webauthn_credentials SET user_id       = $U1 WHERE user_id       = $U2;
  UPDATE sessions             SET user_id       = $U1 WHERE user_id       = $U2;  -- repoints the calling session too, so the browser stays logged in
  DELETE FROM users WHERE id = $U2;
  COMMIT;
  ```
  No manual reconciliation UI needed — both devices' histories converge under one canonical row the moment the second device proves ownership of the same email.
- OTP mechanics: 6-digit code, 10-minute TTL, max 5 verify attempts (code invalidated after), 60s resend cooldown/email, 10 requests/hour/IP (same rate-limiter as everything else). A daily cleanup job (same ticker that expires stale bill sessions/receipts) also deletes expired `otp_codes`/`sessions` rows.

**Reconciliation with pass 1's original bearer edit-token idea:** superseded. With real per-request identity now in place, "can I edit this bill" is simply "does my session's `user_id` match `bill_sessions.owner_user_id`" — no separate edit-token bearer needed. The **view token is kept** exactly as pass 1 designed it (128-bit `crypto/rand`, sha256-hashed, `bv_` prefix) since that's still the right mechanism for a link meant to be handed to people with no account at all.

## Backend

**Router:** chi. **DB:** postgres via `pgx/v5`, sqlc-generated or hand-written parameterized queries. Two DB roles: `cher_migrate` (schema owner) and `cher_app` (runtime, no DDL grants). **Redis:** rate-limit and LLM-spend-cap counters only — nothing identity-related lives there.

**Bill schema** (`0002_bills.sql`):
- `bill_sessions`: id (uuid pk), `owner_user_id` (fk users, `ON DELETE SET NULL`, indexed), title, restaurant_name, bill_date, subtotal, total_paid, receipt_image_path, `view_token_hash` (unique, sha256 of a 128-bit token), `expires_at` (default now()+60d, bumped on mutation), `extract_count`, timestamps
- `people` (id, session_id fk cascade, name, sort_order)
- `dishes` (id, session_id fk cascade, name, unit_price, quantity, line_total generated, source: manual|llm_extracted)
- `portions` (dish_id fk cascade, person_id fk cascade, shares numeric check >=0, unique(dish_id,person_id))
- `extraction_runs` (audit trail: provider, model, raw_response jsonb, status)
- CHECK constraints mirroring API validation bounds (price/shares/total ranges)

**Endpoints:**
```
GET    /api/me                            {hasEmail, hasPasskey} — drives optional save-history banner
POST   /api/auth/otp/request              {email} rate-limited
POST   /api/auth/otp/verify               {email, code} -> in-place upgrade or merge (see above)
POST   /api/auth/passkey/register         [session] attach credential to current user
POST   /api/auth/passkey/login            resolve credential -> user, repoint session (merge if needed)
POST   /api/auth/logout                   clears session

POST   /api/sessions                      [identified] create bill, owner = current user
GET    /api/me/bills                      [identified] history: bills owned by current user
GET    /api/sessions/:id                  [owner check] full detail
PATCH  /api/sessions/:id                  [owner check] title/restaurant/date/total_paid
POST   /api/sessions/:id/people           [owner check] add person
PATCH  /api/people/:id / DELETE           [owner check via session]
POST   /api/sessions/:id/dishes/bulk      [owner check] replace dish list
PATCH  /api/dishes/:id / DELETE           [owner check via session]
PUT    /api/portions                      [owner check via dish's session] upsert {dish_id, person_id, shares}
GET    /api/sessions/:id/breakdown        [owner check] computed split (source of truth)
POST   /api/sessions/:id/receipt          [owner check] multipart upload, 10MiB cap
POST   /api/sessions/:id/extract          [owner check] triggers LLM extraction
POST   /api/sessions/:id/share            [owner check] (re)generate view token, returns share URL
GET    /api/view/{viewToken}              PUBLIC read-only breakdown
GET    /api/view/{viewToken}/receipt      PUBLIC read-only image
GET    /healthz
```
Public/share routes live in an isolated `httpapi/public` package with no access to sessions/cookies/auth middleware, and never serialize the view-token hash, owner id, or internal UUID beyond the URL the user chose to share.

**Split calculation** (`internal/split/calculate.go`, pure function, unit-tested, integer cents throughout):
```
dish_total_shares      = Σ shares for that dish
person_pct_of_dish     = person_shares / dish_total_shares   (dish flagged "unassigned" if 0, surfaced not silently dropped)
person_pct_of_subtotal = Σ_dishes(person_pct_of_dish(d) * dish.line_total) / subtotal
person_owes            = person_pct_of_subtotal * total_paid
```
Largest-remainder rounding so per-person amounts always sum exactly to `total_paid`. Single source of truth for both `/api/sessions/:id/breakdown` and `/api/view/{token}`; frontend's `lib/split.ts` mirrors it only for instant local preview.

**LLM provider abstraction** (`internal/llm`):
```go
type Provider interface {
    ExtractReceipt(ctx context.Context, image []byte, mimeType string) (*ExtractedReceipt, error)
    Name() string
}
```
Config: `LLM_PROVIDER`, `LLM_BASE_URL`, `LLM_MODEL`, `LLM_API_KEY`/`LLM_API_KEY_FILE`. Default impl calls Fireworks' OpenAI-compatible `chat/completions` with model `accounts/fireworks/models/kimi-k2p7-code`, image as base64 `image_url` content part, `response_format: json_schema` for reliable structured output. OpenAI-wire-compatible, so an `openai/` sibling package proves swappability.

**Email provider abstraction** (`internal/email`), same modularity pattern:
```go
type Provider interface {
    SendOTP(ctx context.Context, to, code string) error
}
```
Default: SMTP — works against any real provider's relay (SES/Postmark/Sendgrid) with zero code change, and against **mailpit** in dev.

**Receipt upload pipeline** (`internal/receipts`): `http.MaxBytesReader` 10MiB cap → `http.DetectContentType` on first 512 bytes (ignore client Content-Type/extension) → `image.DecodeConfig` dimension check (reject >40MP) → full decode + **re-encode to JPEG q80** (strips EXIF/GPS, neutralizes polyglot files) → store as `{uuid}.jpg`, filename never derived from client input.

**Abuse / cost controls (Redis-backed):**
- `internal/ratelimit`: fixed-window counters (`INCR`+`EXPIRE`) per IP — global `60 req/min/IP`, `10/hour/IP` on `POST /api/sessions`, `5/hour/IP` on `POST /api/sessions/:id/extract`, `10/hour/IP` on `POST /api/auth/otp/request` (plus the Postgres-tracked per-email cooldown/attempt caps above), `30/min/IP` on invalid `/api/view/*` lookups.
- **Global LLM spend cap**: `LLM_DAILY_SPEND_CAP_CENTS=100` ($1/day) via Redis key `llm:spend:{utc-date}`, incremented by estimated cost from Fireworks' returned token `usage` × `LLM_COST_PER_1K_TOKENS_CENTS`, ~25h TTL for daily self-reset. Checked *before* calling the provider; 503 once it would be exceeded.
- Per-session `extract_count` cap (max 5, race-safe conditional `UPDATE`) as a belt-and-suspenders limit if Redis is ever unavailable.
- 60s upstream timeout, bounded `max_tokens`, LLM output parsed with `DisallowUnknownFields`, extracted values re-validated through the same bounds as manual input.

**Headers/CORS:** same-origin in prod (Caddy fronts both frontend+backend); dev allows only the Vite origin. Security headers: `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, CSP on the SPA shell.

## Frontend

**Stack:** Vite + React, Tailwind v4 + minimal shadcn/ui subset (Button, Input, Drawer via vaul, Badge, Tabs), wouter for routing, lucide-react icons, zustand + localStorage for in-progress bill editing state, React Query for backend sync. Bundle budget <120KB gz. All API calls use `credentials: 'include'`.

**Routes:** `/`, `/bill/:id/people`, `/bill/:id/items`, `/bill/:id/assign`, `/bill/:id/results`, `/s/:viewToken`. No `/login` gate — the app is usable immediately; `GET /api/me` auto-provisions an anonymous identity on first call via the cookie the backend sets.

**Screens:**
- **Welcome** — three `BigActionCard`s (scan receipt / type items manually / add people first); below them, a **"Your bills" history list** from `GET /api/me/bills` (works immediately, anonymously); if `!hasEmail && !hasPasskey`, a dismissible `SaveHistoryBanner` ("Save your splits — add an email or passkey to get them back on another device") opens a lightweight drawer offering "Email me a code" or "Use a passkey" (only shown if `PASSKEY_ACCOUNTS_ENABLED`)
- **People** — text input + chips, round-robin 8-color palette
- **Items** — shared `ItemEditorList` for manual entry and post-extraction review; extraction loading state; flags item-sum vs receipt-total mismatches; receipt thumbnail with fullscreen zoom viewer
- **Assign** (confirmed layout: **focus + rail**, not literal two columns) — full-width scrollable `DishRow` list; sticky bottom `PeopleRail` with `PersonChip`s. Tap a dish → rail becomes tap-to-assign chips for that dish (tap = +1 share, "−" appears once >0, "Split evenly" pill). Tap a person chip with nothing selected → dish list grows an inline `ShareStepper` per row for that person. One shared `assignments` map, one `ADJUST_SHARES(itemId, personId, delta)` action regardless of direction. Exit gate warns (doesn't block) on unassigned dishes, offering to split the remainder evenly.
- **Total paid** — Drawer from a sticky `TotalBar` on Assign and Results (editable any time)
- **Results** — per-person `PersonResultCard` (accordion, line items, largest-remainder-adjusted total), "Share link" CTA → `POST /api/sessions/:id/share`, "Edit split" back-link
- **SharedView** (`/s/:token`) — separate code-split chunk, chrome-free, all person cards pre-expanded, receipt image inline, no inputs, no identity calls at all

**Money math** (`lib/split.ts`): integer cents, mirrors backend formula, used only for zero-latency preview while adjusting shares; Results always reconciles against `GET /api/sessions/:id/breakdown` before display.

**Mobile specifics:** 44px+ touch targets (48px steppers/chips), `touch-action: manipulation`, ≥16px input font, safe-area padding on sticky bars, `visualViewport` handling on Items so sticky bars don't fight the keyboard, client-side downscale of receipt photos before upload.

**Accessibility:** `aria-live` rail caption announcing mode/share changes, `aria-pressed`/`aria-label` on chips, identity never conveyed by color alone, plain semantic HTML on SharedView.

## Build Order

1. `.mise.toml` + repo scaffold + `docker-compose.yml` (postgres, redis, mailpit, backend health endpoint)
2. `0001_identity.sql` + `internal/auth/session.go` (cookie-based, anonymous auto-provision) + `Identify` middleware — a working "hit any endpoint, get an anonymous identity" loop before anything else
3. `0002_bills.sql` + `store` CRUD + `internal/split` with unit tests (correctness-critical, testable independent of frontend/LLM/auth extras)
4. OTP (`internal/email` + `internal/auth/otp.go`) and merge logic (`internal/auth/merge.go`)
5. Passkey ceremonies (`internal/auth/webauthn.go`, go-webauthn), gated by `PASSKEY_ACCOUNTS_ENABLED`
6. `internal/ratelimit` (Redis counters) + LLM daily spend cap plumbing
7. `internal/llm` interface + Fireworks impl, smoke-tested against real receipt photos via curl before wiring to HTTP
8. `internal/receipts` upload pipeline
9. `httpapi` handlers wiring store+split+llm+ratelimit+auth, full endpoint list
10. Frontend scaffold (vite, tailwind, shadcn subset, wouter, zustand) + Welcome (history + save-history banner)/People/Items screens
11. Assign screen (bulk of frontend work) + Total paid drawer + Results
12. Wire receipt upload/extraction end to end; SharedView + share-link creation
13. Prod Docker paths (Caddy same-origin proxy, distroless backend) + secrets wiring + final compose

## Verification

- `internal/split` unit tests: even split, uneven shares, zero-share (unassigned) dish, rounding reconciliation (owed amounts sum exactly to `total_paid`)
- Identity loop in dev: hit `GET /api/me` cold, confirm a cookie + anonymous user appear with no client action required; create a bill, confirm it's in `GET /api/me/bills`; request an OTP, read the code from mailpit, verify, confirm the *same* bill now shows `hasEmail: true` with no data loss; simulate the collision case (two anonymous identities verifying the same email) and confirm they merge into one bill history
- If `PASSKEY_ACCOUNTS_ENABLED=true`: register a passkey on an anonymous identity, confirm login via that passkey from a fresh session lands on the same user/history
- Manual curl smoke test of `internal/llm` Fireworks client against 2-3 real receipt photos before wiring to the upload endpoint
- `internal/ratelimit` tests against real Redis confirming per-IP windows reset and the daily spend cap actually blocks once the configured cent threshold is hit
- `docker compose up` brings up postgres+redis+mailpit+backend+frontend; full flow with zero login: create a session, upload a receipt, confirm extraction, assign shares, fetch breakdown, create a share link, load `/s/:token` in an incognito window and confirm it renders with no cookies sent and exposes no owner id or internal token
- Confirm a second, unrelated anonymous identity gets 404 on someone else's bill id, not data leakage
- Set `ANON_ACCOUNTS_ENABLED=false` and confirm every route now 401s without a session, to validate the operator escape hatch works
- Manually test the Assign screen on an actual narrow mobile viewport (devtools device emulation at minimum) for the focus+rail interaction before considering it done
