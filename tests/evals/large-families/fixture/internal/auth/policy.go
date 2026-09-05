package auth

import "strings"

// PasswordPolicy validates a candidate password.
type PasswordPolicy struct {
    MinLength int
}

// DefaultPolicy is the demo password policy.
var DefaultPolicy = PasswordPolicy{MinLength: 8}

// Validate reports whether the candidate password satisfies the policy.
func (p PasswordPolicy) Validate(candidate string) error {
    if len(candidate) < p.MinLength {
        return ErrPasswordTooShort
    }
    return nil
}

// Normalize lowercases and trims a user identifier.
func Normalize(userID string) string {
    return strings.ToLower(strings.TrimSpace(userID))
}
