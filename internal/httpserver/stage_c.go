package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/awmbtc/AI-cloudhub/internal/agent"
	"github.com/awmbtc/AI-cloudhub/internal/marketplace"
	"github.com/awmbtc/AI-cloudhub/internal/memkernel"
	"github.com/awmbtc/AI-cloudhub/internal/modules"
	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func (s *Server) routeMemoryRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.memory == nil {
		writeErr(w, http.StatusNotFound, "memory not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		f := store.MemoryFilter{
			AgentID: q.Get("agent_id"),
			DriveID: q.Get("drive_id"),
			Layer:   q.Get("layer"),
			Key:     q.Get("key"),
		}
		if lim, _ := strconv.Atoi(q.Get("limit")); lim > 0 {
			f.Limit = lim
		}
		// Agents only see their own agent_id memories unless human.
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
			f.AgentID = pr.AgentID
		}
		items, err := s.memory.List(userID, f)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	case http.MethodPost:
		var body struct {
			AgentID   string          `json:"agent_id"`
			DriveID   string          `json:"drive_id"`
			Layer     string          `json:"layer"`
			Key       string          `json:"key"`
			Content   string          `json:"content"`
			Meta      json.RawMessage `json:"meta"`
			Embedding []float32       `json:"embedding"`
			TTLSec    int             `json:"ttl_sec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
			body.AgentID = pr.AgentID
		}
		in := memkernel.PutInput{
			AgentID: body.AgentID, DriveID: body.DriveID, Layer: body.Layer,
			Key: body.Key, Content: body.Content, MetaJSON: body.Meta, Embedding: body.Embedding,
		}
		if body.TTLSec > 0 {
			in.TTL = time.Duration(body.TTLSec) * time.Second
		}
		e, err := s.memory.Put(userID, in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "memory.put", e.ID, e.Layer)
		writeJSON(w, http.StatusCreated, e)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeMemorySub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.memory == nil {
		writeErr(w, http.StatusNotFound, "memory not configured")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/memory/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		e, err := s.memory.Get(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" && e.AgentID != "" && e.AgentID != pr.AgentID {
			writeErr(w, http.StatusForbidden, "memory not owned by agent")
			return
		}
		writeJSON(w, http.StatusOK, e)
	case http.MethodDelete:
		if err := s.memory.Delete(userID, id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		s.auth.Audit(userID, "memory.delete", id, "")
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeMarketplaceRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.market == nil {
		writeErr(w, http.StatusNotFound, "marketplace not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		mine := r.URL.Query().Get("mine") == "1"
		items, err := s.market.List(userID, mine)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	case http.MethodPost:
		if !s.requireHuman(w, r) {
			return
		}
		var body struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			Kind        string                 `json:"kind"`
			Version     string                 `json:"version"`
			Payload     map[string]interface{} `json:"payload"`
			Public      *bool                  `json:"public"`
			PriceCents  int64                  `json:"price_cents"`
			Currency    string                 `json:"currency"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		pub := true
		if body.Public != nil {
			pub = *body.Public
		}
		it, err := s.market.Publish(userID, marketplace.PublishInput{
			Name: body.Name, Description: body.Description, Kind: body.Kind,
			Version: body.Version, Payload: body.Payload, Public: pub,
			PriceCents: body.PriceCents, Currency: body.Currency,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "marketplace.publish", it.ID, it.Kind)
		writeJSON(w, http.StatusCreated, it)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeMarketplaceSub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.market == nil {
		writeErr(w, http.StatusNotFound, "marketplace not configured")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/marketplace/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "install" && r.Method == http.MethodPost {
		if !s.requireHuman(w, r) {
			return
		}
		if s.agents == nil {
			writeErr(w, http.StatusBadRequest, "agents not configured")
			return
		}
		res, err := s.market.InstallAgentTemplate(userID, id, func(name, desc string, scopes []string) (string, error) {
			a, err := s.agents.Create(userID, agent.CreateInput{
				Name: name, Description: desc, DefaultScopes: scopes,
			})
			if err != nil {
				return "", err
			}
			return a.ID, nil
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "marketplace.install", id, res.AgentID)
		writeJSON(w, http.StatusCreated, res)
		return
	}
	if len(parts) == 2 && parts[1] == "checkout" {
		s.routeMarketplaceCheckout(w, r, userID, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		it, err := s.market.Get(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, it)
	case http.MethodDelete:
		if !s.requireHuman(w, r) {
			return
		}
		if err := s.market.Delete(userID, id); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auth.Audit(userID, "marketplace.delete", id, "")
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deployment": "monolith",
		"note":       "Logical modules only; default is single cmd/api process (D-002). Not a platform runner pool (D-001).",
		"modules":    modules.Registry(),
	})
}

