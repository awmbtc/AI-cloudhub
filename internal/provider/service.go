package provider

import (
	"fmt"
	"log"
	"strings"

	"github.com/awmbtc/AI-cloudhub/internal/crypto/secretbox"
	"github.com/awmbtc/AI-cloudhub/internal/policy"
	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// Service stores user provider bindings.
// When box is non-nil, secret_key is envelope-encrypted before persistence.
// When box is nil (dev: AI_CLOUDHUB_MASTER_KEY empty), secrets stay plaintext.
type Service struct {
	store store.Store
	box   *secretbox.Box
}

// NewService creates a provider registry backed by store (plaintext secrets).
func NewService(st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{store: st}
}

// NewServiceWithBox creates a registry that seals secret_key with the master key.
// Pass nil box for plaintext storage (same as NewService).
func NewServiceWithBox(st store.Store, box *secretbox.Box) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{store: st, box: box}
}

// SetBox attaches or clears envelope encryption. Existing rows are not re-sealed.
func (s *Service) SetBox(box *secretbox.Box) {
	s.box = box
}

// EncryptionEnabled reports whether new secrets will be sealed at rest.
func (s *Service) EncryptionEnabled() bool {
	return s.box != nil
}

// CreateInput is the API body for registering a provider.
type CreateInput struct {
	Name  string      `json:"name"`
	Type  Type        `json:"type"`
	Creds Credentials `json:"credentials"`
}

// Create validates and stores a provider for the user.
func (s *Service) Create(userID string, in CreateInput) (*Record, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, fmt.Errorf("name required")
	}
	if !IsImplemented(in.Type) {
		return nil, fmt.Errorf("provider %q not implemented yet; see docs/VENDORS.md", in.Type)
	}
	// Per-user provider quota (default 20).
	if list, err := s.store.ListProviders(userID); err == nil {
		if err := policy.DefaultQuota.CheckProviders(len(list)); err != nil {
			return nil, err
		}
	}
	resolved, err := Resolve(in.Type, in.Creds)
	if err != nil {
		return nil, err
	}
	rec := &Record{
		ID:             uuid.NewString(),
		UserID:         userID,
		Name:           in.Name,
		Type:           in.Type,
		Creds:          in.Creds,
		EndpointPublic: resolved.Endpoint,
		Region:         resolved.Region,
		AccountID:      strings.TrimSpace(in.Creds.AccountID),
	}
	// Keep normalized endpoint on creds for later resolve
	rec.Creds.Endpoint = resolved.Endpoint
	if rec.Creds.Region == "" {
		rec.Creds.Region = resolved.Region
	}

	if s.box != nil {
		sealed, err := s.box.Seal([]byte(rec.Creds.SecretKey))
		if err != nil {
			return nil, fmt.Errorf("encrypt secret: %w", err)
		}
		rec.SecretEnc = sealed
		rec.Creds.SecretKey = "" // clear plaintext at rest
	}

	p, err := recordToStore(rec)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateProvider(p); err != nil {
		return nil, err
	}
	return rec, nil
}

// Get returns a provider if owned by userID.
// SecretKey may be empty when SecretEnc is set; use ResolveRecord for usable creds.
func (s *Service) Get(userID, id string) (*Record, error) {
	p, err := s.store.GetProvider(userID, id)
	if err != nil {
		return nil, fmt.Errorf("provider not found")
	}
	return recordFromStore(p)
}

// List returns providers for a user.
func (s *Service) List(userID string) []*Record {
	list, err := s.store.ListProviders(userID)
	if err != nil {
		return nil
	}
	var out []*Record
	for _, p := range list {
		rec, err := recordFromStore(p)
		if err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Delete removes a provider.
func (s *Service) Delete(userID, id string) error {
	if err := s.store.DeleteProvider(userID, id); err != nil {
		return fmt.Errorf("provider not found")
	}
	return nil
}

// ResolveRecord returns connection params for mount / S3 client.
// Decrypts SecretEnc when envelope encryption is enabled.
func (s *Service) ResolveRecord(userID, id string) (*Resolved, *Record, error) {
	rec, err := s.Get(userID, id)
	if err != nil {
		return nil, nil, err
	}
	creds, err := s.plaintextCreds(rec)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := Resolve(rec.Type, creds)
	if err != nil {
		return nil, nil, err
	}
	return resolved, rec, nil
}

// plaintextCreds returns credentials with SecretKey filled from SecretEnc when needed.
func (s *Service) plaintextCreds(rec *Record) (Credentials, error) {
	c := rec.Creds
	if len(rec.SecretEnc) == 0 {
		return c, nil
	}
	if s.box == nil {
		return Credentials{}, fmt.Errorf("provider %s has encrypted secret but master key is not configured", rec.ID)
	}
	pt, err := s.box.Open(rec.SecretEnc)
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt secret: %w", err)
	}
	c.SecretKey = string(pt)
	return c, nil
}

// LogDevModeWarning logs notice when running without master key.
// Safe to call at process start.
func LogDevModeWarning() {
	log.Printf("WARNING: %s unset — provider secrets stored in PLAINTEXT (dev mode only; set a 32-byte base64/hex key or passphrase for production)", secretbox.EnvMasterKey)
}

// credsBlob is the on-disk JSON shape for store.Provider.CredsJSON.
// Includes optional sealed secret so plaintext secret_key can be empty.
type credsBlob struct {
	AccessKey      string `json:"access_key"`
	SecretKey      string `json:"secret_key"`
	Endpoint       string `json:"endpoint"`
	Region         string `json:"region,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
	ForcePathStyle *bool  `json:"force_path_style,omitempty"`
	UseSSL         *bool  `json:"use_ssl,omitempty"`
	// SecretEnc is NaCl secretbox ciphertext for SecretKey (ACH1||nonce||ct).
	SecretEnc []byte `json:"secret_enc,omitempty"`
}

func recordToStore(rec *Record) (*store.Provider, error) {
	blob := credsBlob{
		AccessKey:      rec.Creds.AccessKey,
		SecretKey:      rec.Creds.SecretKey,
		Endpoint:       rec.Creds.Endpoint,
		Region:         rec.Creds.Region,
		AccountID:      rec.Creds.AccountID,
		ForcePathStyle: rec.Creds.ForcePathStyle,
		UseSSL:         rec.Creds.UseSSL,
		SecretEnc:      rec.SecretEnc,
	}
	creds, err := store.MarshalJSON(blob)
	if err != nil {
		return nil, err
	}
	return &store.Provider{
		ID:             rec.ID,
		UserID:         rec.UserID,
		Name:           rec.Name,
		Type:           string(rec.Type),
		CredsJSON:      creds,
		EndpointPublic: rec.EndpointPublic,
		Region:         rec.Region,
		AccountID:      rec.AccountID,
	}, nil
}

func recordFromStore(p *store.Provider) (*Record, error) {
	var blob credsBlob
	if len(p.CredsJSON) > 0 {
		if err := store.UnmarshalJSON(p.CredsJSON, &blob); err != nil {
			return nil, fmt.Errorf("decode credentials: %w", err)
		}
	}
	return &Record{
		ID:     p.ID,
		UserID: p.UserID,
		Name:   p.Name,
		Type:   Type(p.Type),
		Creds: Credentials{
			AccessKey:      blob.AccessKey,
			SecretKey:      blob.SecretKey,
			Endpoint:       blob.Endpoint,
			Region:         blob.Region,
			AccountID:      blob.AccountID,
			ForcePathStyle: blob.ForcePathStyle,
			UseSSL:         blob.UseSSL,
		},
		SecretEnc:      blob.SecretEnc,
		EndpointPublic: p.EndpointPublic,
		Region:         p.Region,
		AccountID:      p.AccountID,
	}, nil
}
