package dispatch

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
	"fleetops/internal/auth"
)

// Role names used by the nearest route (README §8, R-6).
const (
	roleAdmin      = "admin"
	roleManager    = "manager"
	roleDispatcher = "dispatcher"
)

// Limit bounds for the nearest read (GF-211).
const (
	defaultLimit = 10
	maxLimit     = 50
)

// timeLayout is RFC3339 UTC with millisecond precision, as in mapview (GF-130).
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// Handler serves GET /jobs/:id/nearest (GF-213).
type Handler struct {
	Store *Store
}

// Register mounts the route behind authn. The role check runs before the query (GF-215).
func (h *Handler) Register(e *echo.Echo, authn echo.MiddlewareFunc) {
	readers := auth.RequireRole(roleAdmin, roleManager, roleDispatcher)
	e.GET("/jobs/:id/nearest", h.nearest, authn, readers)
}

type candidateOut struct {
	VehicleID  int64   `json:"vehicle_id"`
	Plate      string  `json:"plate"`
	DriverID   int64   `json:"driver_id"`
	DriverName string  `json:"driver_name"`
	DistanceM  float64 `json:"distance_m"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	ServerTS   string  `json:"server_ts"`
}

type nearestOut struct {
	JobID      int64          `json:"job_id"`
	Candidates []candidateOut `json:"candidates"`
}

// nearest answers the eligible vehicles nearest to the job's pickup (GF-214).
// The caller's fleet from the token is the only scope.
func (h *Handler) nearest(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	limit, ok := limitParam(c.QueryParams()["limit"])
	if !ok {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}

	ctx := c.Request().Context()
	lat, lng, err := h.Store.JobPickup(ctx, p.FleetID, id)
	if errors.Is(err, ErrNotFound) {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	if err != nil {
		return err
	}
	cands, err := h.Store.Nearest(ctx, p.FleetID, lat, lng, limit)
	if err != nil {
		return err
	}

	out := nearestOut{JobID: id, Candidates: make([]candidateOut, 0, len(cands))}
	for _, cd := range cands {
		out.Candidates = append(out.Candidates, candidateOut{
			VehicleID:  cd.VehicleID,
			Plate:      cd.Plate,
			DriverID:   cd.DriverID,
			DriverName: cd.DriverName,
			DistanceM:  cd.DistanceM,
			Lat:        cd.Lat,
			Lng:        cd.Lng,
			ServerTS:   cd.ServerTS.UTC().Format(timeLayout),
		})
	}
	return c.JSON(http.StatusOK, out)
}

// limitParam reads the optional limit (GF-211). Absent means the default. Any
// other value must be one integer in 1..50.
func limitParam(vals []string) (int, bool) {
	if len(vals) == 0 {
		return defaultLimit, true
	}
	if len(vals) != 1 {
		return 0, false
	}
	n, err := strconv.Atoi(vals[0])
	if err != nil || n < 1 || n > maxLimit {
		return 0, false
	}
	return n, true
}

// idParam reads the job id. A malformed or non-positive id is absent (GF-188).
func idParam(c echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}
