package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/awmbtc/AI-cloudhub/internal/connector"
)

func (s *Server) routeLineage(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.lineage == nil {
		writeErr(w, http.StatusNotFound, "lineage not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		entity := r.URL.Query().Get("entity")
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := s.lineage.List(userID, entity, lim)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
			Entity string `json:"entity"`
			Parent string `json:"parent"`
			Detail string `json:"detail"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		actor := userID
		if pr := principalFrom(r); pr != nil && pr.AgentID != "" {
			actor = "agent:" + pr.AgentID
		} else {
			actor = "user:" + userID
		}
		e, err := s.lineage.Record(userID, actor, body.Action, body.Entity, body.Parent, body.Detail)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, e)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeGraph(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.idgraph == nil {
		writeErr(w, http.StatusNotFound, "idgraph not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sub, obj := r.URL.Query().Get("subject"), r.URL.Query().Get("object")
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := s.idgraph.List(userID, sub, obj, lim)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	case http.MethodPost:
		var body struct {
			Subject  string          `json:"subject"`
			Relation string          `json:"relation"`
			Object   string          `json:"object"`
			Meta     json.RawMessage `json:"meta"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		e, err := s.idgraph.Link(userID, body.Subject, body.Relation, body.Object, body.Meta)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, e)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeConnectorsRoot(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.connectors == nil {
		writeErr(w, http.StatusNotFound, "connectors not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.connectors.List(userID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "catalog": connector.Catalog()})
	case http.MethodPost:
		if !s.requireHuman(w, r) {
			return
		}
		var in connector.CreateInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		c, err := s.connectors.Create(userID, in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if s.lineage != nil {
			_, _ = s.lineage.Record(userID, "user:"+userID, "connector.register", "connector:"+c.ID, "", c.Type)
		}
		writeJSON(w, http.StatusCreated, c)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeConnectorsSub(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.connectors == nil {
		writeErr(w, http.StatusNotFound, "connectors not configured")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/connectors/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		c, err := s.connectors.Get(userID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, c)
	case http.MethodDelete:
		if !s.requireHuman(w, r) {
			return
		}
		if err := s.connectors.Delete(userID, id); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleConnectorCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": connector.Catalog()})
}

// memory search + marketplace checkout extensions via same root handlers patch
func (s *Server) routeMemorySearch(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.memory == nil {
		writeErr(w, http.StatusNotFound, "memory not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Query []float32 `json:"query"`
		K     int       `json:"k"`
		Layer string    `json:"layer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	hits, err := s.memory.SearchVector(userID, body.Query, body.K, body.Layer)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"hits": hits})
}

func (s *Server) routeMarketplaceCheckout(w http.ResponseWriter, r *http.Request, userID, itemID string) {
	if s.market == nil {
		writeErr(w, http.StatusNotFound, "marketplace not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireHuman(w, r) {
		return
	}
	p, err := s.market.CheckoutDetailed(userID, itemID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.lineage != nil {
		_, _ = s.lineage.Record(userID, "user:"+userID, "marketplace.checkout", "item:"+itemID, "purchase:"+p.ID, p.Status)
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.market == nil {
		writeErr(w, http.StatusNotFound, "marketplace not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body")
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	p, err := s.market.HandleStripeWebhook(body, sig)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.lineage != nil {
		_, _ = s.lineage.Record(p.UserID, "stripe", "marketplace.paid", "purchase:"+p.ID, "item:"+p.ItemID, p.ProviderRef)
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) routePurchases(w http.ResponseWriter, r *http.Request, userID, _, _ string) {
	if s.market == nil {
		writeErr(w, http.StatusNotFound, "marketplace not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireHuman(w, r) {
		return
	}
	items, err := s.market.ListPurchases(userID, 50)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (s *Server) routePurchaseWebhook(w http.ResponseWriter, r *http.Request, userID, purchaseID string) {
	// Internal/dev webhook: mark paid. Production would verify Stripe signature.
	if s.market == nil {
		writeErr(w, http.StatusNotFound, "marketplace not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ProviderRef string `json:"provider_ref"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	p, err := s.market.WebhookPaid(userID, purchaseID, body.ProviderRef)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

