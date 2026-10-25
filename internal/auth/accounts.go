package auth

import "context"

// Account is what login and refresh need from the users table. It is defined
// here so auth does not import users; users.Store satisfies Accounts.
type Account struct {
	UserID       int64
	FleetID      int64
	Role         string
	PasswordHash string
	Disabled     bool
}

// Accounts looks up users for the login and refresh handlers. found is false
// when no such user exists.
type Accounts interface {
	AccountByEmail(ctx context.Context, email string) (acct Account, found bool, err error)
	AccountByID(ctx context.Context, fleetID, userID int64) (acct Account, found bool, err error)
}
