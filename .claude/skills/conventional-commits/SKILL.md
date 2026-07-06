---
name: conventional-commits
description: Use when writing or amending git commit messages in this repository. Ensures commit messages follow the Conventional Commits 1.0.0 specification. Triggers on requests to "commit", "make a commit", "commit this", "write a commit message", "amend commit", "fix commit message", or any task involving creating or rewriting commit messages. Also triggers when staging and committing changes via git.
---

# Conventional Commits

All commit messages in this repository MUST follow the Conventional Commits 1.0.0 specification. Before writing any commit message, **read** the specification in full:

- `.claude/skills/conventional-commits/specification.md`

The specification defines the structure (`<type>[scope]: <description>`, optional body, optional footers), the rules for `feat` and `fix` types, how to indicate breaking changes (`!` in the prefix or `BREAKING CHANGE:` footer), and the full normative rules. Follow it exactly.

## Workflow

1. **Read** `.claude/skills/conventional-commits/specification.md` before drafting any message.
2. Inspect the staged (or to-be-staged) changes to determine the correct type and scope:
   - `feat` — new feature
   - `fix` — bug fix
   - `build`, `chore`, `ci`, `docs`, `style`, `refactor`, `perf`, `test` — other allowed types
   - Add `!` after the type/scope (or a `BREAKING CHANGE:` footer) when the change is breaking
3. Compose the message per the spec: a short imperative-mood description, optional body explaining *why*, and optional footers (`BREAKING CHANGE:`, `Refs: #123`, `Reviewed-by:`, etc.).
4. Only commit when the user has explicitly requested it — never commit on your own initiative.

## Quick reference

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

Breaking change indicators:

- `feat(api)!: drop legacy auth endpoint`
- `fix!: drop support for Node 6` (with `BREAKING CHANGE:` footer recommended)

When in doubt, prefer splitting the work into multiple commits so each one has a single clear type.