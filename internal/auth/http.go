package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"fleetops/internal/apierr"
)

// RefreshCookieName is the refresh cookie (GF-26). Path is /auth.
const RefreshCookieName = "fleetops_refresh"

// accessExpiresIn is the access-token lifetime in seconds for the response body.
var accessExpiresIn = int(AccessTTL / time.Second)

// Handlers serves /auth/login, /auth/refresh, and /auth/logout.
type Handlers struct {
	DB       *sql.DB
	Issuer   *Issuer
	Throttle *Throttle
	Accounts Accounts
	Clock    func() time.Time
}

// dummyVerify spends the same argon2id time on an unknown email as on a real one.
func dummyVerify(pw string) {
	_, _ = VerifyPassword(dummyHash, pw)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// Login implements POST /auth/login.
func (h *Handlers) Login(c echo.Context) error {
	var in loginRequest
	if err := c.Bind(&in); err != nil || in.Email == "" || in.Password == "" {
		return apierr.Write(c, http.StatusBadRequest, apierr.ValidationFailed)
	}

	ip := clientIP(c.Request().RemoteAddr)
	if retry, ok := h.Throttle.Allow(in.Email, ip); !ok {
		c.Response().Header().Set(echo.HeaderRetryAfter, strconv.Itoa(retryAfterSeconds(retry)))
		return apierr.Write(c, http.StatusTooManyRequests, apierr.RateLimited)
	}

	ctx := c.Request().Context()
	acct, found, err := h.Accounts.AccountByEmail(ctx, in.Email)
	if err != nil {
		return fmt.Errorf("login lookup: %w", err)
	}
	if !found {
		dummyVerify(in.Password)
		h.Throttle.RecordFailure(in.Email, ip)
		return apierr.Write(c, http.StatusUnauthorized, apierr.InvalidCredentials)
	}

	match, err := VerifyPassword(acct.PasswordHash, in.Password)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !match || acct.Disabled {
		h.Throttle.RecordFailure(in.Email, ip)
		return apierr.Write(c, http.StatusUnauthorized, apierr.InvalidCredentials)
	}

	return h.issueSession(c, acct)
}

// Refresh implements POST /auth/refresh. Any refusal clears the cookie.
func (h *Handlers) Refresh(c echo.Context) error {
	ctx := c.Request().Context()
	now := h.Clock()

	ck, err := c.Cookie(RefreshCookieName)
	if err != nil || ck.Value == "" {
		clearRefreshCookie(c)
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}

	userID, fleetID, newRaw, err := Rotate(ctx, h.DB, ck.Value, now)
	if errors.Is(err, ErrInvalid) {
		clearRefreshCookie(c)
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	if err != nil {
		return fmt.Errorf("rotate: %w", err)
	}

	acct, found, err := h.Accounts.AccountByID(ctx, fleetID, userID)
	if err != nil {
		return fmt.Errorf("refresh lookup: %w", err)
	}
	if !found || acct.Disabled {
		clearRefreshCookie(c)
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}

	access, err := h.Issuer.Issue(acct.UserID, acct.FleetID, acct.Role)
	if err != nil {
		return fmt.Errorf("issue access token: %w", err)
	}
	setRefreshCookie(c, newRaw)
	return c.JSON(http.StatusOK, tokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: accessExpiresIn,
	})
}

// Logout implements POST /auth/logout. It is idempotent: no cookie or an
// unknown cookie is 204.
func (h *Handlers) Logout(c echo.Context) error {
	ck, err := c.Cookie(RefreshCookieName)
	if err != nil || ck.Value == "" {
		return c.NoContent(http.StatusNoContent)
	}
	if err := RevokeFamily(c.Request().Context(), h.DB, ck.Value, h.Clock()); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	clearRefreshCookie(c)
	return c.NoContent(http.StatusNoContent)
}

// issueSession creates a new refresh family and access token for a verified login.
func (h *Handlers) issueSession(c echo.Context, acct Account) error {
	ctx := c.Request().Context()
	now := h.Clock()

	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin login: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	raw, err := Issue(ctx, tx, acct.UserID, acct.FleetID, now)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit login: %w", err)
	}

	access, err := h.Issuer.Issue(acct.UserID, acct.FleetID, acct.Role)
	if err != nil {
		return fmt.Errorf("issue access token: %w", err)
	}
	setRefreshCookie(c, raw)
	return c.JSON(http.StatusOK, tokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: accessExpiresIn,
	})
}

func setRefreshCookie(c echo.Context, raw string) {
	c.SetCookie(&http.Cookie{
		Name:     RefreshCookieName,
		Value:    raw,
		Path:     "/auth",
		MaxAge:   int(RefreshTTL / time.Second),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearRefreshCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// clientIP takes the host from RemoteAddr only; X-Forwarded-For is ignored (GF-25).
func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// retryAfterSeconds rounds up so a client never retries early.
func retryAfterSeconds(d time.Duration) int {
	return int(math.Ceil(d.Seconds()))
}
