package users

import (
	"context"
	"errors"

	"fleetops/internal/auth"
)

// Store satisfies auth.Accounts, which login and refresh use. The lookups
// translate ErrNotFound into found=false.
var _ auth.Accounts = (*Store)(nil)

// AccountByEmail is the login lookup (D-01: login by email only).
func (s *Store) AccountByEmail(ctx context.Context, email string) (auth.Account, bool, error) {
	u, err := s.GetByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return auth.Account{}, false, nil
	}
	if err != nil {
		return auth.Account{}, false, err
	}
	return toAccount(u), true, nil
}

// AccountByID is the refresh lookup, scoped to the fleet from the token row.
func (s *Store) AccountByID(ctx context.Context, fleetID, userID int64) (auth.Account, bool, error) {
	u, err := s.Get(ctx, fleetID, userID)
	if errors.Is(err, ErrNotFound) {
		return auth.Account{}, false, nil
	}
	if err != nil {
		return auth.Account{}, false, err
	}
	return toAccount(u), true, nil
}

func toAccount(u User) auth.Account {
	return auth.Account{
		UserID:       u.ID,
		FleetID:      u.FleetID,
		Role:         u.Role,
		PasswordHash: u.PasswordHash,
		Disabled:     u.Disabled(),
	}
}
