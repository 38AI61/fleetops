// Package registry holds the fleet registry: vehicles, drivers, devices,
// vehicle pairings, and the device-key checks P4 ingest will call (D-27–D-31).
// Every query is scoped by fleet_id (GF-27).
package registry

import "errors"

// Sentinel errors. The HTTP layer maps each to one apierr code (GF-42).
var (
	ErrNotFound           = errors.New("registry: not found")
	ErrValidation         = errors.New("registry: validation failed")
	ErrConflict           = errors.New("registry: conflict")
	ErrVehicleNotActive   = errors.New("registry: vehicle not active")
	ErrDeviceRevoked      = errors.New("registry: device revoked")
	ErrDeviceInUse        = errors.New("registry: device in use")
	ErrDeviceNotFound     = errors.New("registry: device not found")
	ErrDeviceKeyMalformed = errors.New("registry: device key malformed")
	ErrDeviceKeyInvalid   = errors.New("registry: device key invalid")
)
