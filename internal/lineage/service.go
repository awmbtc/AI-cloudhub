// Package lineage is Data Lineage v0 — append-only control-plane activity graph.
package lineage

import (
	"fmt"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/store"
	"github.com/google/uuid"
)

// Service records and queries lineage events.
type Service struct{ st store.Store }

// New creates a lineage service.
func New(st store.Store) *Service { return &Service{st: st} }

// Record appends an event.
func (s *Service) Record(userID, actorID, action, entity, parent, detail string) (*store.LineageEvent, error) {
	if s == nil || s.st == nil {
		return nil, fmt.Errorf("lineage not configured")
	}
	action = strings.TrimSpace(action)
	entity = strings.TrimSpace(entity)
	if action == "" || entity == "" {
		return nil, fmt.Errorf("action and entity required")
	}
	e := &store.LineageEvent{
		ID: uuid.NewString(), UserID: userID, ActorID: actorID,
		Action: action, Entity: entity, Parent: strings.TrimSpace(parent),
		Detail: strings.TrimSpace(detail), CreatedAt: time.Now().UTC(),
	}
	if err := s.st.AppendLineage(e); err != nil {
		return nil, err
	}
	return e, nil
}

// List returns recent events, optionally filtered by entity.
func (s *Service) List(userID, entity string, limit int) ([]*store.LineageEvent, error) {
	return s.st.ListLineage(userID, entity, limit)
}
