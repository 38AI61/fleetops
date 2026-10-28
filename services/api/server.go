// Package main is the FleetOps API. This file wires the routes; main.go owns
// process startup. Migrations are not run here (D-13).
package main

import (
	"database/sql"
	"net/http"
	"regexp"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/nats-io/nats.go/jetstream"

	"fleetops/internal/alerts"
	"fleetops/internal/auth"
	"fleetops/internal/dispatch"
	"fleetops/internal/geofence"
	"fleetops/internal/ingest"
	"fleetops/internal/jobs"
	"fleetops/internal/mapview"
	"fleetops/internal/observability"
	"fleetops/internal/proofs"
	"fleetops/internal/registry"
	"fleetops/internal/reports"
	"fleetops/internal/users"
)

// maxBodyBytes bounds every request body. Passwords are capped at 256 bytes
// by policy, so this only blocks oversized payloads before argon2 runs.
const maxBodyBytes = "64K"

// deliverPath is the one route that takes a multipart photo (GF-199). It has its
// own 8M BodyLimit, so the global 64K limit skips it.
var deliverPath = regexp.MustCompile(`^/driver/jobs/[0-9]+/deliver$`)

func skipDeliverBody(c echo.Context) bool {
	return c.Request().Method == http.MethodPost && deliverPath.MatchString(c.Request().URL.Path)
}

// Deps are the collaborators NewServer needs. Tests inject a fake Clock.
// DeviceKeySalt is the HMAC key for device keys and license hashes (GF-30).
// NATS and Gate back POST /ingest/position (P4).
// PresenceTTL is the map offline threshold (FLEETOPS_PRESENCE_TTL, GF-132).
// Events publishes jobs.events after each applied job transition (GF-179).
// Proofs stores delivery photos (P12, GF-201). Nil leaves deliver answering 503.
type Deps struct {
	DB            *sql.DB
	Issuer        *auth.Issuer
	Clock         func() time.Time
	Throttle      *auth.Throttle
	DeviceKeySalt []byte
	NATS          jetstream.JetStream
	Gate          *ingest.Gate
	PresenceTTL   time.Duration
	Events        jobs.Publisher
	Proofs        *proofs.Store
}

// NewServer returns the API with every P2 through P12 route mounted.
func NewServer(d Deps) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{Skipper: skipDeliverBody, Limit: maxBodyBytes}))

	// /metrics is unauthenticated and sits outside every JWT group (GF-329, GF-330).
	// The api owns no fleetops series, so its registry holds only the Go and process collectors.
	e.GET("/metrics", echo.WrapHandler(observability.Handler(observability.NewRegistry())))

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

	jh := &jobs.Handler{Svc: &jobs.Store{DB: d.DB}, Events: d.Events, Now: d.Clock}
	// Assign the interface only when the store exists: a nil *proofs.Store in an
	// interface is not nil (GF-201).
	if d.Proofs != nil {
		jh.Proofs = d.Proofs
	}
	jh.Register(e, auth.RequireAuth(d.Issuer))

	mh := &mapview.Handler{DB: d.DB, Clock: d.Clock, PresenceTTL: d.PresenceTTL, Jobs: &jobs.MapLookup{DB: d.DB}}
	mh.Register(e, auth.RequireAuth(d.Issuer))

	dh := &dispatch.Handler{Store: &dispatch.Store{DB: d.DB}}
	dh.Register(e, auth.RequireAuth(d.Issuer))

	gh := &geofence.Handler{Store: &geofence.Store{DB: d.DB}}
	gh.Register(e, auth.RequireAuth(d.Issuer))

	alh := &alerts.Handler{Store: &alerts.Store{DB: d.DB}, Clock: d.Clock}
	alh.Register(e, auth.RequireAuth(d.Issuer))

	reh := &reports.Handler{Store: &reports.Store{DB: d.DB}, Clock: d.Clock}
	reh.Register(e, auth.RequireAuth(d.Issuer))

	// Device route: no JWT group (GF-64). The device key is checked inside the handler.
	ih := &ingest.Handler{DB: d.DB, Salt: d.DeviceKeySalt, Clock: d.Clock, Gate: d.Gate, JS: d.NATS}
	ih.Register(e)

	return e
}
