// Package memkernel is Stage C Memory Kernel v0 — three logical layers of
// small control-plane memories (not a vector DB / embedding service).
package memkernel

import (
	"encoding/json"
	"fmt"
	"math"
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

// Limits (honest; reject rather than silent truncate where noted).
const (
	MaxContentBytes   = 64 * 1024 // 64 KiB
	MaxEmbeddingDims  = 4096
	MaxSearchK        = 50
	DefaultSearchK    = 10
	MaxListLimit      = 500
	DefaultListLimit  = 100
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
	Embedding []float32     // optional vector (client-provided); stored as JSON
	TTL       time.Duration // 0 = no expiry
	// ExpiresAt, if set (non-zero) and TTL==0, is stored as absolute expiry.
	ExpiresAt time.Time
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
	if len(content) > MaxContentBytes {
		return nil, fmt.Errorf("content too large (max %d bytes)", MaxContentBytes)
	}
	if len(in.MetaJSON) > MaxContentBytes {
		return nil, fmt.Errorf("meta too large (max %d bytes)", MaxContentBytes)
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
	if len(in.Embedding) > 0 {
		if len(in.Embedding) > MaxEmbeddingDims {
			return nil, fmt.Errorf("embedding too large (max %d dims)", MaxEmbeddingDims)
		}
		b, err := json.Marshal(in.Embedding)
		if err != nil {
			return nil, err
		}
		e.EmbeddingJSON = b
	}
	if in.TTL > 0 {
		e.ExpiresAt = time.Now().UTC().Add(in.TTL)
	} else if !in.ExpiresAt.IsZero() {
		e.ExpiresAt = in.ExpiresAt.UTC()
	}
	if err := s.st.CreateMemory(e); err != nil {
		return nil, err
	}
	return e, nil
}

// SearchHit is a vector search result.
type SearchHit struct {
	Entry *store.MemoryEntry `json:"entry"`
	Score float64            `json:"score"` // cosine similarity [-1,1]
}

// SearchVector finds top-k memories by cosine similarity (client provides query vector).
// Entries without embeddings are skipped. This is NOT a hosted embedding model.
// k must be in 1..MaxSearchK; omit/0 → DefaultSearchK. k > MaxSearchK errors (honest limit).
func (s *Service) SearchVector(userID string, query []float32, k int, layer string) ([]SearchHit, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query embedding required")
	}
	if len(query) > MaxEmbeddingDims {
		return nil, fmt.Errorf("query embedding too large (max %d dims)", MaxEmbeddingDims)
	}
	if k <= 0 {
		k = DefaultSearchK
	}
	if k > MaxSearchK {
		return nil, fmt.Errorf("k too large (max %d)", MaxSearchK)
	}
	list, err := s.List(userID, store.MemoryFilter{Layer: layer, Limit: MaxListLimit})
	if err != nil {
		return nil, err
	}
	var hits []SearchHit
	for _, e := range list {
		if expired(e) {
			continue
		}
		if len(e.EmbeddingJSON) == 0 {
			continue
		}
		var emb []float32
		if err := json.Unmarshal(e.EmbeddingJSON, &emb); err != nil || len(emb) != len(query) {
			continue
		}
		score := cosine(query, emb)
		hits = append(hits, SearchHit{Entry: e, Score: score})
	}
	// sort desc by score
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].Score > hits[i].Score {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func expired(e *store.MemoryEntry) bool {
	if e == nil || e.ExpiresAt.IsZero() {
		return false
	}
	return !e.ExpiresAt.After(time.Now().UTC())
}

// Get returns one non-expired entry owned by userID.
func (s *Service) Get(userID, id string) (*store.MemoryEntry, error) {
	e, err := s.st.GetMemory(userID, id)
	if err != nil {
		return nil, err
	}
	if expired(e) {
		return nil, fmt.Errorf("memory not found")
	}
	return e, nil
}

// List filters memories for the user (store already skips expired rows).
func (s *Service) List(userID string, f store.MemoryFilter) ([]*store.MemoryEntry, error) {
	f.UserID = userID
	if f.Limit <= 0 {
		f.Limit = DefaultListLimit
	}
	if f.Limit > MaxListLimit {
		f.Limit = MaxListLimit
	}
	list, err := s.st.ListMemory(f)
	if err != nil {
		return nil, err
	}
	// Defense in depth: drop any expired that slipped through.
	out := list[:0]
	for _, e := range list {
		if expired(e) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Delete removes an entry.
func (s *Service) Delete(userID, id string) error {
	return s.st.DeleteMemory(userID, id)
}
