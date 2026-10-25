// Package apierr writes the GF-28 error envelope: {"error":"<code>"}.
package apierr

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Codes returned to clients (GF-28).
const (
	InvalidCredentials = "invalid_credentials" //nolint:gosec // error code string, not a credential
	Unauthorized       = "unauthorized"
	Forbidden          = "forbidden"
	NotFound           = "not_found"
	Conflict           = "conflict"
	SelfLockout        = "self_lockout"
	ValidationFailed   = "validation_failed"
	WeakPassword       = "weak_password"
	RateLimited        = "rate_limited"

	// Registry codes (GF-42).
	VehicleNotActive = "vehicle_not_active"
	DeviceRevoked    = "device_revoked"
	DeviceInUse      = "device_in_use"
)

// Write sends the envelope with the given status.
func Write(c echo.Context, status int, code string) error {
	return c.JSON(status, map[string]string{"error": code})
}

// Internal logs nothing and returns a bare 500 so internals never leak.
func Internal(c echo.Context) error {
	return c.NoContent(http.StatusInternalServerError)
}
