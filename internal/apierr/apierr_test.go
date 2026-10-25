package apierr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// TestCodeStatusTable pins each code to the HTTP status the GF-28 and GF-42
// tables assign. Write sends whatever status the caller passes, so the table
// is the contract the handlers must follow.
func TestCodeStatusTable(t *testing.T) {
	cases := []struct {
		code   string
		status int
	}{
		{InvalidCredentials, http.StatusUnauthorized},
		{Unauthorized, http.StatusUnauthorized},
		{Forbidden, http.StatusForbidden},
		{NotFound, http.StatusNotFound},
		{Conflict, http.StatusConflict},
		{SelfLockout, http.StatusConflict},
		{ValidationFailed, http.StatusBadRequest},
		{WeakPassword, http.StatusBadRequest},
		{RateLimited, http.StatusTooManyRequests},
		{VehicleNotActive, http.StatusConflict},
		{DeviceRevoked, http.StatusConflict},
		{DeviceInUse, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)
			if err := Write(c, tc.status, tc.code); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if body["error"] != tc.code {
				t.Fatalf("envelope = %v, want error=%q", body, tc.code)
			}
		})
	}
}
