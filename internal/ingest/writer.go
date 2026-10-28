package ingest

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/lib/pq"

	"fleetops/internal/geofence"
)

// qResolveDevices maps each device to its paired vehicle (GF-75).
const qResolveDevices = `
SELECT d.id, v.id, v.fleet_id
FROM devices d
JOIN vehicles v ON v.paired_device_id = d.id
WHERE d.id = ANY($1::bigint[]) AND d.revoked_at IS NULL`

// qInsertPositions appends paired, fresh messages to the partitioned history (GF-77).
const qInsertPositions = `
INSERT INTO positions (msg_id, fleet_id, vehicle_id, geom, speed_kmh, heading_deg, accuracy_m, device_ts, server_ts)
SELECT u.msg_id, u.fleet_id, u.vehicle_id,
       ST_SetSRID(ST_MakePoint(u.lng, u.lat), 4326)::geography,
       u.speed_kmh::real, u.heading_deg::real, u.accuracy_m::real, u.device_ts, u.server_ts
FROM unnest($1::uuid[], $2::bigint[], $3::bigint[], $4::float8[], $5::float8[], $6::float8[],
            $7::float8[], $8::float8[], $9::text[]::timestamptz[], $10::text[]::timestamptz[])
     AS u(msg_id, fleet_id, vehicle_id, lng, lat, speed_kmh, heading_deg, accuracy_m, device_ts, server_ts)
ON CONFLICT (server_ts, msg_id) DO NOTHING`

// qUpsertLatest writes one row per vehicle, guarded so an older batch never
// overwrites a newer row (GF-76).
const qUpsertLatest = `
INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, speed_kmh, heading_deg, device_ts, server_ts)
SELECT u.vehicle_id, u.fleet_id,
       ST_SetSRID(ST_MakePoint(u.lng, u.lat), 4326)::geography,
       u.speed_kmh, u.heading_deg, u.device_ts, u.server_ts
FROM unnest($1::bigint[], $2::bigint[], $3::float8[], $4::float8[], $5::float8[], $6::float8[],
            $7::text[]::timestamptz[], $8::text[]::timestamptz[])
     AS u(vehicle_id, fleet_id, lng, lat, speed_kmh, heading_deg, device_ts, server_ts)
ON CONFLICT (vehicle_id) DO UPDATE SET
  fleet_id    = EXCLUDED.fleet_id,
  geom        = EXCLUDED.geom,
  speed_kmh   = EXCLUDED.speed_kmh,
  heading_deg = EXCLUDED.heading_deg,
  device_ts   = EXCLUDED.device_ts,
  server_ts   = EXCLUDED.server_ts
WHERE EXCLUDED.server_ts > vehicles_latest.server_ts`

// FlushResult reports one flush. Every message of the batch is in exactly one
// of Stored, Stale, or Unpaired, so the caller acks the whole batch on nil error (GF-79).
type FlushResult struct {
	Stored   []Message
	Stale    int
	Unpaired int
	// Observed is the stored fixes in (server_ts, msg_id) order, set after commit
	// (GF-278). The geofence hook reads it. Stale and unpaired messages are not in it.
	Observed []geofence.Observation
}

// Writer runs the flush write path (GF-74 to GF-79). Flush is serialised by a
// mutex; the P6 consumer is the expected single caller.
type Writer struct {
	db       *sql.DB
	counters *Counters

	mu     sync.Mutex
	smooth map[int64]smoothState
}

// NewWriter returns a writer with an empty heading state.
func NewWriter(db *sql.DB, c *Counters) *Writer {
	return &Writer{db: db, counters: c, smooth: map[int64]smoothState{}}
}

// route is a device's paired vehicle and that vehicle's fleet.
type route struct {
	vehicleID int64
	fleetID   int64
}

// pairedMsg is a fresh message resolved to its vehicle.
type pairedMsg struct {
	msg       Message
	vehicleID int64
}

// Flush writes one batch in one transaction. On error it rolls back and
// changes no counter and no heading state; the batch is not modified.
func (w *Writer) Flush(ctx context.Context, batch []Message, now time.Time) (FlushResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var res FlushResult
	fresh := make([]Message, 0, len(batch))
	for _, m := range batch {
		if isStale(m.ServerTS, now) {
			res.Stale++
			continue
		}
		fresh = append(fresh, m)
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return FlushResult{}, fmt.Errorf("begin flush: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	routes, err := resolveDevices(ctx, tx, fresh)
	if err != nil {
		return FlushResult{}, err
	}

	var paired []pairedMsg
	for _, m := range fresh {
		r, ok := routes[m.DeviceID]
		if !ok || r.fleetID != m.FleetID {
			res.Unpaired++
			continue
		}
		paired = append(paired, pairedMsg{msg: m, vehicleID: r.vehicleID})
		res.Stored = append(res.Stored, m)
	}

	ordered := slices.Clone(paired)
	slices.SortFunc(ordered, func(a, b pairedMsg) int { return compareMsg(a.msg, b.msg) })

	// Heading EMA runs over the sorted order on a staged copy; the map is
	// replaced only after COMMIT (GF-74, GF-78).
	next := map[int64]smoothState{}
	for _, p := range ordered {
		st, ok := next[p.vehicleID]
		if !ok {
			st = w.smooth[p.vehicleID]
		}
		next[p.vehicleID] = st.next(p.msg.Position.Heading, p.msg.ServerTS)
	}

	if len(ordered) > 0 {
		if err := insertPositions(ctx, tx, ordered); err != nil {
			return FlushResult{}, err
		}
		if err := upsertLatest(ctx, tx, ordered, next); err != nil {
			return FlushResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return FlushResult{}, fmt.Errorf("commit flush: %w", err)
	}
	committed = true

	maps.Copy(w.smooth, next)
	w.counters.Stored.Add(int64(len(res.Stored)))
	w.counters.Stale.Add(int64(res.Stale))
	w.counters.Unpaired.Add(int64(res.Unpaired))
	res.Observed = make([]geofence.Observation, len(ordered))
	for i, p := range ordered {
		res.Observed[i] = geofence.Observation{
			VehicleID: p.vehicleID,
			FleetID:   p.msg.FleetID,
			MsgID:     p.msg.MsgID,
			ServerTS:  p.msg.ServerTS,
			Lng:       p.msg.Position.Lng,
			Lat:       p.msg.Position.Lat,
		}
	}
	return res, nil
}

// resolveDevices runs the GF-75 query for the distinct device ids in msgs.
func resolveDevices(ctx context.Context, tx *sql.Tx, msgs []Message) (map[int64]route, error) {
	seen := map[int64]bool{}
	var ids []int64
	for _, m := range msgs {
		if !seen[m.DeviceID] {
			seen[m.DeviceID] = true
			ids = append(ids, m.DeviceID)
		}
	}
	out := map[int64]route{}
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := tx.QueryContext(ctx, qResolveDevices, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("resolve devices: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var deviceID int64
		var r route
		if err := rows.Scan(&deviceID, &r.vehicleID, &r.fleetID); err != nil {
			return nil, fmt.Errorf("scan device route: %w", err)
		}
		out[deviceID] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolve devices: %w", err)
	}
	return out, nil
}

// insertPositions writes every paired message, including duplicates (GF-77).
func insertPositions(ctx context.Context, tx *sql.Tx, msgs []pairedMsg) error {
	n := len(msgs)
	msgIDs := make(pq.StringArray, n)
	fleetIDs := make(pq.Int64Array, n)
	vehicleIDs := make(pq.Int64Array, n)
	lngs, lats := make(pq.Float64Array, n), make(pq.Float64Array, n)
	speeds, headings := make(pq.Float64Array, n), make(pq.Float64Array, n)
	accs := make(pq.Float64Array, n)
	deviceTS, serverTS := make(pq.StringArray, n), make(pq.StringArray, n)
	for i, p := range msgs {
		m := p.msg
		msgIDs[i] = m.MsgID.String()
		fleetIDs[i] = m.FleetID
		vehicleIDs[i] = p.vehicleID
		lngs[i], lats[i] = m.Position.Lng, m.Position.Lat
		speeds[i], headings[i] = m.Position.Speed, m.Position.Heading
		accs[i] = m.Position.Accuracy
		deviceTS[i] = m.Position.DeviceTS.UTC().Format(time.RFC3339Nano)
		serverTS[i] = m.ServerTS.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, qInsertPositions,
		msgIDs, fleetIDs, vehicleIDs, lngs, lats, speeds, headings, accs, deviceTS, serverTS); err != nil {
		return fmt.Errorf("insert positions: %w", err)
	}
	return nil
}

// upsertLatest writes the last message per vehicle (the sort order's winner)
// with the EMA heading after that message (GF-76, GF-78).
func upsertLatest(ctx context.Context, tx *sql.Tx, msgs []pairedMsg, heading map[int64]smoothState) error {
	winner := map[int64]Message{}
	for _, p := range msgs {
		winner[p.vehicleID] = p.msg
	}
	vehicles := slices.Sorted(maps.Keys(winner))

	n := len(vehicles)
	vehicleIDs := make(pq.Int64Array, n)
	fleetIDs := make(pq.Int64Array, n)
	lngs, lats := make(pq.Float64Array, n), make(pq.Float64Array, n)
	speeds, headings := make(pq.Float64Array, n), make(pq.Float64Array, n)
	deviceTS, serverTS := make(pq.StringArray, n), make(pq.StringArray, n)
	for i, v := range vehicles {
		m := winner[v]
		vehicleIDs[i] = v
		fleetIDs[i] = m.FleetID
		lngs[i], lats[i] = m.Position.Lng, m.Position.Lat
		speeds[i] = m.Position.Speed
		headings[i] = heading[v].heading
		deviceTS[i] = m.Position.DeviceTS.UTC().Format(time.RFC3339Nano)
		serverTS[i] = m.ServerTS.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, qUpsertLatest,
		vehicleIDs, fleetIDs, lngs, lats, speeds, headings, deviceTS, serverTS); err != nil {
		return fmt.Errorf("upsert vehicles_latest: %w", err)
	}
	return nil
}

// compareMsg orders by (server_ts, msg_id), the GF-78 order.
func compareMsg(a, b Message) int {
	if c := a.ServerTS.Compare(b.ServerTS); c != 0 {
		return c
	}
	return bytes.Compare(a.MsgID[:], b.MsgID[:])
}
