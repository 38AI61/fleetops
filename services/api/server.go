// Package main is the FleetOps API. This file wires the routes; main.go owns
// process startup. Migrations are not run here (D-13).
package main

import (
	"database/sql"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"fleetops/internal/auth"
	"fleetops/internal/registry"
	"fleetops/internal/users"
)

// maxBodyBytes bounds every request body. Passwords are capped at 256 bytes
// by policy, so this only blocks oversized payloads before argon2 runs.
const maxBodyBytes = "64K"

// Deps are the collaborators NewServer needs. Tests inject a fake Clock.
// DeviceKeySalt is the HMAC key for device keys and license hashes (GF-30).
type Deps struct {
	DB            *sql.DB
	Issuer        *auth.Issuer
	Clock         func() time.Time
	Throttle      *auth.Throttle
	DeviceKeySalt []byte
}

// NewServer returns the API with every P2 and P3 route mounted.
func NewServer(d Deps) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit(maxBodyBytes))

	store := users.NewStore(d.DB)
	ah := &auth.Handlers{
		DB:       d.DB,
		Issuer:   d.Issuer,
		Throttle: d.Throttle,
		Accounts: store,
		Clock:    d.Clock,
	}
	e.POST("/auth/login", ah.Login)
	e.POST("/auth/refresh", ah.Refresh)
	e.POST("/auth/logout", ah.Logout)

	uh := &users.Handler{DB: d.DB, Store: store, Clock: d.Clock}
	admin := []echo.MiddlewareFunc{auth.RequireAuth(d.Issuer), auth.RequireRole("admin")}
	e.GET("/users", uh.List, admin...)
	e.POST("/users", uh.Create, admin...)
	e.PATCH("/users/:id", uh.Update, admin...)

	rh := &registry.Handler{Svc: &registry.Service{DB: d.DB, Salt: d.DeviceKeySalt, Clock: d.Clock}}
	rh.Register(e, auth.RequireAuth(d.Issuer))

	return e
}
