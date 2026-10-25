package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testSecret = []byte(strings.Repeat("k", 32))

// fakeClock is a settable clock for expiry tests.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestIssuer(t *testing.T, clock *fakeClock) *Issuer {
	t.Helper()
	iss, err := NewIssuer(testSecret, clock.now)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return iss
}

func TestNewIssuerRefusesShortSecret(t *testing.T) {
	if _, err := NewIssuer(make([]byte, 31), time.Now); err == nil {
		t.Fatal("31-byte secret: want error")
	}
	if _, err := NewIssuer(make([]byte, 32), time.Now); err != nil {
		t.Fatalf("32-byte secret: %v", err)
	}
}

func TestClaimsRoundTrip(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss := newTestIssuer(t, clock)

	tok, err := iss.Issue(42, 7, "dispatcher")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := iss.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.FleetID != 7 || got.Role != "dispatcher" || got.Subject != "42" {
		t.Fatalf("claims = %+v", got)
	}
}

func TestExpiryIsExactlyFifteenMinutes(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss := newTestIssuer(t, clock)

	tok, err := iss.Issue(1, 1, "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := iss.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if d := got.ExpiresAt.Sub(got.IssuedAt.Time); d != 15*time.Minute {
		t.Fatalf("exp - iat = %v, want 15m", d)
	}
	if want := clock.t.Add(15 * time.Minute); !got.ExpiresAt.Equal(want) {
		t.Fatalf("exp = %v, want %v", got.ExpiresAt.Time, want)
	}
}

func TestTokenValidUntilJustBeforeExpiry(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss := newTestIssuer(t, clock)
	tok, err := iss.Issue(1, 1, "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	clock.t = clock.t.Add(15*time.Minute - time.Second)
	if _, err := iss.Verify(tok); err != nil {
		t.Fatalf("1 s before exp: %v", err)
	}
}

func TestTokenRejectedOneSecondAfterExpiry(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss := newTestIssuer(t, clock)
	tok, err := iss.Issue(1, 1, "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	clock.t = clock.t.Add(15*time.Minute + time.Second)
	if _, err := iss.Verify(tok); err == nil {
		t.Fatal("1 s after exp: want rejection")
	}
}

func TestAlgNoneRejected(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss := newTestIssuer(t, clock)
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(clock)).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := iss.Verify(tok); err == nil {
		t.Fatal("alg=none: want rejection")
	}
}

func TestHS384AndHS512Rejected(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss := newTestIssuer(t, clock)
	for _, m := range []jwt.SigningMethod{jwt.SigningMethodHS384, jwt.SigningMethodHS512} {
		tok, err := jwt.NewWithClaims(m, validClaims(clock)).SignedString(testSecret)
		if err != nil {
			t.Fatalf("sign %s: %v", m.Alg(), err)
		}
		if _, err := iss.Verify(tok); err == nil {
			t.Fatalf("%s token: want rejection", m.Alg())
		}
	}
}

func TestTamperedFleetIDRejected(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss := newTestIssuer(t, clock)
	tok, err := iss.Issue(1, 1, "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	parts := strings.Split(tok, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	m["fleet_id"] = 2
	forged, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(forged)

	if _, err := iss.Verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered fleet_id: want rejection")
	}
}

func TestWrongSecretRejected(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss := newTestIssuer(t, clock)
	tok, err := iss.Issue(1, 1, "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	other, err := NewIssuer([]byte(strings.Repeat("z", 32)), clock.now)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	if _, err := other.Verify(tok); err == nil {
		t.Fatal("wrong secret: want rejection")
	}
}

func validClaims(clock *fakeClock) Claims {
	return Claims{
		FleetID: 1,
		Role:    "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			IssuedAt:  jwt.NewNumericDate(clock.t),
			ExpiresAt: jwt.NewNumericDate(clock.t.Add(AccessTTL)),
		},
	}
}
