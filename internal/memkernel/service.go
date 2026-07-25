// Package memkernel is Stage C Memory Kernel v0 — three logical layers of
// small control-plane memories (not a vector DB / embedding service).
package memkernel

import (
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// Layers (product vocabulary).
const (
	LayerWorking  = "working"  // short-lived task context
	LayerEpisodic = "episodic" // events / session notes
	LayerSemantic = "semantic" // durable facts / preferences
)

// Service is the Memory Kernel API surface (module boundary for future split).
type Service struct {
	st store.Store
}

// New returns a memory service on the control-plane store.
func New(st store.Store) *Service { return &Service{st: st} }

// PutInput creates a memory entry.
type PutInput struct {
	AgentID   string
	DriveID   string
	Layer     string
	Key       string
	Content   string
	MetaJSON  []byte
	TTL       time.Duration // 0 = no expiry
}

// Put stores a memory for userID (and optional agent).
func (s *Service) Put(userID string, in PutInput) (*store.MemoryEntry, error) {
	if s == nil || s.st == nil {
		return nil, fmt.Errorf("memory not configured")
	}
	layer := strings.TrimSpace(in.Layer)
	if layer == "" {
		layer = LayerWorking
	}
	switch layer {
	case LayerWorking, LayerEpisodic, LayerSemantic:
	default:
		return nil, fmt.Errorf("layer must be working|episodic|semantic")
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return nil, fmt.Errorf("content required")
	}
	if len(content) > 64*1024 {
		return nil, fmt.Errorf("content too large (max 64KiB)")
	}
	e := &store.MemoryEntry{
		ID:        uuid.NewString(),
		UserID:    userID,
		AgentID:   strings.TrimSpace(in.AgentID),
		DriveID:   strings.TrimSpace(in.DriveID),
		Layer:     layer,
		Key:       strings.TrimSpace(in.Key),
		Content:   content,
		MetaJSON:  in.MetaJSON,
		CreatedAt: time.Now().UTC(),
	}
	if in.TTL > 0 {
		e.ExpiresAt = time.Now().UTC().Add(in.TTL)
	}
	if err := s.st.CreateMemory(e); err != nil {
		return nil, err
	}
	return e, nil
}

// Get returns one entry owned by userID.
func (s *Service) Get(userID, id string) (*store.MemoryEntry, error) {
	return s.st.GetMemory(userID, id)
}

// List filters memories for the user.
func (s *Service) List(userID string, f store.MemoryFilter) ([]*store.MemoryEntry, error) {
	f.UserID = userID
	return s.st.ListMemory(f)
}

// Delete removes an entry.
func (s *Service) Delete(userID, id string) error {
	return s.st.DeleteMemory(userID, id)
}
