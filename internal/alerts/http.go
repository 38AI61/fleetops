package alerts

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
	"fleetops/internal/auth"
)

// Role names for the alert route table (README §8, GF-298).
const (
	roleAdmin      = "admin"
	roleManager    = "manager"
	roleDispatcher = "dispatcher"
)

// Query bounds for GET /alerts (GF-298).
const (
	defaultWindow = 24 * time.Hour
	defaultLimit  = 50
	maxLimit      = 200
)

// Handler serves the P19 alert routes. Every handler takes its fleet from the
// principal, never from the request (GF-298, GF-269).
type Handler struct {
	Store *Store
	// Clock sets the default since window. Nil means time.Now.
	Clock func() time.Time
}

// Register mounts the alert routes. Each route runs its role check before it
// reads a body (GF-28). Dispatchers read and ack alerts; only admins and managers
// touch rules (GF-298).
func (h *Handler) Register(e *echo.Echo, authn echo.MiddlewareFunc) {
	readers := auth.RequireRole(roleAdmin, roleManager, roleDispatcher)
	ruleEditors := auth.RequireRole(roleAdmin, roleManager)

	e.GET("/alerts", h.list, authn, readers)
	e.PATCH("/alerts/:id/ack", h.ack, authn, readers)
	e.GET("/alert-rules", h.listRules, authn, ruleEditors)
	e.PATCH("/alert-rules", h.patchRules, authn, ruleEditors)
}

func (h *Handler) now() time.Time {
	if h.Clock != nil {
		return h.Clock()
	}
	return time.Now()
}

func (h *Handler) list(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	since := h.now().Add(-defaultWindow)
	if raw := c.QueryParam("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
		}
		since = t
	}
	limit := defaultLimit
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxLimit {
			return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
		}
		limit = n
	}
	page, err := h.Store.List(c.Request().Context(), p.FleetID, since, limit)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, page)
}

func (h *Handler) ack(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		// A malformed id is treated as absent, as in the registry (GF-269).
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	a, err := h.Store.Ack(c.Request().Context(), p.FleetID, id, p.UserID)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, a)
}

// rulesBody is the GET and PATCH /alert-rules body (GF-298).
type rulesBody struct {
	Rules []Rule `json:"rules"`
}

func (h *Handler) listRules(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	rules, err := h.Store.ListRules(c.Request().Context(), p.FleetID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, rulesBody{Rules: rules})
}

func (h *Handler) patchRules(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	patches, err := ParsePatch(c.Request().Body)
	if err != nil {
		return writeErr(c, err)
	}
	rules, err := h.Store.PatchRules(c.Request().Context(), p.FleetID, patches)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, rulesBody{Rules: rules})
}

// writeErr maps the alert sentinels to the apierr envelope. Anything else is
// returned so Echo's error handler answers 500.
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
