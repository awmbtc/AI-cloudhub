package httpserver

import (
	"encoding/json"
	"fmt"

	"github.com/awmbtc/AI-cloudhub/internal/marketplace"
	"github.com/awmbtc/AI-cloudhub/internal/memkernel"
)

// installSideEffects captures optional memory/graph artifacts written after install.
type installSideEffects struct {
	MemoryID string
}

// recordMarketplaceInstall best-effort writes:
//   - episodic memory key marketplace.install.<itemID>
//   - graph edges user→installed→item, agent→from_item→item
//   - lineage marketplace.install
// Failures are ignored so install still succeeds (control-plane soft side effects).
func (s *Server) recordMarketplaceInstall(userID, itemID string, res *marketplace.AgentInstallResult) installSideEffects {
	var out installSideEffects
	if res == nil {
		return out
	}
	agentID := res.AgentID
	detail := fmt.Sprintf("agent=%s name=%s", agentID, res.Name)

	if s.lineage != nil {
		_, _ = s.lineage.Record(userID, "user:"+userID, "marketplace.install",
			"item:"+itemID, "agent:"+agentID, detail)
	}

	if s.memory != nil {
		meta, _ := json.Marshal(map[string]interface{}{
			"item_id":  itemID,
			"agent_id": agentID,
			"name":     res.Name,
			"scopes":   res.Scopes,
			"source":   "marketplace.install",
		})
		e, err := s.memory.Put(userID, memkernel.PutInput{
			AgentID:  agentID,
			Layer:    memkernel.LayerEpisodic,
			Key:      "marketplace.install." + itemID,
			Content:  fmt.Sprintf("Installed marketplace item %s as agent %s (%s)", itemID, agentID, res.Name),
			MetaJSON: meta,
		})
		if err == nil && e != nil {
			out.MemoryID = e.ID
			if s.lineage != nil {
				_, _ = s.lineage.Record(userID, "user:"+userID, "memory.put",
					"memory:"+e.ID, "item:"+itemID, "marketplace.install")
			}
			if s.idgraph != nil {
				_, _ = s.idgraph.Link(userID, "agent:"+agentID, "wrote_memory", "memory:"+e.ID, nil)
			}
		}
	}

	if s.idgraph != nil {
		// user installed catalog item; agent was materialized from item
		_, _ = s.idgraph.Link(userID, "user:"+userID, "installed", "item:"+itemID, nil)
		if agentID != "" {
			_, _ = s.idgraph.Link(userID, "agent:"+agentID, "from_item", "item:"+itemID, nil)
			_, _ = s.idgraph.Link(userID, "user:"+userID, "owns_agent", "agent:"+agentID, nil)
		}
	}
	return out
}
