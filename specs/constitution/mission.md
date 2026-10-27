# Mission — FleetOps

Status: approved constitution, 2026-10-25. Binding source: `README.md` §1–§9. Decisions: `tech-stack.md` (D-01…D-41). Sequencing: `roadmap.md` (Phases 1–23).
Rule: README is never hand-edited (AGENTS.md). Anything beyond README is a **SPEC-AMEND** recorded in §7 below.

## 1. Vision

FleetOps is an operations platform for small and medium courier and field-service fleets (100–500 vans): live vehicle tracking, job dispatch, geofencing, alerting, and driver tools in one system. It replaces three disconnected tools: a GPS vendor portal (positions only), chat (job coordination), and a spreadsheet (planning). Goal: dispatchers see the nearest van and assign in seconds; managers answer "why was yesterday's on-time rate 71%?"; drivers work from one phone app that tolerates bad signal.

Portfolio framing (README pitch): high-throughput geospatial telemetry, batch ingestion under backpressure, WebSocket fanout, offline-tolerant driver PWA.

## 2. Users & roles

| User | Needs (README §1) | Primary phases |
|---|---|---|
| Dispatcher | Live map, create/assign jobs in seconds, alerts | P8, P9, P10, P13, P19 |
| Fleet manager | Daily ops reports, registry, exportable history | P3, P21 |
| Driver | Today's jobs, one-tap status, works with bad signal | P11, P14, P15 |
| Admin | User management, device pairing, API keys, fleet settings | P2, P3, P19 |

Roles: `admin`, `manager`, `dispatcher`, `driver` (README §2.1.2, §8). Permissions per resource are the §8 matrix; it is enforced by the role-matrix test (tech-stack §9, D-37) and is authoritative over this summary.

## 3. In scope (MVP) — README §2.1

| # | Item (README §2.1) | Roadmap phases |
|---|---|---|
| 1 | Fleet registry: vehicles, drivers, device pairing with history | P3 |
| 2 | Auth & roles: JWT login, four roles, `fleet_id` scoping | P2, P3 (matrix), P11 (driver matrix) |
| 3 | Live tracking: device POST `/ingest/position`, latest + append-only history | P4, P5, P6, P7 (fanout), P14 (PWA posting) |
| 4 | Operations map: status colors, clustering, details card, follow mode, 2-min gray-out | P7, P8, P9 |
| 5 | Job lifecycle: states, driver transitions, photo + coordinates at delivery | P10, P11, P12, P13, P14, P15, P16 |
| 6 | Geofences: polygon/circle draw; enter / exit / dwell events | P17, P18 |
| 7 | Alerts: device silent, geofence exit outside schedule, ETA slip; bell; email stub | P19, P20 |
| 8 | Reports: per-day/per-driver jobs, on-time %, distance, idle; CSV | P21 |

Cross-cutting: NFRs (§9) P22–P23. Test-standard gates: every phase (roadmap §1).

## 4. Success criteria

Measured targets (README §9) — detail and measuring phase in roadmap §5:

| Target | Value | Measured by |
|---|---|---|
| Ingest steady | 100 msg/s | P5 smoke, P23 k6 |
| Ingest burst | 2,000 msg/s (10×) | P23 k6, no unbounded memory |
| Freshness | p95 ≤ 2 s from device send to map | P23 (simulator timestamps) |
| API latency | p95 ≤ 300 ms, assign + report endpoints | P23 k6 |
| Observability | ingest rate, flush sizes, WS conns, geofence latency; assign trace | P22 |
| Testing | Go unit, testcontainers integration, k6, one Playwright | Every phase; P16; P23 |

Demo (README §10 demo script, D-39): dispatcher logs in → 50 simulated vans move → draw depot geofence → create and assign job to nearest van → driver accepts and advances → delivery photo appears → device-silent alert fires → open daily report. Script lives in `docs/demo.md` (D-39); completed in P23. Demo is manual by design (roadmap P16 note).

## 5. Out of scope — README §2.3 (verbatim)

Payroll, customs/international compliance, native mobile apps, customer-facing booking, pricing/invoicing.

## 6. Backlog (unscheduled)

Full list in `roadmap.md` §4 (README §2.2 stretch items, §15 cloud profile, accepted-risk follow-ups). Nothing in the backlog is in MVP scope.

## 7. Approved deviations from README

**Approval basis:** every SPEC-AMEND below was auto-approved under the no-questions standing instruction on 2026-10-25 (no user available). The plan §5 items (D-06, D-08, D-22, D-26, D-28, D-32, D-33, D-29, D-17, D-16) are pre-approved on that basis. Items marked **(added at self-gate)** were not in the plan's §5 list; they were added so README requirements have an owning phase. They are flagged for review at the next user contact.

| ID | Deviation / addition | README ref | Type | Phase |
|---|---|---|---|---|
| D-06 | `GET/POST/PATCH /users` (admin) | §1 admin, §6 | SPEC-AMEND additive endpoint | P2 |
| D-08 | `alert_rules` table; `GET/PATCH /alert-rules` (admin, manager); three fixed kinds, parameters configurable | §2.1.7, §5, §6 | SPEC-AMEND additive table + endpoints | P19 |
| D-09 | `alerts.acked_at` column; one-time `NULL → value` ack transition, trigger-enforced | §5 alerts, §8 audit | SPEC-AMEND additive column | P19 |
| D-16 | JetStream stream `GPS_RAW`; NATS container runs with `-js` | §7 (fire-and-forget), §12 | SPEC-AMEND additive infra | P4 |
| D-17 | Redis carries `position.batch` and `presence.changed`; NATS carries `jobs.events` and `alerts`; the §7 row putting `positions.batch` on NATS is superseded by §3 and §4 | §3, §4, §7 (internal conflict) | SPEC-AMEND override of §7 row | P7 |
| D-22 | Optional `FLEETOPS_*` tunables; §12 names stay required | §12 | SPEC-AMEND additive env | P5 |
| D-26 | `jobs`: `notes`, `size_class`, `weight_kg`, `delivery_geom`, `failure_reason`; `geofences.dwell_min` | §2.1.5, §5 | SPEC-AMEND additive columns | P10, P17 |
| D-28 | `vehicle_pairings` table; partial unique indexes; `vehicles.paired_device_id` mirror | §2.1.1, §5 | SPEC-AMEND additive table | P3 |
| D-29 | `devices.revoked_at`; key rotate and revoke semantics | §8 (rotatable, revocable) | SPEC-AMEND additive column | P3 |
| D-32 | `daily_driver_stats` rollup table | §2.1.8, §9 API p95 | SPEC-AMEND additive table | P21 |
| D-33 | `geofences`: `radius_m`, `bbox`, `dwell_min`; `geofence_events` table | §2.1.6, §5 | SPEC-AMEND additive columns + table | P17, P18 |
| D-01 / D-02 | `users` and `refresh_tokens` tables (new) | §5 has no users table; §2.1.2, §8 | SPEC-AMEND additive tables | P2 |
| D-23 | `job_transitions` reference table, read by the state trigger (new) | §2.1.5 edges; §5 state trigger | SPEC-AMEND additive table | P10 |
| D-02 / D-03 | `POST /auth/refresh`, `POST /auth/logout`; cookie-based refresh | §6 (login only), §8 | SPEC-AMEND additive endpoints | P2 |
| D-23 | `POST /jobs/:id/cancel`; `POST /driver/jobs/:id/fail` | §2.1.5 states, §6 | SPEC-AMEND additive endpoints | P10, P11 |
| D-29 / D-30 | Device key endpoints: issue, rotate, revoke (admin) | §8 device keys | SPEC-AMEND additive endpoints | P3 |
| D-28 | `GET/POST /vehicles/:id/pairings` (pairing history) | §2.1.1 | SPEC-AMEND additive endpoints | P3 |
| D-14 | `positions` parent table partitioned by range on `server_ts` | §5 (`positions_YYYY_MM` only) | Structural; children unchanged | P5, P6 |
| D-13 | CI, golangci-lint, golang-migrate, Makefile move to Phase 1 (README §10 lists them under Phase 0) | §10 (status list, not §1–§9) | Schedule deviation | P1 |
| D-07 | MinIO compose service added in P12, not Phase 0 (README §12 lists it) | §12 | Timing deviation | P12 |
| D-41 | Cloud profile (§15) deferred to backlog | §15 (not §1–§9) | Deferral | Backlog |
| D-18 (interpretation) | Stale (>60 s) messages dropped at every flush, not only under load | §7 "under load" | Interpretation, stricter | P5 |
| GF-1 | Driver **push notifications** deferred; MVP uses WS (`job.updated`, D-21) + polling fallback | §3 "push when online" | SPEC-AMEND deferral | Backlog |
| GF-2 | `admin_audit(id, fleet_id, actor_id, action, target, at)` append-only table; every admin write records the actor | §8 "admin actions logged with actor" | SPEC-AMEND additive table | P2, P3 |
| GF-3 | `POST /devices/:id/rotate` and `POST /devices/:id/revoke` (paths chosen here; plan named the operations only) | §8 | SPEC-AMEND additive endpoints | P3 |
| GF-12 | Test and CI PostGIS image tag `postgis/postgis:16-3.4` (`testinfra.PostgisImage`); README §12 and compose keep `16` (not a published tag) until compose is touched in P4 | §12 | SPEC-AMEND image tag correction | P1 |
| GF-247 | CI `e2e` job runs the Go services and Vite dev servers as host processes against a compose subset (postgres, redis, nats, minio; `osrm` not started); browsers installed per run | §9 (testing), §7 process | SPEC-AMEND addition | P16 |

Rule: any new SPEC-AMEND gets a row here and a decision ID (D-nn from the plan, or GF-n for self-gate additions) in `tech-stack.md` §11 (D-40).
