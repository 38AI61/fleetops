package reports

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
	"fleetops/internal/auth"
)

// Role names (README §8). All four roles read the report; the driver role reads
// its own row only (GF-324).
const (
	roleAdmin      = "admin"
	roleManager    = "manager"
	roleDispatcher = "dispatcher"
	roleDriver     = "driver"
)

// noDriver is the driver filter for a driver role with no linked driver row.
// BIGSERIAL ids start at 1, so 0 matches no rollup row.
const noDriver int64 = 0

// Handler serves GET /reports/daily (GF-324). Every handler takes its fleet from
// the principal, never from the request.
type Handler struct {
	Store *Store
	// Clock sets the default date. Nil means time.Now.
	Clock func() time.Time
}

// dailyBody is the JSON 200 body (GF-324).
type dailyBody struct {
	Date string `json:"date"`
	Rows []Row  `json:"rows"`
}

// Register mounts the report route behind the auth middleware.
func (h *Handler) Register(e *echo.Echo, authn echo.MiddlewareFunc) {
	e.GET("/reports/daily", h.daily, authn,
		auth.RequireRole(roleAdmin, roleManager, roleDispatcher, roleDriver))
}

func (h *Handler) now() time.Time {
	if h.Clock != nil {
		return h.Clock()
	}
	return time.Now()
}

func (h *Handler) daily(c echo.Context) error {
	ctx := c.Request().Context()
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}

	// Parse every parameter before any lookup, so a bad value is always a 400.
	date := ""
	if raw := c.QueryParam("date"); raw != "" {
		if _, err := time.Parse(time.DateOnly, raw); err != nil {
			return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
		}
		date = raw
	}
	format := c.QueryParam("format")
	switch format {
	case "", "json":
		format = "json"
	case "csv":
	default:
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	var driver *int64
	if raw := c.QueryParam("driver"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
		}
		driver = &id
	}

	tz, err := h.Store.fleetTimezone(ctx, p.FleetID)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fmt.Errorf("fleet %d timezone: %w", p.FleetID, err)
	}
	if date == "" {
		date = LocalDay(h.now(), loc).Format(time.DateOnly)
	}

	if p.Role == roleDriver {
		own, linked, err := h.Store.ownDriver(ctx, p)
		if err != nil {
			return err
		}
		if driver != nil && (!linked || *driver != own) {
			return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
		}
		switch {
		case !linked:
			none := noDriver
			driver = &none
		case driver == nil:
			driver = &own
		}
	} else if driver != nil {
		in, err := h.Store.driverInFleet(ctx, p.FleetID, *driver)
		if err != nil {
			return err
		}
		if !in {
			return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
		}
	}

	if format == "csv" {
		return h.writeCSV(c, p.FleetID, date, driver)
	}
	rows, err := h.Store.Daily(ctx, p.FleetID, date, driver)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, dailyBody{Date: date, Rows: rows})
}

// writeCSV streams the export (GF-327). The query runs before the headers, so a
// database error is still a 500. After the first byte, a write error is only logged.
func (h *Handler) writeCSV(c echo.Context, fleetID int64, date string, driver *int64) error {
	rows, err := h.Store.Rows(c.Request().Context(), fleetID, date, driver)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	res := c.Response()
	res.Header().Set(echo.HeaderContentType, "text/csv; charset=utf-8")
	res.Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="daily-%s.csv"`, date))
	res.WriteHeader(http.StatusOK)
	if err := WriteCSV(res, date, rows, res.Flush); err != nil {
		log.Printf("reports: csv export: %v", err)
	}
	return nil
}
