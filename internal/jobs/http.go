package jobs

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
	"fleetops/internal/auth"
)

// Role names used by the jobs route table (README §8, R-6).
const (
	roleAdmin      = "admin"
	roleManager    = "manager"
	roleDispatcher = "dispatcher"
)

// Limits from GF-173. The DB CHECKs enforce the same bounds.
const (
	maxEtaMinutes = 1440
	maxNotesRunes = 1000
	maxWeightKg   = 100000
	dayLayout     = "2006-01-02"
)

// Handler serves the P10 dispatcher routes (GF-177). Every handler takes its
// fleet and actor from the principal, never from the request (GF-27, GF-180).
type Handler struct {
	Svc    *Store
	Events Publisher
}

// Register mounts the job routes. authn is auth.RequireAuth for the
// deployment's issuer. Each route runs its role check before it reads a body (GF-28).
func (h *Handler) Register(e *echo.Echo, authn echo.MiddlewareFunc) {
	writers := auth.RequireRole(roleAdmin, roleDispatcher)
	readers := auth.RequireRole(roleAdmin, roleManager, roleDispatcher)

	e.POST("/jobs", h.create, authn, writers)
	e.GET("/jobs", h.list, authn, readers)
	e.POST("/jobs/:id/assign", h.assign, authn, writers)
	e.POST("/jobs/:id/cancel", h.cancel, authn, writers)
}

type pointBody struct {
	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`
}

type createBody struct {
	Pickup      *pointBody `json:"pickup"`
	Dropoff     *pointBody `json:"dropoff"`
	WindowStart *string    `json:"window_start"`
	WindowEnd   *string    `json:"window_end"`
	EtaMinutes  *int       `json:"eta_minutes"`
	Notes       *string    `json:"notes"`
	SizeClass   *string    `json:"size_class"`
	WeightKg    *float64   `json:"weight_kg"`
}

type assignBody struct {
	VehicleID *int64 `json:"vehicle_id"`
	DriverID  *int64 `json:"driver_id"`
}

type listOut struct {
	Jobs []Job `json:"jobs"`
}

// newJobFrom validates a create body (GF-173). Any failure is ErrValidation.
func newJobFrom(in createBody) (NewJob, error) {
	var out NewJob
	var err error
	if in.Pickup == nil || in.Dropoff == nil {
		return NewJob{}, ErrValidation
	}
	if out.Pickup, err = point(in.Pickup); err != nil {
		return NewJob{}, err
	}
	if out.Dropoff, err = point(in.Dropoff); err != nil {
		return NewJob{}, err
	}
	if out.WindowStart, err = optionalTime(in.WindowStart); err != nil {
		return NewJob{}, err
	}
	if out.WindowEnd, err = optionalTime(in.WindowEnd); err != nil {
		return NewJob{}, err
	}
	if out.WindowStart != nil && out.WindowEnd != nil && !out.WindowEnd.After(*out.WindowStart) {
		return NewJob{}, ErrValidation
	}
	if in.EtaMinutes != nil && (*in.EtaMinutes < 0 || *in.EtaMinutes > maxEtaMinutes) {
		return NewJob{}, ErrValidation
	}
	if in.Notes != nil && utf8.RuneCountInString(*in.Notes) > maxNotesRunes {
		return NewJob{}, ErrValidation
	}
	if in.SizeClass != nil && !validSizeClass(*in.SizeClass) {
		return NewJob{}, ErrValidation
	}
	if in.WeightKg != nil && (*in.WeightKg <= 0 || *in.WeightKg > maxWeightKg) {
		return NewJob{}, ErrValidation
	}
	out.EtaMinutes = in.EtaMinutes
	out.Notes = in.Notes
	out.SizeClass = in.SizeClass
	out.WeightKg = in.WeightKg
	return out, nil
}

func point(p *pointBody) (Point, error) {
	if p.Lat == nil || p.Lng == nil {
		return Point{}, ErrValidation
	}
	if *p.Lat < -90 || *p.Lat > 90 || *p.Lng < -180 || *p.Lng > 180 {
		return Point{}, ErrValidation
	}
	return Point{Lat: *p.Lat, Lng: *p.Lng}, nil
}

// optionalTime parses RFC3339 and returns UTC. Absent or null is nil.
func optionalTime(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, ErrValidation
	}
	u := t.UTC()
	return &u, nil
}

func validSizeClass(s string) bool {
	return s == "small" || s == "medium" || s == "large"
}

// filterFrom reads GET /jobs query parameters (GF-174). A parameter that is
// present must be valid; an empty value is invalid.
func filterFrom(q url.Values) (Filter, error) {
	var f Filter
	if vals, ok := q["state"]; ok {
		if len(vals) != 1 || !State(vals[0]).Valid() {
			return Filter{}, ErrValidation
		}
		s := State(vals[0])
		f.State = &s
	}
	if vals, ok := q["driver"]; ok {
		id, err := parsePositive(vals)
		if err != nil {
			return Filter{}, err
		}
		f.DriverID = &id
	}
	if vals, ok := q["day"]; ok {
		if len(vals) != 1 {
			return Filter{}, ErrValidation
		}
		if _, err := time.Parse(dayLayout, vals[0]); err != nil {
			return Filter{}, ErrValidation
		}
		day := vals[0]
		f.Day = &day
	}
	return f, nil
}

func parsePositive(vals []string) (int64, error) {
	if len(vals) != 1 {
		return 0, ErrValidation
	}
	id, err := strconv.ParseInt(vals[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrValidation
	}
	return id, nil
}

func (h *Handler) create(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in createBody
	if err := bindStrict(c, &in); err != nil {
		return writeErr(c, err)
	}
	nj, err := newJobFrom(in)
	if err != nil {
		return writeErr(c, err)
	}
	job, err := h.Svc.Create(c.Request().Context(), p.UserID, p.FleetID, nj)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusCreated, job)
}

func (h *Handler) list(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	f, err := filterFrom(c.QueryParams())
	if err != nil {
		return writeErr(c, err)
	}
	jobs, err := h.Svc.List(c.Request().Context(), p.FleetID, f)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, listOut{Jobs: jobs})
}

// assign assigns or reassigns (GF-175). Applied and replay are both 200.
func (h *Handler) assign(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in assignBody
	if err := bindStrict(c, &in); err != nil {
		return writeErr(c, err)
	}
	if in.VehicleID == nil || *in.VehicleID <= 0 || in.DriverID == nil || *in.DriverID <= 0 {
		return writeErr(c, ErrValidation)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	ch, out, err := h.Svc.Assign(c.Request().Context(), p.UserID, p.FleetID, id, *in.VehicleID, *in.DriverID)
	if err != nil {
		return writeErr(c, err)
	}
	if out == Applied {
		publishChange(h.Events, ch)
	}
	return c.JSON(http.StatusOK, ch.Job)
}

// cancel moves a job to cancelled (GF-176). Applied and replay are both 200.
func (h *Handler) cancel(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	if err := bindEmptyObject(c); err != nil {
		return writeErr(c, err)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	ch, out, err := h.Svc.Cancel(c.Request().Context(), p.UserID, p.FleetID, id)
	if err != nil {
		return writeErr(c, err)
	}
	if out == Applied {
		publishChange(h.Events, ch)
	}
	return c.JSON(http.StatusOK, ch.Job)
}

// bindStrict decodes one JSON object and refuses unknown fields. Any decode
// failure is ErrValidation, which writeErr maps to 400.
func bindStrict(c echo.Context, v any) error {
	dec := json.NewDecoder(c.Request().Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return ErrValidation
	}
	return nil
}

// bindEmptyObject accepts an empty body or {} and refuses any field, and
// refuses null (GF-176). A map decodes {} to a non-nil empty map and null to nil.
func bindEmptyObject(c echo.Context) error {
	dec := json.NewDecoder(c.Request().Body)
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return ErrValidation
	}
	if fields == nil || len(fields) > 0 {
		return ErrValidation
	}
	return nil
}

// idParam reads :id as a positive integer. A malformed id is treated as absent.
func idParam(c echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

// writeErr maps store sentinels to the GF-178 envelope. Anything else is
// returned so Echo's error handler answers 500.
func writeErr(c echo.Context, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	case errors.Is(err, ErrValidation):
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	case errors.Is(err, ErrConflict):
		return apierr.Write(c, http.StatusConflict, apierr.Conflict)
	case errors.Is(err, ErrVehicleNotActive):
		return apierr.Write(c, http.StatusConflict, apierr.VehicleNotActive)
	default:
		return err
	}
}
