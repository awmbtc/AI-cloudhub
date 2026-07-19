package manifest

import (
	"encoding/json"
	"time"
)

// Document is the machine-readable workspace contract for Agents.
// See docs/ARCHITECTURE.md §8.
type Document struct {
	Version    int               `json:"version"`
	Product    string            `json:"product"`
	DriveID    string            `json:"drive_id"`
	MountPoint string            `json:"mount_point"`
	Mode       string            `json:"mode"`
	WriteBarrier string          `json:"write_barrier"`
	IssuedAt   time.Time         `json:"issued_at"`
	ExpiresAt  time.Time         `json:"expires_at,omitempty"`
	Env        map[string]string `json:"env"`
	Agent      AgentPolicy       `json:"agent"`
}

// AgentPolicy constrains how agents may use the workspace.
type AgentPolicy struct {
	AllowedPaths    []string `json:"allowed_paths"`
	DenyUploadTools bool     `json:"deny_upload_tools"`
	Instructions    string   `json:"instructions"`
}

// Input for Build.
type Input struct {
	DriveID    string
	MountPoint string
	Mode       string
	APIBase    string
	TTL        time.Duration
}

// Build creates a Workspace Manifest.
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
	return Document{
		Version:      1,
		Product:      "AI-cloudhub",
		DriveID:      in.DriveID,
		MountPoint:   in.MountPoint,
		Mode:         in.Mode,
		WriteBarrier: "fsync_on_close",
		IssuedAt:     now,
		ExpiresAt:    exp,
		Env: map[string]string{
			"AI_CLOUDHUB_WORKSPACE": in.MountPoint,
			"AI_CLOUDHUB_DRIVE_ID":  in.DriveID,
			"AI_CLOUDHUB_MODE":      in.Mode,
			"AI_CLOUDHUB_API":       in.APIBase,
		},
		Agent: AgentPolicy{
			AllowedPaths:    []string{in.MountPoint},
			DenyUploadTools: true,
			Instructions:    "All artifacts MUST be written under AI_CLOUDHUB_WORKSPACE. Do not use cloud upload APIs.",
		},
	}
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
