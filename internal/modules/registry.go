// Package modules describes logical control-plane modules (Stage C).
// Default deployment remains a single cmd/api process (D-002: no forced microservices).
// Each entry is a package boundary that *could* become a process later.
package modules

// Module is a logical service unit in the monorepo.
type Module struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Package     string   `json:"package"`
	Process     string   `json:"process"` // always "api" for now
	Status      string   `json:"status"`  // embedded | optional | external
	Description string   `json:"description"`
	APIs        []string `json:"apis,omitempty"`
}

// Registry returns the Stage C module map for diagnostics.
func Registry() []Module {
	return []Module{
		{ID: "identity", Name: "Identity & Auth", Package: "internal/auth", Process: "api", Status: "embedded",
			Description: "Users, JWT, refresh, agents tokens", APIs: []string{"/v1/auth/*", "/v1/me", "/v1/agents*"}},
		{ID: "policy", Name: "Policy Engine", Package: "internal/policy", Process: "api", Status: "embedded",
			Description: "Built-in + JSON + OPA + remote PDP client", APIs: []string{"/v1/admin/policy"}},
		{ID: "drive", Name: "Drive & Bindings", Package: "internal/drive", Process: "api", Status: "embedded",
			Description: "Drives, bindings, sessions, objects, snapshots", APIs: []string{"/v1/drives*", "/v1/bindings*", "/v1/sessions/*"}},
		{ID: "sts", Name: "STS Assist", Package: "internal/sts", Process: "api", Status: "embedded",
			Description: "Multi-vendor short sessions; OCI PAR/secret Stage C", APIs: []string{"session.source"}},
		{ID: "jobs", Name: "BYOC Jobs", Package: "internal/job", Process: "api", Status: "embedded",
			Description: "Job queue; runners claim on user compute only (D-001)", APIs: []string{"/v1/jobs*"}},
		{ID: "memory", Name: "Memory Kernel v0", Package: "internal/memkernel", Process: "api", Status: "embedded",
			Description: "working/episodic/semantic small memories", APIs: []string{"/v1/memory*"}},
		{ID: "marketplace", Name: "Agent Marketplace v0", Package: "internal/marketplace", Process: "api", Status: "embedded",
			Description: "Templates/skills + checkout skeleton", APIs: []string{"/v1/marketplace*", "/v1/purchases*"}},
		{ID: "lineage", Name: "Data Lineage v0", Package: "internal/lineage", Process: "api", Status: "embedded",
			Description: "Append-only activity events", APIs: []string{"/v1/lineage"}},
		{ID: "idgraph", Name: "Identity Graph v0", Package: "internal/idgraph", Process: "api", Status: "embedded",
			Description: "Subject-relation-object edges", APIs: []string{"/v1/graph"}},
		{ID: "connectors", Name: "Git/DB/SaaS connectors", Package: "internal/connector", Process: "api", Status: "embedded",
			Description: "First-class connector catalog + bindings (sync on BYOC)", APIs: []string{"/v1/connectors*"}},
		{ID: "pdp-remote", Name: "Remote PDP", Package: "internal/policy", Process: "external", Status: "optional",
			Description: "HTTP PDP you host; AI_CLOUDHUB_PDP_URL", APIs: []string{"POST AI_CLOUDHUB_PDP_URL"}},
		{ID: "runtime", Name: "hubd / runner", Package: "cmd/hubd,cmd/runner", Process: "user-host", Status: "external",
			Description: "Mount and BYOC workers on user machines — never a platform pool", APIs: []string{}},
	}
}
