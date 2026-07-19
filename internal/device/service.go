// Package device provides hubd/runtime device registration for the control plane.
package device

import (
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// Device is a registered hubd/runtime endpoint.
type Device struct {
	ID       string    `json:"id"`
	UserID   string    `json:"user_id"`
	Name     string    `json:"name"`
	LastSeen time.Time `json:"last_seen"`
}

// RegisterInput is the body for POST /v1/devices.
type RegisterInput struct {
	// ID is optional; when empty a new UUID is generated. hubd should pass AI_CLOUDHUB_DEVICE_ID.
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Service manages device registration.
type Service struct {
	store store.Store
}

// NewService creates a device service backed by store.
func NewService(st store.Store) *Service {
	if st == nil {
		st = store.NewMemory()
	}
	return &Service{store: st}
}

// Register upserts a device for the user and refreshes LastSeen.
func (s *Service) Register(userID string, in RegisterInput) (*Device, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = uuid.NewString()
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = id
	}
	now := time.Now().UTC()
	// Preserve existing name if re-registering with empty name was already handled above.
	if existing, err := s.store.GetDevice(userID, id); err == nil {
		if strings.TrimSpace(in.Name) == "" {
			name = existing.Name
		}
	}
	d := &Device{
		ID:       id,
		UserID:   userID,
		Name:     name,
		LastSeen: now,
	}
	if err := s.store.UpsertDevice(deviceToStore(d)); err != nil {
		return nil, err
	}
	return d, nil
}

// Get returns a device owned by user.
func (s *Service) Get(userID, id string) (*Device, error) {
	d, err := s.store.GetDevice(userID, id)
	if err != nil {
		return nil, fmt.Errorf("device not found")
	}
	return deviceFromStore(d), nil
}

// List returns devices for user.
func (s *Service) List(userID string) []*Device {
	list, err := s.store.ListDevices(userID)
	if err != nil {
		return nil
	}
	out := make([]*Device, 0, len(list))
	for _, d := range list {
		out = append(out, deviceFromStore(d))
	}
	return out
}

// Touch updates LastSeen for an existing device (no-op if missing).
func (s *Service) Touch(userID, id string) error {
	d, err := s.store.GetDevice(userID, id)
	if err != nil {
		return err
	}
	d.LastSeen = time.Now().UTC()
	return s.store.UpsertDevice(d)
}

func deviceToStore(d *Device) *store.Device {
	return &store.Device{
		ID:       d.ID,
		UserID:   d.UserID,
		Name:     d.Name,
		LastSeen: d.LastSeen,
	}
}

func deviceFromStore(d *store.Device) *Device {
	return &Device{
		ID:       d.ID,
		UserID:   d.UserID,
		Name:     d.Name,
		LastSeen: d.LastSeen,
	}
}
