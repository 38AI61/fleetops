# P9 fix — CLAUDE.md install row (host quirk)

Goal: fix one line in CLAUDE.md so the frontend install command works on this host.

Context: this host has `NODE_ENV=production` set globally, so `npm ci` omits devDependencies by default (verified 2026-10-26: `npm --prefix apps/web ci` alone leaves vitest/tsc/vite missing; `npm --prefix apps/web ci --include=dev` makes frontend gates 1–4 green). The current CLAUDE.md row therefore fails for any future session.

Task: in CLAUDE.md § "Frontend (P9)", change the Install row to `npm --prefix apps/web ci --include=dev`. Change nothing else — no other line, no other file.

Done when: `git diff` shows exactly that one-line change and you have committed it with message `Fix frontend install row in CLAUDE.md for NODE_ENV=production hosts (P9)`.

Out of scope: everything else — no other CLAUDE.md edits, no code, no specs, no README, no push.

Report: the diff and the commit hash.