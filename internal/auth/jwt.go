package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"fleetops/internal/config"
)

// AccessTTL is the access-token lifetime (D-03: 15 min).
const AccessTTL = 15 * time.Minute

// ErrInvalidToken covers every reason Verify refuses a token.
var ErrInvalidToken = errors.New("invalid access token")

// Claims are the access-token claims (D-03). Subject is the user ID.
type Claims struct {
	FleetID int64  `json:"fleet_id"`
	Role    string `json:"role"`
	jwt.RegisteredClaims
}

// Issuer signs and verifies HS256 access tokens. The clock is injected so
// expiry is testable without sleeping (GF-20, GF-29).
type Issuer struct {
	secret []byte
	now    func() time.Time
	ttl    time.Duration
}

// NewIssuer refuses a secret shorter than 32 bytes (defense in depth beside config).
func NewIssuer(secret []byte, clock func() time.Time) (*Issuer, error) {
	if len(secret) < config.MinJWTSecretBytes {
		return nil, fmt.Errorf("jwt secret must be at least %d bytes", config.MinJWTSecretBytes)
	}
	if clock == nil {
		clock = time.Now
	}
	return &Issuer{secret: secret, now: clock, ttl: AccessTTL}, nil
}

// Issue returns a signed access token for the user.
func (i *Issuer) Issue(userID, fleetID int64, role string) (string, error) {
	now := i.now()
	claims := Claims{
		FleetID: fleetID,
		Role:    role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// Verify checks signature, HS256 only, required exp, and expiry against the
// injected clock. Any failure returns ErrInvalidToken.
func (i *Issuer) Verify(token string) (Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return i.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(i.now),
	)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}
