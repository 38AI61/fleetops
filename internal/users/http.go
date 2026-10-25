package users

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
	"fleetops/internal/audit"
	"fleetops/internal/auth"
)

// Audit actions for admin user writes (GF-2).
const (
	ActionCreate        = "user.create"
	ActionUpdate        = "user.update"
	ActionPasswordReset = "user.password_reset"
)

// Handler serves /users. Routes are wired in services/api with RequireAuth and
// RequireRole("admin") in front.
type Handler struct {
	DB    *sql.DB
	Store *Store
	Clock func() time.Time
}

type createRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type patchRequest struct {
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
	Password *string `json:"password"`
}

// List implements GET /users: the caller's fleet only, no password hashes.
func (h *Handler) List(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	list, err := h.Store.List(c.Request().Context(), p.FleetID)
	if err != nil {
		return err
	}
	if list == nil {
		list = []User{}
	}
	return c.JSON(http.StatusOK, list)
}

// Create implements POST /users. fleet_id comes from the principal, never the body (GF-27).
func (h *Handler) Create(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in createRequest
	if err := c.Bind(&in); err != nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	email, ok := parseEmail(in.Email)
	if !ok || !ValidRole(in.Role) {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	if err := auth.CheckPolicy(in.Password); err != nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.WeakPassword)
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	tx, err := h.Store.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	u, err := h.Store.Create(ctx, tx, p.FleetID, email, hash, in.Role)
	if errors.Is(err, ErrConflict) {
		return apierr.Write(c, http.StatusConflict, apierr.Conflict)
	}
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Record{
		FleetID: p.FleetID, ActorID: p.UserID, Action: ActionCreate, Target: target(u.ID),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create user: %w", err)
	}
	return c.JSON(http.StatusCreated, u)
}

// Update implements PATCH /users/:id. Order (GF-28): body validation (400),
// self-lockout (409), then fleet scope (404). Only a successful write audits.
func (h *Handler) Update(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in patchRequest
	if err := c.Bind(&in); err != nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	if in.Role == nil && in.Disabled == nil && in.Password == nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	if in.Role != nil && !ValidRole(*in.Role) {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}

	patch := Patch{Role: in.Role, Disabled: in.Disabled}
	action := ActionUpdate
	if in.Password != nil {
		if err := auth.CheckPolicy(*in.Password); err != nil {
			return apierr.Write(c, http.StatusBadRequest, apierr.WeakPassword)
		}
		hash, err := auth.HashPassword(*in.Password)
		if err != nil {
			return err
		}
		patch.PasswordHash = &hash
		action = ActionPasswordReset
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}

	selfDemote := in.Role != nil && *in.Role != "admin"
	selfDisable := in.Disabled != nil && *in.Disabled
	if id == p.UserID && (selfDemote || selfDisable) {
		return apierr.Write(c, http.StatusConflict, apierr.SelfLockout)
	}

	ctx := c.Request().Context()
	tx, err := h.Store.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	u, err := h.Store.Update(ctx, tx, p.FleetID, id, patch)
	if errors.Is(err, ErrNotFound) {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	if err != nil {
		return err
	}

	now := h.Clock()
	if (in.Disabled != nil && *in.Disabled) || in.Password != nil {
		if err := auth.RevokeAllForUser(ctx, tx, id, now); err != nil {
			return err
		}
	}
	if err := audit.Write(ctx, tx, audit.Record{
		FleetID: p.FleetID, ActorID: p.UserID, Action: action, Target: target(id),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update user: %w", err)
	}
	return c.JSON(http.StatusOK, u)
}

// parseEmail trims, parses, and lowercases an address. Display names are refused.
func parseEmail(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Address != raw {
		return "", false
	}
	return NormalizeEmail(addr.Address), true
}

func target(id int64) string {
	return "user:" + strconv.FormatInt(id, 10)
}
