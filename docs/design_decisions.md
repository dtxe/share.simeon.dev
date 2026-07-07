# Design Decisions

Running log of decisions that aren't obvious from the code. Full original plan: see `docs/plan.md` (copied from the planning session) for complete context; this file is the trimmed, living version — update it when a decision changes instead of leaving it stale.

## Naming: "Cher" → "Share" (2026-07-05)
- App renamed from "Cher" to "Share." Prod deploy target is `share.simeon.dev`, under the user's main site at `simeon.dev`.
- Renamed: Go module path (`cher-app/backend` → `share/backend`), Postgres DB name/user (`cher`/`cher_app` → `share`/`share_app`, required wiping the local dev Postgres volume — only test data was lost), session cookie name (`cher_sid` → `share_sid`), passkey RP display name, `SMTP_FROM`, user-facing strings (page title, Welcome heading, SharedView footer), and the Docker Compose project name (`name: share` in `docker-compose.yml`, so containers/images/volumes are now `share-*`/`share_*` instead of `cher-app-*`/`cher-app_*`).
- **Deliberately not renamed**: the working directory (`/srv/cher-app`) stays as-is — renaming it would break the cwd of any in-progress agent session pointed at it. `docs/plan.md`'s body text (the original historical planning doc) also keeps its original "Cher" references except the title, since it's a frozen historical snapshot, not live config — see its as-built note.
- `.env.example` intentionally still uses localhost-shaped dev values (`PASSKEY_RP_ID=localhost`, `PASSKEY_ORIGIN=http://localhost:5173`, `PUBLIC_BASE_URL=http://localhost:5173`, `CORS_ALLOWED_ORIGIN=http://localhost:5173`). No prod compose/env file exists yet — when one is built, it should set these to `share.simeon.dev`-based values (`PASSKEY_RP_ID=share.simeon.dev`, `PASSKEY_ORIGIN=https://share.simeon.dev`, etc.) and a real `SMTP_FROM` address on that domain.

## Stack
- **Go + chi** backend, **Postgres** (pgx/v5) durable store, **Redis** for ephemeral counters only (rate limits, LLM daily spend cap) — never identity data.
- **Vite + React**, Tailwind v4 + a *minimal* shadcn/ui subset (Button, Input, Drawer/vaul, Badge, Tabs) — not the full catalog, to keep bundle small. `wouter` for routing over react-router (app only has ~6 routes).
- **mise** pins Go/Node versions and defines dev tasks; docker compose is still the actual runtime.
- Go pinned to **1.25** (bumped up from an initial 1.22 pin once it became clear current chi/pgx/migrate/go-redis/go-webauthn releases all require 1.24+). Backend dev hot reload via `air@latest`. Backend's dev-only host port is `8081` (not `8080`) to avoid clashing with the frontend prod nginx image's `8080:80` mapping when compose merges base + override files.
- **docker compose** services: postgres, redis, backend, frontend. OTP email always uses a configured real SMTP relay; no local SMTP catcher is part of the stack.

## Identity: anonymous by default, email/passkey as optional upgrades
Went through several rounds of direction before landing here — earlier sketches (bearer edit/view token pair, then client-id header, then mandatory OTP+passkey login) are all superseded.

- **Anonymous is not a special case.** Every visitor is a real `users` row (nullable email) from their first request, auto-provisioned by the `Identify` middleware. One session mechanism serves anonymous and authenticated users identically — no separate code path.
- **Transport: httpOnly cookie**, not localStorage/header, chosen because the app renders LLM-extracted text and receipt data in the DOM — an XSS-readable identity value (localStorage or a non-httpOnly cookie) would be a real exfiltration risk. `ANON_IDENTITY_TRANSPORT=header` mode exists for split-origin deployments but isn't the default.
- **Email OTP and passkeys are optional, never mandatory** (`ANON_ACCOUNTS_ENABLED=true` default; operators can flip it to force login). They exist purely so a user can get their bill history back on another device — not a gate on using the app.
- **Passkey registration is an in-place upgrade** of the current anonymous user row (no separate signup). Passkey login resolves credential → user and repoints the session; because passkeys sync via the platform's own credential manager (iCloud Keychain, Google Password Manager), this gives real cross-device continuity without ever collecting an email.
- **OTP merge behavior is decisive, not a menu**: verifying an email with no prior owner just attaches it to the current row in place. Verifying an email that's already attached to a *different* row (e.g. a second device with its own anonymous history) triggers an automatic merge — advisory-locked transaction, folds the second row's bills/credentials/sessions onto the first, deletes the now-empty row. No manual reconciliation UI.
- **Public share links stay a separate mechanism**: a 128-bit `crypto/rand` view token (sha256-hashed at rest, `bv_` prefix), addressed via an isolated `httpapi/public` package with no access to session/auth machinery. Edit access, by contrast, is just "does my session's `user_id` match `bill_sessions.owner_user_id`" — no bearer edit-token needed once real per-request identity exists.

## LLM integration
- **Provider interface** (`internal/llm.Provider`), not a hardcoded Fireworks call — `fireworks/` is the default impl, `openai/` is a sibling proving swappability (Fireworks' endpoint is OpenAI-wire-compatible, so this costs little).
- Default model: `accounts/fireworks/models/minimax-m3` via Fireworks' `chat/completions`, image sent as base64 `image_url` content part, and a forced `extract_receipt` tool call used for reliable structured extraction (restaurant name, date, totals, items) instead of prompt+regex parsing. `accounts/fireworks/models/kimi-k2p7-code` remains a supported Fireworks model option.
- LLM pricing is split by input/output tokens. Supported-model pricing is built into config for MiniMax M3 (`0.03` input / `0.12` output cents per 1K tokens) and Kimi K2.7 Code (`0.095` input / `0.4` output cents per 1K tokens); custom models must set both split cost env vars explicitly.
- Reasoning config is model-specific: Kimi K2.7 Code gets Fireworks' `reasoning_effort: "low"`; MiniMax M3 does not get `reasoning_effort` or `thinking` because Fireworks documents M3's `thinking: {"type":"adaptive"}` as the default rather than a low-thinking setting, so the prompt explicitly asks it to minimize thinking and call the extraction tool directly.
- **Cost controls are load-bearing, not decorative**: a global Redis-backed daily spend cap (`LLM_DAILY_SPEND_CAP_CENTS`, default $1/day) checked *before* every call, plus per-IP and per-session extraction caps. This exists because receipt extraction is the one endpoint that costs real money per call and has no natural rate limit otherwise.
- Same modularity pattern applied to **email delivery** (`internal/email.Provider`, SMTP default) — swap providers via env vars only.

## Receipt upload handling
- Validate by sniffing magic bytes (`http.DetectContentType`), not client-supplied Content-Type/extension.
- Dimension check before full decode (decompression-bomb guard), then **always re-encode to JPEG q80** server-side — this strips EXIF/GPS (real privacy leak on an image behind a *public* share link) and neutralizes polyglot-file tricks, not just a size optimization.
- Server-generated filenames only (`{uuid}.jpg`) — no client-controlled byte ever touches a filesystem path.

## Split calculation
- Backend (`internal/split`) is the single source of truth, integer cents throughout, largest-remainder rounding so per-person amounts always sum exactly to the entered total paid. Frontend has a mirrored formula (`lib/split.ts`) but only for instant local preview — the Results screen always reconciles against the backend before showing a number as final.
- Dishes with zero assigned shares are surfaced as "unassigned" warnings, never silently dropped from the math.
- Money columns in Postgres are `BIGINT` cents (`unit_price_cents`, `total_paid_cents`, `subtotal_cents`), not `NUMERIC(10,2)` as an earlier architecture-pass sketch had it — matches "integer cents throughout" exactly and sidesteps numeric/float rounding entirely, at the cost of the DB values not being human-readable as currency without dividing by 100.

## Implementation notes (things that bit us during scaffolding)
- `go:embed` cannot reference parent directories (`../../migrations` fails) — migrations live at `backend/internal/db/migrations/`, embedded from within the `db` package itself, not at the top-level `backend/migrations/` an earlier sketch assumed.
- `golang-migrate`'s `iofs` source requires `{version}_{name}.up.sql` / `.down.sql` filenames, not bare `.sql` — every migration needs both files even though we only call `.Up()` at boot.
- Toolchain: started pinned to Go 1.22 per the original plan, but current chi/pgx/migrate/go-redis/go-webauthn releases all require Go 1.24+ — bumped the pin to **1.25** everywhere (`.mise.toml`, `docker/backend.Dockerfile`, `go.mod`).
- Header-transport identity mode (`ANON_IDENTITY_TRANSPORT=header`) returns the new session token via an `X-Anon-Session-Token` response header (CORS-exposed) rather than a JSON body field — keeps the `Identify` middleware fully generic across every handler instead of coupling it to specific response shapes.

## Frontend: thin client over the live API, not a local-first reducer
The original plan sketch (from before the identity model was finalized) assumed a client-side-first architecture: a big local reducer + localStorage, touching the backend only a few times (extract, share). That's superseded — since every anonymous visitor already gets a real, persisted `bill_sessions` row from the moment they pick an action on Welcome, there's no reason to also maintain a parallel local copy of the same state. The frontend is now a straightforward React Query client against the live API: every screen reads/writes through `lib/api.ts`, and `lib/split.ts`'s local math is used only for the Assign screen's instant per-tap preview, never as a store of record. This is simpler to reason about and means refresh/back-button "just work" for free (the server, not localStorage, is what survives a reload).

Also simplified from plan: skipped installing shadcn/ui's actual component set (Button/Input/Badge/Tabs) — the screens that mattered (Assign's DishRow/PersonChip/PeopleRail) are fully custom anyway, so a generic Button primitive would have added a dependency without saving real work. Kept `vaul` for the two drawers (Total Paid, Share Link) since a proper accessible bottom sheet is genuinely fiddly to hand-roll.

## Assign screen: "focus + rail," not literal two columns
User's original mental model was a literal two-column screen (dishes | people). Design review (delegated to Fable) flagged that true side-by-side columns don't fit a phone viewport legibly (~170px each). Presented three concrete alternatives; user picked **focus + rail**: full-width scrollable dish list, sticky bottom people rail. Tapping a dish turns the rail into assign-controls for that dish; tapping a person (with nothing selected) grows inline steppers on every dish row for that person. Same underlying shares map either direction, one shared selection/adjustment action — preserves the two-way interaction the user wanted without the cramped layout.
