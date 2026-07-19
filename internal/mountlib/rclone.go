package mountlib

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/awmbtc/AI-cloudhub/internal/provider"
)

const DefaultRemote = "aihub"

// Spec is a normalized mount specification for any runtime (hubd / runner).
type Spec struct {
	RemoteName string `json:"remote_name"`
	RemotePath string `json:"remote_path"` // remote:bucket[/prefix]
	MountPoint string `json:"mount_point"`
	RcloneConf string `json:"rclone_conf"`
	Mode       string `json:"mode"` // mount | sync_workspace
}

// BuildConf generates rclone config for S3-compatible providers.
func BuildConf(remoteName string, r *provider.Resolved) string {
	if remoteName == "" {
		remoteName = DefaultRemote
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", remoteName)
	fmt.Fprintf(&b, "type = s3\n")
	fmt.Fprintf(&b, "provider = %s\n", rcloneProvider(r))
	fmt.Fprintf(&b, "access_key_id = %s\n", r.AccessKey)
	fmt.Fprintf(&b, "secret_access_key = %s\n", r.SecretKey)
	if r.SessionToken != "" {
		fmt.Fprintf(&b, "session_token = %s\n", r.SessionToken)
	}
	fmt.Fprintf(&b, "endpoint = %s\n", endpointURL(r))
	fmt.Fprintf(&b, "region = %s\n", r.Region)
	if r.ForcePathStyle {
		fmt.Fprintf(&b, "force_path_style = true\n")
	} else {
		fmt.Fprintf(&b, "force_path_style = false\n")
	}
	fmt.Fprintf(&b, "acl = private\n")
	fmt.Fprintf(&b, "no_check_bucket = true\n")
	return b.String()
}

// RemotePath builds remote:bucket[/prefix].
func RemotePath(remote, bucket, prefix string) string {
	if remote == "" {
		remote = DefaultRemote
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return fmt.Sprintf("%s:%s", remote, bucket)
	}
	return fmt.Sprintf("%s:%s/%s", remote, bucket, prefix)
}

// NewSpec builds a full mount spec.
func NewSpec(r *provider.Resolved, bucket, prefix, mountPoint, mode string) Spec {
	if mode == "" {
		mode = "mount"
	}
	conf := BuildConf(DefaultRemote, r)
	return Spec{
		RemoteName: DefaultRemote,
		RemotePath: RemotePath(DefaultRemote, bucket, prefix),
		MountPoint: mountPoint,
		RcloneConf: conf,
		Mode:       mode,
	}
}

// MountArgs returns rclone argv (without binary name).
func MountArgs(spec Spec, confPath string) []string {
	args := []string{
		"mount", spec.RemotePath, spec.MountPoint,
		"--config", confPath,
		"--vfs-cache-mode", "full",
		"--dir-cache-time", "10s",
	}
	if runtime.GOOS != "windows" {
		// daemon mode optional; hubd manages process lifecycle itself
	}
	return args
}

// UnmountHint is a human/shell unmount suggestion.
func UnmountHint(mountPoint string) string {
	if runtime.GOOS == "windows" {
		return "stop hubd/rclone process holding the mount"
	}
	return fmt.Sprintf("fusermount -u %s 2>/dev/null || umount %s", mountPoint, mountPoint)
}

func rcloneProvider(r *provider.Resolved) string {
	if r.ProviderLabel != "" {
		return r.ProviderLabel
	}
	switch r.Type {
	case provider.TypeR2:
		return "Cloudflare"
	case provider.TypeMinIO:
		return "Minio"
	default:
		return "Other"
	}
}

func endpointURL(r *provider.Resolved) string {
	scheme := "https"
	if !r.UseSSL {
		scheme = "http"
	}
	ep := r.Endpoint
	if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		return ep
	}
	return scheme + "://" + ep
}
