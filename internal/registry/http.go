package registry

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
	"fleetops/internal/auth"
)

// Role names used by the registry route table (README §8, R-6).
const (
	roleAdmin      = "admin"
	roleManager    = "manager"
	roleDispatcher = "dispatcher"
	roleDriver     = "driver"
)

// Handler serves the P3 registry routes (GF-37, GF-38, GF-47). Every handler
// takes its fleet from the principal, never from the request (GF-27).
type Handler struct {
	Svc *Service
}

// Register mounts every registry route. authn is auth.RequireAuth for the
// deployment's issuer. Each route then runs its role check before the handler
// reads a body (GF-28), so a wrong role gets 403 whatever the body holds.
func (h *Handler) Register(e *echo.Echo, authn echo.MiddlewareFunc) {
	ops := auth.RequireRole(roleAdmin, roleManager, roleDispatcher)
	managers := auth.RequireRole(roleAdmin, roleManager)
	admins := auth.RequireRole(roleAdmin)
	everyone := auth.RequireRole(roleAdmin, roleManager, roleDispatcher, roleDriver)

	e.GET("/vehicles", h.listVehicles, authn, ops)
	e.GET("/vehicles/:id", h.getVehicle, authn, ops)
	e.POST("/vehicles", h.createVehicle, authn, ops)
	e.PATCH("/vehicles/:id", h.updateVehicle, authn, managers)

	e.GET("/drivers", h.listDrivers, authn, everyone)
	e.GET("/drivers/:id", h.getDriver, authn, everyone)
	e.POST("/drivers", h.createDriver, authn, ops)
	e.PATCH("/drivers/:id", h.updateDriver, authn, managers)

	e.GET("/vehicles/:id/pairings", h.listPairings, authn, ops)
	e.POST("/vehicles/:id/pairings", h.setPairing, authn, managers)

	e.POST("/devices", h.issueDevice, authn, admins)
	e.POST("/devices/:id/rotate", h.rotateDevice, authn, admins)
	e.POST("/devices/:id/revoke", h.revokeDevice, authn, admins)
}

type vehicleBody struct {
	Plate  string `json:"plate"`
	Model  string `json:"model"`
	Status string `json:"status"`
}

type vehiclePatchBody struct {
	Plate  *string `json:"plate"`
	Model  *string `json:"model"`
	Status *string `json:"status"`
}

type driverBody struct {
	Name       string `json:"name"`
	LicenseNo  string `json:"license_no"`
	Phone      string `json:"phone"`
	ShiftStart string `json:"shift_start"`
	ShiftEnd   string `json:"shift_end"`
}

type driverPatchBody struct {
	Name       *string `json:"name"`
	LicenseNo  *string `json:"license_no"`
	Phone      *string `json:"phone"`
	ShiftStart *string `json:"shift_start"`
	ShiftEnd   *string `json:"shift_end"`
}

// nullableID records whether a JSON field was present and what it held. An
// absent field and a null are different: an absent device_id must not unpair.
type nullableID struct {
	Present bool
	ID      *int64
}

func (n *nullableID) UnmarshalJSON(b []byte) error {
	n.Present = true
	if string(b) == "null" {
		n.ID = nil
		return nil
	}
	var v int64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.ID = &v
	return nil
}

type pairingBody struct {
	DeviceID nullableID `json:"device_id"`
	DriverID nullableID `json:"driver_id"`
}

type deviceBody struct {
	Kind string `json:"kind"`
}

func (h *Handler) listVehicles(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	list, err := h.Svc.ListVehicles(c.Request().Context(), p.FleetID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, list)
}

func (h *Handler) getVehicle(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	v, err := h.Svc.GetVehicle(c.Request().Context(), p.FleetID, id)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, v)
}

func (h *Handler) createVehicle(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in vehicleBody
	if err := bindStrict(c, &in); err != nil {
		return writeErr(c, err)
	}
	v, err := h.Svc.CreateVehicle(c.Request().Context(), actorOf(p), VehicleInput(in))
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusCreated, v)
}

func (h *Handler) updateVehicle(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in vehiclePatchBody
	if err := bindStrict(c, &in); err != nil {
		return writeErr(c, err)
	}
	if in.Plate == nil && in.Model == nil && in.Status == nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	v, err := h.Svc.UpdateVehicle(c.Request().Context(), actorOf(p), id, VehiclePatch(in))
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, v)
}

// listDrivers returns every driver to a manager or dispatcher. A driver sees
// only its linked row, or nothing when it has no link (GF-49).
func (h *Handler) listDrivers(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	ctx := c.Request().Context()
	var only *int64
	if p.Role == roleDriver {
		linked, err := h.Svc.LinkedDriverID(ctx, p.FleetID, p.UserID)
		if err != nil {
			return err
		}
		if linked == nil {
			return c.JSON(http.StatusOK, []Driver{})
		}
		only = linked
	}
	list, err := h.Svc.ListDrivers(ctx, p.FleetID, only)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, list)
}

// getDriver answers a driver's request for another driver's row with 404, not
// 403, so existence is not revealed (GF-49).
func (h *Handler) getDriver(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	ctx := c.Request().Context()
	if p.Role == roleDriver {
		linked, err := h.Svc.LinkedDriverID(ctx, p.FleetID, p.UserID)
		if err != nil {
			return err
		}
		if linked == nil || *linked != id {
			return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
		}
	}
	d, err := h.Svc.GetDriver(ctx, p.FleetID, id)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (h *Handler) createDriver(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in driverBody
	if err := bindStrict(c, &in); err != nil {
		return writeErr(c, err)
	}
	d, err := h.Svc.CreateDriver(c.Request().Context(), actorOf(p), DriverInput(in))
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusCreated, d)
}

func (h *Handler) updateDriver(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in driverPatchBody
	if err := bindStrict(c, &in); err != nil {
		return writeErr(c, err)
	}
	if in.Name == nil && in.LicenseNo == nil && in.Phone == nil && in.ShiftStart == nil && in.ShiftEnd == nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	d, err := h.Svc.UpdateDriver(c.Request().Context(), actorOf(p), id, DriverPatch(in))
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (h *Handler) listPairings(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	list, err := h.Svc.ListPairings(c.Request().Context(), p.FleetID, id)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, list)
}

// setPairing pairs, swaps, or unpairs (GF-38). A new row is 201; an unpair or a
// no-op is 200 with the row it reports, or null when nothing was open.
func (h *Handler) setPairing(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in pairingBody
	if err := bindStrict(c, &in); err != nil {
		return writeErr(c, err)
	}
	if !in.DeviceID.Present {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	row, outcome, err := h.Svc.SetPairing(c.Request().Context(), actorOf(p), id,
		PairingInput{DeviceID: in.DeviceID.ID, DriverID: in.DriverID.ID})
	if err != nil {
		return writeErr(c, err)
	}
	if outcome == Created && in.DeviceID.ID != nil {
		return c.JSON(http.StatusCreated, row)
	}
	return c.JSON(http.StatusOK, row)
}

func (h *Handler) issueDevice(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	var in deviceBody
	if err := bindStrict(c, &in); err != nil {
		return writeErr(c, err)
	}
	d, err := h.Svc.IssueDevice(c.Request().Context(), actorOf(p), in.Kind)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusCreated, d)
}

func (h *Handler) rotateDevice(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	d, err := h.Svc.RotateDevice(c.Request().Context(), actorOf(p), id)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (h *Handler) revokeDevice(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	d, err := h.Svc.RevokeDevice(c.Request().Context(), actorOf(p), id)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

// bindStrict decodes one JSON object and refuses unknown fields (GF-51). Any
// decode failure is ErrValidation, which writeErr maps to 400.
func bindStrict(c echo.Context, v any) error {
	dec := json.NewDecoder(c.Request().Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return ErrValidation
	}
	return nil
}

// idParam reads :id as a positive integer. A malformed id is treated as absent.
func idParam(c echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil
}

func actorOf(p auth.Principal) Actor {
	return Actor{UserID: p.UserID, FleetID: p.FleetID}
}

// writeErr maps registry sentinels to the GF-28 and GF-42 envelope. Anything
// else is returned so Echo's error handler answers 500.
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
	case errors.Is(err, ErrDeviceRevoked):
		return apierr.Write(c, http.StatusConflict, apierr.DeviceRevoked)
	case errors.Is(err, ErrDeviceInUse):
		return apierr.Write(c, http.StatusConflict, apierr.DeviceInUse)
	}
	return err
}
