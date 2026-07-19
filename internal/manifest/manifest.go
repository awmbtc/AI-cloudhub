package manifest

import (
	"encoding/json"
	"strings"
	"time"
)

// Document is the machine-readable workspace contract for Agents.
// See docs/ARCHITECTURE.md §8 and ROADMAP-2.0 Manifest 2.0.
type Document struct {
	Version      int               `json:"version"`
	Product      string            `json:"product"`
	DriveID      string            `json:"drive_id"`
	MountPoint   string            `json:"mount_point"`
	Mode         string            `json:"mode"`
	WriteBarrier string            `json:"write_barrier"`
	IssuedAt     time.Time         `json:"issued_at"`
	ExpiresAt    time.Time         `json:"expires_at,omitempty"`
	Env          map[string]string `json:"env"`
	Agent        AgentPolicy       `json:"agent"`
	// Permissions is Manifest 2.0 (optional; empty = full workspace).
	Permissions *Permissions `json:"permissions,omitempty"`
}

// Permissions describes path prefixes under the mount (Manifest 2.0).
type Permissions struct {
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
}

// AgentPolicy constrains how agents may use the workspace.
type AgentPolicy struct {
	AgentID         string   `json:"agent_id,omitempty"`
	AllowedPaths    []string `json:"allowed_paths"`
	DenyUploadTools bool     `json:"deny_upload_tools"`
	Instructions    string   `json:"instructions"`
}

// Input for Build.
type Input struct {
	DriveID       string
	MountPoint    string
	Mode          string
	APIBase       string
	TTL           time.Duration
	AgentID       string
	ReadPrefixes  []string
	WritePrefixes []string
}

// Build creates a Workspace Manifest (v1 compatible; v2 when prefixes set).
func Build(in Input) Document {
	if in.Mode == "" {
		in.Mode = "mount"
	}
	if in.MountPoint == "" {
		in.MountPoint = "/workspace"
	}
	now := time.Now().UTC()
	var exp time.Time
	if in.TTL > 0 {
		exp = now.Add(in.TTL)
	}
	ver := 1
	allowed := []string{in.MountPoint}
	var perms *Permissions
	if len(in.ReadPrefixes) > 0 || len(in.WritePrefixes) > 0 {
		ver = 2
		perms = &Permissions{
			Read:  joinPrefixes(in.MountPoint, in.ReadPrefixes),
			Write: joinPrefixes(in.MountPoint, in.WritePrefixes),
		}
		// allowed_paths = union of read+write for older consumers
		allowed = nil
		seen := map[string]bool{}
		for _, p := range perms.Read {
			if !seen[p] {
				seen[p] = true
				allowed = append(allowed, p)
			}
		}
		for _, p := range perms.Write {
			if !seen[p] {
				seen[p] = true
				allowed = append(allowed, p)
			}
		}
		if len(allowed) == 0 {
			allowed = []string{in.MountPoint}
		}
	}
	env := map[string]string{
		"AI_CLOUDHUB_WORKSPACE": in.MountPoint,
		"AI_CLOUDHUB_DRIVE_ID":  in.DriveID,
		"AI_CLOUDHUB_MODE":      in.Mode,
		"AI_CLOUDHUB_API":       in.APIBase,
	}
	if in.AgentID != "" {
		env["AI_CLOUDHUB_AGENT_ID"] = in.AgentID
	}
	return Document{
		Version:      ver,
		Product:      "AI-cloudhub",
		DriveID:      in.DriveID,
		MountPoint:   in.MountPoint,
		Mode:         in.Mode,
		WriteBarrier: "fsync_on_close",
		IssuedAt:     now,
		ExpiresAt:    exp,
		Env:          env,
		Permissions:  perms,
		Agent: AgentPolicy{
			AgentID:         in.AgentID,
			AllowedPaths:    allowed,
			DenyUploadTools: true,
			Instructions:    "All artifacts MUST be written under AI_CLOUDHUB_WORKSPACE (and permissions.write when set). Do not use cloud upload APIs.",
		},
	}
}

func joinPrefixes(mount string, prefs []string) []string {
	if len(prefs) == 0 {
		return nil
	}
	var out []string
	for _, p := range prefs {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if p == "" {
			out = append(out, mount)
			continue
		}
		out = append(out, strings.TrimRight(mount, "/")+"/"+p)
	}
	return out
}

// JSON returns pretty JSON bytes.
func (d Document) JSON() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

// EnvFile returns KEY=VALUE lines for shell export.
func (d Document) EnvFile() string {
	out := ""
	for k, v := range d.Env {
		out += k + "=" + v + "\n"
	}
	return out
}
