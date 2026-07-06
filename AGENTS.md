# AGENTS.md

The **Share** bill-split app — Go + Postgres + Redis backend, Vite + React frontend. (Formerly "Cher"; see `docs/design_decisions.md`.)

## Reading `@docs` references

When you see an `@docs/...` reference in this file, read that file **on demand** — don't preemptively load everything. Treat loaded content as mandatory context for the task at hand.

## Before non-trivial work

- **Gotchas first**: @docs/agent_lessons.md — many cost real debugging time; check before touching build, Docker, Go JSON, React drawer state, Caddy, or test infra.
- **Rationale**: @docs/design_decisions.md — why non-obvious choices were made (identity model, money math, LLM spend cap, Assign screen layout, etc.).
- **Status**: @docs/todo.md — what's built; only remaining item is a real-browser WebAuthn round trip.

`@docs/plan.md` is the original plan, mostly superseded — see its as-built note. Don't act on it without cross-checking `design_decisions.md` and `todo.md`.

## Commits

- **Commit as you go.** When a logical unit of work is done and the tree is clean (tests pass, lint clean, no half-done refactors), commit it. Don't pile up large uncommitted changes waiting to be reviewed in one shot.
- **Conventional Commits 1.0.0** — see `.claude/skills/conventional-commits/SKILL.md`. Read its `specification.md` before writing any commit message.
- **Inspect before committing**: run `git status`, `git diff`, and `git log --oneline -10`; stage only intended files; never commit secrets (`.env`, `secrets/`).
- **One logical change per commit.** Split unrelated edits into separate commits. If a commit message would need "and" in the subject, it's two commits.