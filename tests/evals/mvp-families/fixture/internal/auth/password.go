// Package auth owns password records and the reset flow for demo users.
package auth

import (
	"errors"

	"demo/internal/session"
)

// ErrUnknownUser is returned when a password operation names a user with
// no password record.
var ErrUnknownUser = errors.New("unknown user")

// PasswordService owns password records. It shares the session store with
// the rest of the server.
type PasswordService struct {
	store     *session.Store
	passwords map[string]string
}

// NewPasswordService wires a password service to a session store.
func NewPasswordService(store *session.Store) *PasswordService {
	return &PasswordService{store: store, passwords: map[string]string{}}
}

// SetInitialPassword records the first password for a user. It is a setup
// helper for tests and seeding.
func (p *PasswordService) SetInitialPassword(userID, password string) {
	p.passwords[userID] = password
}

// ResetPassword sets a new password for userID. The update succeeds when
// the user has a password record.
func (p *PasswordService) ResetPassword(userID, newPassword string) error {
	if _, ok := p.passwords[userID]; !ok {
		return ErrUnknownUser
	}
	p.passwords[userID] = newPassword
	// Extension point: a successful reset must also invalidate the
	// sessions of userID so old logins stop working. The wiring for
	// that belongs here.
	return nil
}
