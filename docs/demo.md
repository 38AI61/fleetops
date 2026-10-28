# FleetOps demo (5 minutes)

The walkthrough follows mission §51. Ports and environment names are in CLAUDE.md, so the steps below point there instead of repeating them.

## Before you start

1. Bring up the stack and run the local services (CLAUDE.md, "Local infrastructure", "Local API", "Local ingest consumer", "Local WS hub"). Start the web dispatcher and the driver PWA (CLAUDE.md, "Frontend" and "Driver PWA").
2. Seed a fresh database with the demo data. Set the device key salt as CLAUDE.md describes, then run:

   ```
   SEED_ARGS="--admin-email <email>" make seed-demo
   ```

   Enter the admin password at the prompt. The first seed creates the fleet, the admin, 50 demo vans with trackers, and 50 demo drivers. A rerun on a seeded database exits 0 and changes nothing (GF-340). To start the demo again, use a fresh database.

## 1. Log in (0:00)

Open the web dispatcher (CLAUDE.md, "Frontend") and sign in as the admin you seeded.

## 2. Fifty vans move (0:45)

In a second terminal, run the simulator against the local API (CLAUDE.md, "Local simulator"):

```
go run ./tools/simulator --fleet=50 --rate=5s --api <API base URL>
```

The map shows 50 vans moving along their loops within a few seconds.

## 3. Draw the depot geofence (1:30)

Open `/zones`, draw a polygon around the depot, give it a name, and save. The evaluator records entry and exit events as vans cross it (CLAUDE.md, "Geofence evaluator").

## 4. Create and assign the nearest van (2:15)

Open `/jobs/new`, pick a pickup and a dropoff on the map, and create the job. Open its assignment panel on `/jobs`. The candidates are ordered by distance, so choose the first one and assign it (CLAUDE.md, "Nearest and assignment").

## 5. The driver accepts and delivers (3:00)

Open the driver PWA and sign in as a demo driver. Use the email and password of an entry in `deploy/.local/demo-users.json`, for example `demo-driver-001@example.com`. Accept the job, start it, pick it up, and deliver it with a photo and a location (CLAUDE.md, "Driver PWA" and "Proof of delivery").

## 6. The photo appears (3:45)

Back on `/jobs`, the delivered job shows its proof photo. The link is presigned and valid for 15 minutes (CLAUDE.md, "Proof of delivery").

## 7. A device-silent alert (4:00)

Stop the simulator and wait for the silence threshold. The default `threshold_s` is 600 seconds, so for a shorter run lower it first with `PATCH /alert-rules` (CLAUDE.md, "Alerts"). A `device_silent` warning then appears on the bell in the topbar. Acknowledge it there.

## 8. The daily report (4:45)

Open `/reports` and pick today's date. It shows jobs completed, on-time share, distance and idle time per driver (CLAUDE.md, "Reports"). The figures come from the 15-minute rollup, so a figure may lag until the next pass.

## Nightly load run (documentation only)

The load scenarios are not part of the demo. A host with k6 installed and the stack running can run them nightly. The `TOKEN` value is an access JWT that expires, so the cron entry needs a script that logs in first (CLAUDE.md, "Load and demo (P23)"):

```
30 2 * * * cd <repo> && <script that sets TOKEN from a fresh login> && make loadtest >> deploy/.local/loadtest/nightly.log 2>&1
```
