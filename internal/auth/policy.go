package auth

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Password and username policy for register / change-password.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 128
	MinUsernameLen = 3
	MaxUsernameLen = 64
)

// usernameRE: letters, digits, underscore, hyphen, dot (no spaces).
var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateUsername checks length and character set.
func ValidateUsername(username string) error {
	username = strings.TrimSpace(username)
	n := utf8.RuneCountInString(username)
	if n < MinUsernameLen {
		return fmt.Errorf("username too short (min %d)", MinUsernameLen)
	}
	if n > MaxUsernameLen {
		return fmt.Errorf("username too long (max %d)", MaxUsernameLen)
	}
	if !usernameRE.MatchString(username) {
		return fmt.Errorf("username must start with alphanumeric and contain only [a-zA-Z0-9._-]")
	}
	return nil
}

// ValidatePassword checks length bounds for new passwords.
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLen {
		return fmt.Errorf("password too short (min %d)", MinPasswordLen)
	}
	if n > MaxPasswordLen {
		return fmt.Errorf("password too long (max %d)", MaxPasswordLen)
	}
	return nil
}
