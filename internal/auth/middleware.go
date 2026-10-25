package auth

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
)

const principalKey = "fleetops.principal"

// Principal is the authenticated caller, set by RequireAuth. Handlers must
// still scope every store query by FleetID; the claim alone is not trusted for
// data access beyond identifying the caller.
type Principal struct {
	UserID  int64
	FleetID int64
	Role    string
}

// PrincipalFrom returns the principal RequireAuth stored in the context.
func PrincipalFrom(c echo.Context) (Principal, bool) {
	p, ok := c.Get(principalKey).(Principal)
	return p, ok
}

// RequireAuth verifies the bearer access token (HS256, D-03) and stores the
// principal. Any failure is 401 unauthorized.
func RequireAuth(issuer *Issuer) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Request().Header.Get(echo.HeaderAuthorization)
			tok, found := strings.CutPrefix(h, "Bearer ")
			if !found || tok == "" {
				return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
			}
			claims, err := issuer.Verify(tok)
			if err != nil {
				return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
			}
			userID, err := parseSubject(claims.Subject)
			if err != nil {
				return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
			}
			c.Set(principalKey, Principal{UserID: userID, FleetID: claims.FleetID, Role: claims.Role})
			return next(c)
		}
	}
}

// RequireRole allows only the listed roles. It runs after RequireAuth and
// before body parsing (GF-28), so a wrong role gets 403 regardless of body.
func RequireRole(roles ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			p, ok := PrincipalFrom(c)
			if !ok {
				return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
			}
			for _, r := range roles {
				if p.Role == r {
					return next(c)
				}
			}
			return apierr.Write(c, http.StatusForbidden, apierr.Forbidden)
		}
	}
}

func parseSubject(sub string) (int64, error) {
	return strconv.ParseInt(sub, 10, 64)
}
