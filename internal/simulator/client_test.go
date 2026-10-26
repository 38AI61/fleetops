package simulator

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "fo_dev_7_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// TestSendClassification: 204 is accepted, other 4xx is rejected, and 5xx or
// a transport error is failed. No case panics or leaks the key (GF-146).
func TestSendClassification(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    Outcome
		code    string
	}{
		{"204", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }, Accepted, ""},
		{"400", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"validation_failed"}`)
		}, Rejected, "validation_failed"},
		{"401", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
		}, Rejected, "unauthorized"},
		{"503", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"busy"}`)
		}, Failed, "busy"},
		{"closed connection", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}, Failed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ingest/position" || r.Header.Get("X-Device-Key") != testKey {
					w.WriteHeader(http.StatusTeapot)
					return
				}
				tc.handler(w, r)
			}))
			defer srv.Close()

			s := NewHTTPSender(srv.URL+"/", 2)
			got, err := s.Send(context.Background(), testKey, PositionBody{Lat: 52.5, Lng: 13.4, Speed: 40, Heading: 90, Accuracy: 5, TS: "2026-10-26T10:00:00.000Z"})
			if got != tc.want {
				t.Fatalf("outcome = %d (%v), want %d", got, err, tc.want)
			}
			if tc.want == Accepted && err != nil {
				t.Fatalf("accepted with error %v", err)
			}
			if tc.want != Accepted && err == nil {
				t.Fatal("non-accepted outcome without an error")
			}
			if err != nil && strings.Contains(err.Error(), testKey) {
				t.Fatalf("error leaks the key: %v", err)
			}
			if err != nil && tc.code != "" && !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error %q lacks response code %q", err, tc.code)
			}
		})
	}
}

// TestSendUnreachableIsFailed: a closed listener is a transport error, not a panic.
func TestSendUnreachableIsFailed(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	s := NewHTTPSender(url, 1)
	got, err := s.Send(context.Background(), testKey, PositionBody{TS: time.Now().UTC().Format(timeLayout)})
	if got != Failed || err == nil {
		t.Fatalf("outcome = %d, err %v; want Failed with error", got, err)
	}
}
