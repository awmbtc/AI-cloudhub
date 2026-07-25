// Package idgraph is Identity Graph v0 — typed edges between principals and resources.
package idgraph

import (
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// Service manages identity graph edges.
type Service struct{ st store.Store }

// New creates an identity graph service.
func New(st store.Store) *Service { return &Service{st: st} }

// Link creates/updates an edge subject --relation--> object.
func (s *Service) Link(userID, subject, relation, object string, meta []byte) (*store.GraphEdge, error) {
	if s == nil || s.st == nil {
		return nil, fmt.Errorf("idgraph not configured")
	}
	subject, relation, object = strings.TrimSpace(subject), strings.TrimSpace(relation), strings.TrimSpace(object)
	if subject == "" || relation == "" || object == "" {
		return nil, fmt.Errorf("subject, relation, object required")
	}
	e := &store.GraphEdge{
		ID: uuid.NewString(), UserID: userID, Subject: subject, Relation: relation,
		Object: object, MetaJSON: meta, CreatedAt: time.Now().UTC(),
	}
	// Deterministic id for upsert-like behavior on same triple
	e.ID = hashID(userID, subject, relation, object)
	if err := s.st.UpsertGraphEdge(e); err != nil {
		return nil, err
	}
	return e, nil
}

// List returns edges filtered by subject and/or object.
func (s *Service) List(userID, subject, object string, limit int) ([]*store.GraphEdge, error) {
	return s.st.ListGraphEdges(userID, subject, object, limit)
}

func hashID(parts ...string) string {
	// Stable UUID-ish id from parts (not crypto); use uuid v5-like simple concat hash.
	h := uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join(parts, "|")))
	return h.String()
}
