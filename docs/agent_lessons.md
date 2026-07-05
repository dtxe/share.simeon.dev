# Agent Lessons — gotchas hit while building Cher

Unconventional, easy-to-miss things future work on this repo should know. Ordinary decisions belong in `design_decisions.md`; this file is specifically for traps that cost real debugging time.

## Go toolchain / build

- **`go:embed` cannot reference parent directories.** `//go:embed ../../migrations/*.sql` fails to compile ("pattern must not contain '..'"). Migrations had to move from a top-level `backend/migrations/` sketch to `backend/internal/db/migrations/`, embedded from inside the `db` package itself.
- **golang-migrate's `iofs` source requires `{version}_{name}.up.sql` / `.down.sql` filenames**, not bare `.sql`. Both files are required even though the app only ever calls `.Up()` — missing the `.down.sql` (or misnaming either) fails at `migrate.NewWithInstance` with a cryptic "file does not exist."
- **`GOTOOLCHAIN=auto` can fail with "toolchain not available" even when a matching toolchain is already cached locally** (`~/go/pkg/mod/golang.org/toolchain@v0.0.1-goX.Y.Z...`). If you hit this, run `go env -w GOTOOLCHAIN=goX.Y.Z` to pin explicitly rather than fighting the auto-download path.
- Go got bumped from the originally-planned 1.22 to **1.25** mid-build: current releases of chi, pgx/v5, golang-migrate, go-redis/v9, and go-webauthn all require Go 1.24+. If pinning an older Go for a new project like this, expect to hit this wall almost immediately.
- **`air` (hot reload)'s latest release requires Go ≥1.25.** If stuck on an older Go, pin an older `air` version too (was pinned to `v1.52.3` during the brief Go-1.22 window; reverted to `@latest` once Go was bumped).

## Docker / compose

- **`docker-compose.override.yml`'s list-valued fields (`ports`, `volumes`, etc.) are unioned with the base file's, not replaced**, when both are loaded together (the default for `docker compose up`). Two services each publishing to the same host port across base+override will collide even though neither file alone looks wrong. Backend's dev-only port ended up as `8081` specifically to avoid colliding with the frontend prod image's `8080:80` mapping from the base file.
- The target directory (`/srv/cher-app`) was **root-owned** at the very start (`drwxr-xr-x root root`) — needed a `sudo chown` before any scaffolding could happen. Worth an `ls -ld` check before assuming a fresh directory is writable.

## JSON / API shape

- **Go nil slices serialize to JSON `null`, not `[]`.** Every `internal/store` function that builds a list with `var out []T` returns `null` when the list is empty, which crashes a naive frontend `.map()` over it. Always initialize as `out := []T{}` (or `make([]T, 0, ...)`) when the slice will be JSON-encoded. This one crashed the whole app on the very first brand-new bill (zero dishes/people) and only surfaced via actual browser testing — `go vet`/`tsc`/unit tests all stayed green.
- **Public API responses must be hand-built, not reused owner-facing DTOs**, even when it'd save a few lines — `store.Person` carries `sessionId`, which is exactly the kind of field a public/share endpoint must never leak. Map to an explicit minimal shape at the handler boundary instead of passing the domain struct straight to `json.Encode`.
- If you send a model a `response_format: json_schema`, **the JSON schema's property names and your Go struct's `json` tags must match exactly** — they're two independent places that both encode the same contract, and nothing will catch a mismatch except a runtime parse failure (or, worse, silent field-dropping since we `DisallowUnknownFields`). Keep them in the same file/PR when either changes.

## React / frontend

- **A controlled "open" prop (vaul `Drawer.Root`, Radix `Dialog.Root`, etc.) does not remount the component when it closes and reopens** — only the portal's visibility toggles. A `useState(() => computeInitial())` lazy initializer only ever runs once, on first mount, so a drawer that recomputes its prefill from props will silently show stale data on the second open. Fix: a `useEffect` keyed on the `open` boolean that resyncs local state every time it flips true. Hit this exact bug in `TotalPaidDrawer`.
- **When a UI shows a breakdown that's supposed to sum to a displayed total, apply the same scaling to every line, not just the header.** `PersonResultCard` computed per-dish line items from the raw pre-tip subtotal share while the header used the tip-scaled final amount — so "$8.00 + $6.00" visibly didn't add up to "$15.56" above it. Any time a total is adjusted by a factor (tip, discount, rounding), that factor needs to propagate to every displayed component of it.

## Testing

- **Rate-limit/spend-cap tests that hit real Redis can pollute the exact keys the live dev server is using**, if the key is date-based (`rl:llm_spend:{utc-date}`) rather than test-scoped. A test pushed the real daily LLM-spend counter past its cap mid-session, and the live `/extract` endpoint started 503ing for an unrelated reason. Either give test keys their own namespace/prefix or have the test clean up its own delta via `t.Cleanup` (what we did here) — don't assume "it's just a test" means it can't affect the running app when both share one Redis instance.
- Prefer testing against the **real** dependency (real Redis via `docker compose exec backend go test ...`, real Postgres) over mocks for this kind of infrastructure code — but that means real infra-level side effects are also real, per above.

## Caddy

- **Caddy's default *directive* execution order is fixed and independent of the order directives appear in the Caddyfile.** `try_files` runs before `reverse_proxy` in that fixed order, so a naive Caddyfile with a bare `@api path /api/*` + `reverse_proxy` alongside a bare `try_files {path} /index.html` will have the SPA fallback silently rewrite `/api/*` requests to `/index.html` *before* `reverse_proxy` ever sees them — the API proxy never fires, and every API call just returns the SPA's HTML shell (still a 200, so it's easy to miss without actually inspecting the response body). Fix: wrap each concern in its own `handle` block — `handle` blocks are mutually exclusive and evaluated in file order (first match wins, nothing after it runs), which sidesteps the fixed-directive-order problem entirely. Always test a Caddy reverse-proxy setup by checking the actual response *body* of a proxied route, not just its status code.

## Environment / tooling

- **`agent-browser` needs `--args "--no-sandbox"` in this containerized environment**, or Chrome fails to launch (zygote sandbox error). The flag only takes effect on daemon *launch* — if a daemon is already running without it, `agent-browser close` first.
- **Bash tool calls do not share shell state with each other** (env vars set via `export` in one call are gone in the next; only the working directory persists). Any multi-step flow that threads a variable from one command's output into a later command's input (e.g. curl → extract an id → curl again) must happen inside a *single* Bash invocation, not split across several.
- Signed commits via `gpg.format=ssh` depend on `ssh-add -l` actually reaching a live agent. A broken `~/.ssh/ssh_auth_sock` symlink (e.g. pointing at itself, or at a socket from a dead session) fails `git commit` with the unhelpful "Couldn't get agent socket?" — check `ssh-add -l` directly before assuming the commit itself is broken. Also: the auto-mode permission layer treats touching `~/.ssh` or adopting a foreign ssh-agent socket as out-of-scope and will block it without explicit user sign-off — don't try to route around that block, just ask/wait.
- **mailpit's REST API** (`GET http://localhost:8025/api/v1/messages`) is a convenient way to read OTP codes during automated/dev testing without a real inbox — the message `Snippet` field contains the plain-text body.

## Remaining TODO (not yet done)

- **Full WebAuthn round trip** — ceremony endpoints are live and return real challenges, but completing a registration/login needs a real browser + platform authenticator, which hasn't been exercised (curl can't fake WebAuthn attestation).
- **Live Fireworks extraction** — blocked on a real `FIREWORKS_API_KEY`; the graceful-failure path (502, no leaked upstream detail) is confirmed, but no real receipt photo has been run through it yet.

Done since first written: daily cleanup job (`internal/cleanup`, wired into `main.go`, confirmed live removing expired OTP codes/WebAuthn ceremonies) and the Caddy prod-ingress build (both prod Docker images built and smoke-tested standalone — see `docs/todo.md` step 13 for details, including the Caddy directive-order bug above).

## Docker secrets

- `docker-compose.yml` now has a top-level `secrets:` block backed by files in `./secrets/` (gitignored): `postgres_password`, `llm_api_key`, `smtp_pass`.
- **`DATABASE_URL` can't be assembled by compose itself** — compose has no built-in way to interpolate a secret file's contents into another env var's value at the YAML level. Instead, `internal/config` grew discrete `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME` fields; if `DATABASE_URL` isn't set directly, `Load()` builds it from those parts, and `DB_PASSWORD` resolves via the existing `_FILE` convention (`DB_PASSWORD_FILE=/run/secrets/postgres_password`). Both the `postgres` container and the `backend` container mount the *same* secret file, so there's one password with two independent read paths (postgres's native `POSTGRES_PASSWORD_FILE` support, and our own `getEnv`).
- **Don't set both `POSTGRES_PASSWORD` and `POSTGRES_PASSWORD_FILE`** on the postgres official image — it refuses to start if both are present. Once a secret file is wired, drop the plain env var entirely (removed from both `.env` and `.env.example`), don't just leave it unused as a fallback.
- Empty secret files (e.g. `llm_api_key.txt`, `smtp_pass.txt` are 0 bytes in dev — no LLM key yet, mailpit needs no auth) resolve to `""` via `_FILE`, same as an unset plain env var would — no special-casing needed for "optional" secrets.
- Swapping `POSTGRES_PASSWORD` → `POSTGRES_PASSWORD_FILE` recreates the postgres *container* (env changed) but does **not** reinitialize the *volume* — first-run init scripts only run once against an empty data dir. As long as the secret file's contents match whatever password the volume was originally initialized with, auth keeps working across the switch. If they don't match, the fix is `ALTER USER` from inside the running container, not a volume wipe.
