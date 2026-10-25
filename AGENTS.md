# AGENTS.md

Rules for AI agents working in this repo.

## Source of truth

- `specs/` is the binding source of truth. Read it before any non-trivial change. (It is created in the constitution step; until then, README.md is the spec.)
- Never hand-edit `README.md`. Changes to the product spec go through the spec process, not direct edits.

## How work is done (SDD)

- Spec-driven development: a constitution in `specs/`, then one spec per feature.
- Each feature spec lives on its own branch. Implement against the spec on that branch; do not fold unrelated work into it.
- Do not change behavior that a spec does not describe. If the spec is silent or wrong, stop and ask.

## Secrets

- No secrets in the repo: no passwords, tokens, API keys, or real `.env` values.
- Config comes from environment variables. Only variable names are documented (see `CLAUDE.md`).
- `.env` and `.env.*` are gitignored; keep them that way.
