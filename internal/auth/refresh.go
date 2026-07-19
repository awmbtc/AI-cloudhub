package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// DefaultRefreshTTL is 7 days when not configured.
const DefaultRefreshTTL = 7 * 24 * time.Hour

// TokenPair is access + refresh issued together.
type TokenPair struct {
	AccessToken  string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // access token seconds
	TokenType    string `json:"token_type"`
	User         *User  `json:"user,omitempty"`
}

// SetRefreshTTL configures refresh token lifetime (zero → DefaultRefreshTTL).
func (s *Service) SetRefreshTTL(ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultRefreshTTL
	}
	s.refreshTTL = ttl
}

func (s *Service) refreshTTLOrDefault() time.Duration {
	if s.refreshTTL <= 0 {
		return DefaultRefreshTTL
	}
	return s.refreshTTL
}

// IssueTokens builds access + refresh for a known user record.
func (s *Service) IssueTokens(userID, username, role string, tokenVersion int) (*TokenPair, error) {
	if role == "" {
		role = RoleUser
	}
	accessTTL := s.tokenTTLOrDefault()
	// Human login tokens: no agent_id / unrestricted scopes.
	access, err := s.issue(userID, username, role, tokenVersion, accessTTL, "", nil)
	if err != nil {
		return nil, err
	}
	raw, hash, err := newRefreshSecret()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	rt := &store.RefreshToken{
		ID:        uuid.NewString(),
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: now.Add(s.refreshTTLOrDefault()),
		CreatedAt: now,
	}
	if err := s.store.CreateRefreshToken(rt); err != nil {
		return nil, fmt.Errorf("persist refresh: %w", err)
	}
	return &TokenPair{
		AccessToken:  access,
		RefreshToken: raw,
		ExpiresIn:    int64(accessTTL.Seconds()),
		TokenType:    "Bearer",
		User:         &User{ID: userID, Username: username, Role: role},
	}, nil
}

// Refresh exchanges a valid refresh token for a new access + rotated refresh.
// Old refresh is revoked (rotation).
func (s *Service) Refresh(rawRefresh string) (*TokenPair, error) {
	rawRefresh = strings.TrimSpace(rawRefresh)
	if rawRefresh == "" {
		return nil, fmt.Errorf("refresh_token required")
	}
	hash := hashRefresh(rawRefresh)
	rec, err := s.store.GetRefreshTokenByHash(hash)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token")
	}
	u, err := s.store.GetUserByID(rec.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token")
	}
	role := u.Role
	if role == "" {
		role = RoleUser
	}
	// Rotate: revoke old refresh before issuing new pair.
	_ = s.store.RevokeRefreshToken(rec.ID)
	return s.IssueTokens(u.ID, u.Username, role, u.TokenVersion)
}

// RevokeRefresh invalidates one opaque refresh token (best-effort).
func (s *Service) RevokeRefresh(rawRefresh string) {
	if rawRefresh == "" {
		return
	}
	hash := hashRefresh(rawRefresh)
	if rec, err := s.store.GetRefreshTokenByHash(hash); err == nil {
		_ = s.store.RevokeRefreshToken(rec.ID)
	}
}

func newRefreshSecret() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashRefresh(raw), nil
}

func hashRefresh(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
