package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = bcrypt.DefaultCost

// RoleAdmin and RoleUser are RBAC roles.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User is a minimal account.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// DefaultTokenTTL is used when TokenTTL is not set.
const DefaultTokenTTL = 24 * time.Hour

// Service provides register/login and token checks.
type Service struct {
	secret   []byte
	store    store.Store
	tokenTTL time.Duration
}

type tokenPayload struct {
	UserID       string `json:"uid"`
	Username     string `json:"un"`
	Role         string `json:"role"`
	Exp          int64  `json:"exp"`
	JTI          string `json:"jti,omitempty"`
	TokenVersion int    `json:"tv,omitempty"`
}

// New creates an auth service backed by store.
func New(secret string, st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{
		secret:   []byte(secret),
		store:    st,
		tokenTTL: DefaultTokenTTL,
	}
}

// SetTokenTTL overrides issued token lifetime (zero → DefaultTokenTTL).
func (s *Service) SetTokenTTL(ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	s.tokenTTL = ttl
}

func (s *Service) tokenTTLOrDefault() time.Duration {
	if s.tokenTTL <= 0 {
		return DefaultTokenTTL
	}
	return s.tokenTTL
}

// HashPassword returns a bcrypt hash of password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

func isBcryptHash(s string) bool {
	return strings.HasPrefix(s, "$2a$") ||
		strings.HasPrefix(s, "$2b$") ||
		strings.HasPrefix(s, "$2y$")
}

// Register creates a user with bcrypt password. First user becomes admin.
func (s *Service) Register(username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	if _, err := s.store.GetUserByUsername(username); err == nil {
		return nil, fmt.Errorf("user exists")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	role := RoleUser
	if n, err := s.store.CountUsers(); err == nil && n == 0 {
		role = RoleAdmin
	}
	u := &store.User{
		ID:       uuid.NewString(),
		Username: username,
		Password: hash,
		Role:     role,
	}
	if err := s.store.CreateUser(u); err != nil {
		return nil, err
	}
	return &User{ID: u.ID, Username: u.Username, Role: role}, nil
}

// Login verifies credentials and returns a token.
func (s *Service) Login(username, password string) (string, *User, error) {
	rec, err := s.store.GetUserByUsername(username)
	if err != nil {
		return "", nil, fmt.Errorf("invalid credentials")
	}
	role := rec.Role
	if role == "" {
		role = RoleUser
	}

	if isBcryptHash(rec.Password) {
		if err := bcrypt.CompareHashAndPassword([]byte(rec.Password), []byte(password)); err != nil {
			return "", nil, fmt.Errorf("invalid credentials")
		}
	} else {
		if rec.Password != password {
			return "", nil, fmt.Errorf("invalid credentials")
		}
		hash, err := HashPassword(password)
		if err != nil {
			return "", nil, err
		}
		if err := s.store.UpdateUserPassword(rec.ID, hash); err != nil {
			return "", nil, fmt.Errorf("upgrade password hash: %w", err)
		}
	}

	tok, err := s.issue(rec.ID, rec.Username, role, rec.TokenVersion, s.tokenTTLOrDefault())
	if err != nil {
		return "", nil, err
	}
	return tok, &User{ID: rec.ID, Username: rec.Username, Role: role}, nil
}

// Parse validates a bearer token and returns user id, name, role.
func (s *Service) Parse(token string) (userID, username, role string, err error) {
	p, err := s.parsePayload(token)
	if err != nil {
		return "", "", "", err
	}
	role = p.Role
	if role == "" {
		role = RoleUser
	}
	return p.UserID, p.Username, role, nil
}

func (s *Service) parsePayload(token string) (*tokenPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("bad token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("bad token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("bad token")
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, fmt.Errorf("bad signature")
	}
	var p tokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if time.Now().Unix() > p.Exp {
		return nil, fmt.Errorf("token expired")
	}
	if p.JTI != "" {
		rev, err := s.store.IsJTIRevoked(p.JTI)
		if err != nil {
			return nil, fmt.Errorf("token check failed")
		}
		if rev {
			return nil, fmt.Errorf("token revoked")
		}
	}
	// Version check: invalidate after password change / admin revoke-all.
	if rec, err := s.store.GetUserByID(p.UserID); err == nil {
		if p.TokenVersion != rec.TokenVersion {
			return nil, fmt.Errorf("token revoked")
		}
	}
	return &p, nil
}

// Logout revokes the current token (jti denylist until natural expiry).
func (s *Service) Logout(token string) error {
	p, err := s.parsePayload(token)
	if err != nil {
		return err
	}
	if p.JTI == "" {
		// Legacy token without jti: bump user version to force re-login.
		_, err := s.store.BumpTokenVersion(p.UserID)
		return err
	}
	exp := time.Unix(p.Exp, 0).UTC()
	return s.store.RevokeJTI(p.JTI, exp)
}

// RevokeAllSessions bumps token_version so all issued tokens for the user fail Parse.
func (s *Service) RevokeAllSessions(userID string) (int, error) {
	return s.store.BumpTokenVersion(userID)
}

// SetRole updates a user's role (caller must enforce admin).
// Refuses to demote the last remaining admin.
func (s *Service) SetRole(userID, role string) error {
	role = strings.TrimSpace(role)
	if role != RoleAdmin && role != RoleUser {
		return fmt.Errorf("role must be admin or user")
	}
	cur, err := s.store.GetUserByID(userID)
	if err != nil {
		return fmt.Errorf("user not found")
	}
	curRole := cur.Role
	if curRole == "" {
		curRole = RoleUser
	}
	if curRole == RoleAdmin && role == RoleUser {
		n, err := s.countAdmins()
		if err != nil {
			return err
		}
		if n <= 1 {
			return fmt.Errorf("cannot demote the last admin")
		}
	}
	return s.store.UpdateUserRole(userID, role)
}

func (s *Service) countAdmins() (int, error) {
	list, err := s.store.ListUsers()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range list {
		if u.Role == RoleAdmin {
			n++
		}
	}
	return n, nil
}

// GetUser returns public user info by id.
func (s *Service) GetUser(userID string) (*User, error) {
	u, err := s.store.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	role := u.Role
	if role == "" {
		role = RoleUser
	}
	return &User{ID: u.ID, Username: u.Username, Role: role}, nil
}

// ListUsers returns all users without passwords (admin).
func (s *Service) ListUsers() ([]*User, error) {
	list, err := s.store.ListUsers()
	if err != nil {
		return nil, err
	}
	out := make([]*User, 0, len(list))
	for _, u := range list {
		role := u.Role
		if role == "" {
			role = RoleUser
		}
		out = append(out, &User{ID: u.ID, Username: u.Username, Role: role})
	}
	return out, nil
}

// ChangePassword verifies old password and sets a new bcrypt hash.
func (s *Service) ChangePassword(userID, oldPassword, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	u, err := s.store.GetUserByID(userID)
	if err != nil {
		return fmt.Errorf("user not found")
	}
	if isBcryptHash(u.Password) {
		if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(oldPassword)); err != nil {
			return fmt.Errorf("invalid credentials")
		}
	} else if u.Password != oldPassword {
		return fmt.Errorf("invalid credentials")
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.store.UpdateUserPassword(userID, hash); err != nil {
		return err
	}
	// Invalidate all existing sessions after password change.
	_, _ = s.store.BumpTokenVersion(userID)
	return nil
}

// Audit writes a best-effort audit row (never fails the request path loudly).
func (s *Service) Audit(userID, action, target, detail string) {
	_ = s.store.AppendAudit(&store.AuditEvent{
		ID:        uuid.NewString(),
		UserID:    userID,
		Action:    action,
		Target:    target,
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	})
}

// ListAudit returns recent audit events (admin). Filters optional.
func (s *Service) ListAudit(limit int, userID, action string) ([]*store.AuditEvent, error) {
	return s.store.ListAudit(store.AuditFilter{Limit: limit, UserID: userID, Action: action})
}

func (s *Service) issue(userID, username, role string, tokenVersion int, ttl time.Duration) (string, error) {
	if role == "" {
		role = RoleUser
	}
	p := tokenPayload{
		UserID:       userID,
		Username:     username,
		Role:         role,
		Exp:          time.Now().Add(ttl).Unix(),
		JTI:          uuid.NewString(),
		TokenVersion: tokenVersion,
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(raw)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
