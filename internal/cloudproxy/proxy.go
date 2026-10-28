// Package cloudproxy is the single public port of the cloud container (GF-348).
// Render exposes one port, so the front proxy routes the api and the ws-hub
// behind it. The stdlib ReverseProxy relays WebSocket upgrades natively.
package cloudproxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// New routes /stream to the ws-hub and everything else to the api. /metrics is
// denied on this port: P22 keeps metrics on loopback only (GF-329, GF-330). A
// path under /stream, such as /stream/x, belongs to the api.
func New(apiURL, hubURL *url.URL) http.Handler {
	api := httputil.NewSingleHostReverseProxy(apiURL)
	hub := httputil.NewSingleHostReverseProxy(hubURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/metrics"):
			http.NotFound(w, r)
		case r.URL.Path == "/stream":
			hub.ServeHTTP(w, r)
		default:
			api.ServeHTTP(w, r)
		}
	})
}
