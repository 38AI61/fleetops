Fix task on branch `phase-20-eta-slip-osrm` (repo root is your cwd). P20 validation check (e) failed: `wc -l CLAUDE.md` is 204, and `specs/features/20-eta-slip-osrm.md` check (e) requires it below 200. The fix: condense pre-existing CLAUDE.md bullet groups into fewer lines with ZERO content loss — no command, env name, port, rule, or qualifier may be dropped. Apply exactly the four replacements below with the Edit tool using the exact old strings. If any old string does not match exactly, STOP, change nothing, and report which one mismatched. Touch nothing else: no code files, no other CLAUDE.md lines, no README, no merge, no push.

1. Replace these two bullets (the two `make seed` lines):

OLD:
- `make seed` — bootstrap the first fleet and admin (D-05). Run `SEED_ARGS="--admin-email <email>" make seed`. The password comes from a TTY prompt or one line on stdin, never a flag. Exit 0 when already seeded (no prompt); exit 2 on usage errors.
- `SEED_ARGS="--admin-email <email> --demo-devices" make seed` — first seed only: also creates 50 demo vehicles with tracker devices and writes `deploy/.local/devices.json`. The flag needs `DEVICE_KEY_SALT` (at least 32 bytes).

NEW (one bullet):
- `make seed` — bootstrap the first fleet and admin (D-05). Run `SEED_ARGS="--admin-email <email>" make seed`; add ` --demo-devices` on the first seed only to also create 50 demo vehicles with tracker devices and write `deploy/.local/devices.json` (the flag needs `DEVICE_KEY_SALT`, at least 32 bytes). The password comes from a TTY prompt or one line on stdin, never a flag. Exit 0 when already seeded (no prompt); exit 2 on usage errors.

2. Replace these two bullets (the JWT_SECRET / DEVICE_KEY_SALT lines):

OLD:
- `JWT_SECRET` must be at least 32 bytes or the API and `make seed` refuse to start; generate one per shell with `openssl rand -hex 32`.
- `DEVICE_KEY_SALT` is required and must be at least 32 bytes, or the API refuses to start before it opens the database (GF-30). Generate one per shell with `openssl rand -hex 32`. It is the HMAC key for device keys (D-29) and license hashes (D-31).

NEW (one bullet):
- `JWT_SECRET` must be at least 32 bytes or the API and `make seed` refuse to start, and `DEVICE_KEY_SALT` is required and must be at least 32 bytes, or the API refuses to start before it opens the database (GF-30); generate each per shell with `openssl rand -hex 32`. `DEVICE_KEY_SALT` is the HMAC key for device keys (D-29) and license hashes (D-31).

3. Replace these five lines (the Migration rules group: the parent bullet and its four sub-bullets, note the two-space indent on the sub-bullets):

OLD:
- Migration rules (GF-5):
  - Files are `db/migrations/NNN_snake_name.up.sql` and `.down.sql`, sequential from `001`, no timestamps.
  - Each file is one transaction-friendly script: no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`.
  - Migrations are forward-only once merged to `main`; `down` exists for dev and the round-trip test.
  - A dirty `schema_migrations` makes `make migrate` fail. Fix the schema by hand, then `force`. Never edit `schema_migrations` by SQL.

NEW (one bullet):
- Migration rules (GF-5): files are `db/migrations/NNN_snake_name.up.sql` and `.down.sql`, sequential from `001`, no timestamps; each file is one transaction-friendly script (no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`); migrations are forward-only once merged to `main`, with `down` kept for dev and the round-trip test; a dirty `schema_migrations` makes `make migrate` fail — fix the schema by hand, then `force`, and never edit `schema_migrations` by SQL.

4. Replace these two bullets (the Deliver route line and the Success line) with ONE bullet; leave the Errors bullet that follows untouched:

OLD:
- Deliver route: `POST /driver/jobs/:id/deliver`, driver role only, own jobs only, valid from `picked_up` only. Multipart fields: `photo` (file; type sniffed by `http.DetectContentType` on the first 512 bytes, declared Content-Type ignored; JPEG, PNG or WebP; at most 5242880 bytes), `lat` and `lng` (text, finite, lat in [-90, 90], lng in [-180, 180]).
- Success is 200 with the job object. A replay of `delivered` is 200 with no event, no publish and no upload; the body is still validated.

NEW (one bullet):
- Deliver route: `POST /driver/jobs/:id/deliver`, driver role only, own jobs only, valid from `picked_up` only. Multipart fields: `photo` (file; type sniffed by `http.DetectContentType` on the first 512 bytes, declared Content-Type ignored; JPEG, PNG or WebP; at most 5242880 bytes), `lat` and `lng` (text, finite, lat in [-90, 90], lng in [-180, 180]). Success is 200 with the job object; a replay of `delivered` is 200 with no event, no publish and no upload; the body is still validated.

Replacement 1 saves 1 line, 2 saves 1, 3 saves 4, 4 saves 1: seven lines total. After all four edits, run `wc -l CLAUDE.md` — it must print exactly 197. If it does not, STOP without committing and report the actual count.

Then update `specs/features/20-eta-slip-osrm.md` (the appended "Validation results and deviations" section at the end):

5. Replace the paragraph that starts with `- **(e) CLAUDE.md size. FAIL.**` (the whole bullet, ending with `...relax the `< 200` limit.`) with:

- **(e) CLAUDE.md size. FAIL in the run, then fixed.** The implement run left `wc -l CLAUDE.md` at 204 (baseline 197) against the `< 200` cap; its `## ETA (P20)` block is 7 lines, under the 12-line cap, and the run correctly refused to delete existing reference lines. A follow-up commit on this branch condensed four pre-existing bullet groups (the two seed lines, the JWT/DEVICE_KEY_SALT lines, the Migration rules group, the Deliver route/Success lines) into fewer lines with no content loss, bringing the file to 197. Under 200. PASS.

6. Replace the final line that starts with `**Result.**` with:

**Result.** Checks (a), (b), (c), (e), (f), (g), (h), (i), (j), (l), and the runnable part of (k) PASS. Check (d) passes on the diff, and `make e2e` is NOT RUN. The real `prepare.sh` run is NOT RUN. Check (e) initially FAILED (204 lines) and was fixed by the condensation commit described in (e) above (197 lines).

Finally: run `wc -l CLAUDE.md` (expect 197), then commit ONLY `CLAUDE.md` and `specs/features/20-eta-slip-osrm.md` with the message:

Trim CLAUDE.md for the P20 line cap (204 -> 197, no content loss)

Leave `prompts/` untracked. Report: the diff stat, the final `wc -l` output, and the commit hash.