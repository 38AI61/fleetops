package mapview

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
	"fleetops/internal/auth"
)

// maxVehicles caps one response (GF-129). The query asks for one more row to
// learn whether the answer was cut short.
const maxVehicles = 1000

// timeLayout is RFC3339 UTC with millisecond precision and a Z suffix (GF-130).
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// Handler serves GET /map/vehicles (GF-126). PresenceTTL is the offline
// threshold and comes from FLEETOPS_PRESENCE_TTL (GF-132). A nil Jobs means
// no job data (GF-133).
type Handler struct {
	DB          *sql.DB
	Clock       func() time.Time
	PresenceTTL time.Duration
	Jobs        JobLookup
}

// Register mounts the map route behind authn, the same way the registry does.
func (h *Handler) Register(e *echo.Echo, authn echo.MiddlewareFunc) {
	e.GET("/map/vehicles", h.Get, authn)
}

type vehicleOut struct {
	VehicleID  int64         `json:"vehicle_id"`
	Lat        float64       `json:"lat"`
	Lng        float64       `json:"lng"`
	SpeedKmh   *float64      `json:"speed_kmh"`
	HeadingDeg *float64      `json:"heading_deg"`
	Status     DisplayStatus `json:"status"`
	DeviceTS   string        `json:"device_ts"`
	ServerTS   string        `json:"server_ts"`
}

type listOut struct {
	Vehicles  []vehicleOut `json:"vehicles"`
	Truncated bool         `json:"truncated"`
}

// Get answers with the caller's fleet's vehicles inside the bbox (GF-130).
// Row scoping by the token's fleet_id is the isolation boundary (GF-126).
func (h *Handler) Get(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	box, err := ParseBBox(c.QueryParams()["bbox"])
	if err != nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}

	ctx := c.Request().Context()
	rows, err := InBox(ctx, h.DB, p.FleetID, box, maxVehicles+1)
	if err != nil {
		return err
	}
	truncated := len(rows) > maxVehicles
	if truncated {
		rows = rows[:maxVehicles]
	}

	jobs := h.Jobs
	if jobs == nil {
		jobs = noJobLookup{}
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.VehicleID)
	}
	states, err := jobs.ActiveJobStates(ctx, p.FleetID, ids)
	if err != nil {
		return err
	}

	now := h.Clock()
	out := listOut{Vehicles: make([]vehicleOut, 0, len(rows)), Truncated: truncated}
	for _, r := range rows {
		out.Vehicles = append(out.Vehicles, vehicleOut{
			VehicleID:  r.VehicleID,
			Lat:        r.Lat,
			Lng:        r.Lng,
			SpeedKmh:   nullable(r.SpeedKmh),
			HeadingDeg: nullable(r.HeadingDeg),
			Status:     DeriveStatus(r.ServerTS, now, h.PresenceTTL, states[r.VehicleID]),
			DeviceTS:   r.DeviceTS.UTC().Format(timeLayout),
			ServerTS:   r.ServerTS.UTC().Format(timeLayout),
		})
	}
	return c.JSON(http.StatusOK, out)
}

// nullable maps a SQL null to a JSON null.
func nullable(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	return &n.Float64
}
