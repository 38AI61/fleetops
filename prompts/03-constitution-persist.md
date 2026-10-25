# Constitution — persist (auto mode)

Goal: persist the approved constitution plan into `specs/constitution/{mission.md, tech-stack.md, roadmap.md}` and commit. Do NOT implement anything.

Context (read first):
- The approved plan is at `/root/.claude/plans/constitution-plan-jazzy-stream.md` — it is the complete instruction: file outlines (§1), roadmap phases 1–23 (§2), decision log D-01…D-41 (§3), self-gate checklist (§4). This prompt only points at it; do not restate or alter its decisions.
- `README.md` (repo root) is the binding product spec (§1–§9 primary; quote/derive, never contradict). `deploy/PORTS.md` and `deploy/docker-compose.yml` are the existing Phase 0 scaffold facts.
- Standing instruction (no user available): the plan's §5 "confirm before writing" items are pre-approved — record them in the spec files exactly as decided, each with its D-ID and rationale. Note in mission.md §7 that they were auto-approved under the no-questions standing instruction.
- AGENTS.md and CLAUDE.md exist at repo root; respect their rules.

Write:
1. `specs/constitution/mission.md` — per plan §1.1, including §7 "Approved deviations from README" listing every SPEC-AMEND with its D-ID.
2. `specs/constitution/tech-stack.md` — per plan §1.2, including the full decision log (all D-01…D-41 with rationales), the pinned stack, additive schema/API/env lists, messaging transport split, testing preferences.
3. `specs/constitution/roadmap.md` — per plan §1.3: working rules, Phase 0 marked done (commit `b74201d`), phases 1–23 each with scope/validation/testing depth, unscheduled backlog (§2.2 + §15 + accepted risks), cross-phase NFR gates.
4. `prompts/03-constitution-persist.md` — this prompt file itself (copy it into prompts/ as provenance if it is not already there; it will be committed with the specs).

Style constraints for all three files: markdown, terse, tables where natural; every decision carries its D-ID; no content that contradicts README; additive schema/API changes marked SPEC-AMEND; no code.

Self-gate before committing (do this yourself and note the outcome in your report): run through the plan's §4 checklist against your three files — every §2.1 item mapped, every §5 table/column present or amended with ID, every §6 endpoint assigned, §7 mechanics present, §8 matrix enforceable, §9 targets each with a measuring phase, ports within 10000–10019, zero open decisions, the three files mutually consistent (no contradiction between them).

Done when: the three files exist, self-gate checklist passes (quote the checklist result), `git add specs/ prompts/03-constitution-persist.md` and ONE commit with message `Add constitution specs`; `git status` clean; report files with line counts, the checklist outcome, and `git log --oneline -5`.

Out of scope: any code, compose changes, CI files, CLAUDE.md/AGENTS.md edits, phase-1 implementation, README edits.