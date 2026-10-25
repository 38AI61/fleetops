package ingest

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/nats-io/nats.go/jetstream"

	"fleetops/internal/apierr"
	"fleetops/internal/registry"
)

// deviceKeyHeader carries the device key (GF-52). One value only.
const deviceKeyHeader = "X-Device-Key"

// Handler serves POST /ingest/position. It is outside the JWT group (GF-64):
// the device key is the only principal the route accepts.
type Handler struct {
	DB    *sql.DB
	Salt  []byte
	Clock Clock
	Gate  *Gate
	JS    jetstream.JetStream
}

// Register mounts the ingest route with no JWT middleware.
func (h *Handler) Register(e *echo.Echo) {
	e.POST("/ingest/position", h.position)
}

// position runs the GF-53 order: auth, decode and validate, stamp, publish, 204.
// It never writes to the database (GF-67).
func (h *Handler) position(c echo.Context) error {
	r := c.Request()

	keys := r.Header.Values(deviceKeyHeader)
	if len(keys) != 1 {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	dev, err := registry.AuthenticateDeviceKey(r.Context(), h.DB, h.Salt, keys[0])
	if err != nil {
		if isKeyRefusal(err) {
			return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
		}
		return err
	}

	req, err := DecodePosition(c.Response(), r)
	if err != nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}
	pos, err := Validate(req, h.Clock())
	if err != nil {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}

	msg, err := Stamp(dev, pos, h.Clock, uuid.NewV7)
	if err != nil {
		return err
	}
	if err := h.Gate.Publish(h.JS, msg); err != nil {
		return apierr.Write(c, http.StatusServiceUnavailable, apierr.Busy)
	}
	return c.NoContent(http.StatusNoContent)
}

// isKeyRefusal reports the GF-53 401 set. Every one maps to the same body, so a
// revoked device is indistinguishable from a wrong key.
func isKeyRefusal(err error) bool {
	return errors.Is(err, registry.ErrDeviceKeyMalformed) ||
		errors.Is(err, registry.ErrDeviceKeyInvalid) ||
		errors.Is(err, registry.ErrDeviceRevoked) ||
		errors.Is(err, registry.ErrDeviceNotFound)
}
