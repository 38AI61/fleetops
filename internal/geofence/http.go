package geofence

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
	"fleetops/internal/auth"
)

// Role names for the geofence route table (README §8, GF-268).
const (
	roleAdmin      = "admin"
	roleManager    = "manager"
	roleDispatcher = "dispatcher"
)

// Handler serves the P17 geofence routes. Every handler takes its fleet from
// the principal, never from the request (GF-269).
type Handler struct {
	Store *Store
}

// Register mounts the geofence routes. Each route runs its role check before
// it reads a body (GF-28), so a wrong role gets 403 whatever the body holds.
// README §8 has dispatchers drawing fences, so writes are admin and dispatcher (GF-268).
func (h *Handler) Register(e *echo.Echo, authn echo.MiddlewareFunc) {
	readers := auth.RequireRole(roleAdmin, roleManager, roleDispatcher)
	writers := auth.RequireRole(roleAdmin, roleDispatcher)

	e.GET("/geofences", h.list, authn, readers)
	e.POST("/geofences", h.create, authn, writers)
	e.DELETE("/geofences/:id", h.delete, authn, writers)
}

func (h *Handler) list(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	col, err := h.Store.List(c.Request().Context(), p.FleetID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, col)
}

func (h *Handler) create(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	in, err := Parse(c.Request().Body)
	if err != nil {
		return writeErr(c, err)
	}
	f, err := h.Store.Create(c.Request().Context(), p.FleetID, in)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusCreated, f)
}

func (h *Handler) delete(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		// A malformed id is treated as absent, as in the registry (GF-269).
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	if err := h.Store.Delete(c.Request().Context(), p.FleetID, id); err != nil {
		return writeErr(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// writeErr maps the geofence sentinels to the apierr envelope. Anything else
// is returned so Echo's error handler answers 500.
func writeErr(c echo.Context, err error) error {
	switch {
	case errors.Is(err, ErrValidation):
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	case errors.Is(err, ErrNotFound):
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	case errors.Is(err, ErrConflict):
		return apierr.Write(c, http.StatusConflict, apierr.Conflict)
	}
	return err
}
