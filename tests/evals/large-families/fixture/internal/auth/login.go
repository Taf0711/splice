package auth

import (
    "errors"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

// Login authenticates a user against the password service and reports
// success. It does not create sessions; the HTTP layer owns those.
func (p *PasswordService) Login(userID, password string) error {
    recorded, ok := p.passwords[userID]
    if !ok || recorded != password {
        return ErrInvalidCredentials
    }
    return nil
}

// HasPassword reports whether the user has a password record.
func (p *PasswordService) HasPassword(userID string) bool {
    _, ok := p.passwords[userID]
    return ok
}
