package sandbox

import (
	"os"
	"strings"
)

// DefaultEnvAllowPrefixes are safe keys (or prefixes) passed into agent processes.
// Matching is case-insensitive; exact keys or "PREFIX_" form.
var DefaultEnvAllowPrefixes = []string{
	"AI_CLOUDHUB_",
	"PATH",
	"HOME",
	"USER",
	"LOGNAME",
	"LANG",
	"LC_",
	"TZ",
	"TERM",
	"TMPDIR",
	"TEMP",
	"TMP",
	"PWD",
	"SHELL",
	"HOSTNAME",
	// Common non-secret runtime hints (not cloud long-lived keys)
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"NO_PROXY",
	"http_proxy",
	"https_proxy",
	"no_proxy",
}

// DefaultEnvBlockExact never passes these even if allow-listed by mistake.
var DefaultEnvBlockExact = []string{
	"AWS_SECRET_ACCESS_KEY",
	"AWS_ACCESS_KEY_ID",
	"AWS_SESSION_TOKEN",
	"MINIO_SECRET_KEY",
	"MINIO_ACCESS_KEY",
	"AI_CLOUDHUB_TOKEN", // parent API token must not leak into untrusted agent unless explicit
	"JWT_SECRET",
	"AI_CLOUDHUB_MASTER_KEY",
}

// Network-related keys stripped when DenyNetwork is set.
var networkEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	"FTP_PROXY", "ftp_proxy", "SOCKS_PROXY", "socks_proxy",
}

// EnvFilter filters process environment for Sandbox v1.
type EnvFilter struct {
	// AllowPrefixes: if empty, DefaultEnvAllowPrefixes is used.
	AllowPrefixes []string
	// BlockExact: if empty, DefaultEnvBlockExact is used.
	BlockExact []string
	// PassToken when true allows AI_CLOUDHUB_TOKEN (default false).
	PassToken bool
	// DenyNetwork strips proxy-related env and injects AI_CLOUDHUB_NETWORK=deny.
	// This is a soft policy for agents; it does not enforce kernel netns.
	DenyNetwork bool
}

// FilterEnv returns KEY=VALUE entries safe for child agent processes.
// base is typically os.Environ(); extra are forced injects (manifest env) after filter.
func FilterEnv(base []string, extra map[string]string, f EnvFilter) []string {
	allow := f.AllowPrefixes
	if len(allow) == 0 {
		allow = DefaultEnvAllowPrefixes
	}
	block := f.BlockExact
	if len(block) == 0 {
		block = DefaultEnvBlockExact
	}
	blockSet := map[string]bool{}
	for _, b := range block {
		blockSet[strings.ToUpper(b)] = true
	}
	if f.PassToken {
		delete(blockSet, "AI_CLOUDHUB_TOKEN")
	}
	if f.DenyNetwork {
		for _, k := range networkEnvKeys {
			blockSet[strings.ToUpper(k)] = true
		}
		// also strip from allow prefixes matching PROXY
	}

	out := make([]string, 0, len(base)+len(extra)+2)
	seen := map[string]bool{}

	add := func(kv string) {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return
		}
		key := kv[:i]
		uk := strings.ToUpper(key)
		if blockSet[uk] {
			return
		}
		if f.DenyNetwork && strings.Contains(uk, "PROXY") {
			return
		}
		if !keyAllowed(key, allow) {
			return
		}
		// last write wins
		if seen[uk] {
			// replace existing
			for j, e := range out {
				if strings.HasPrefix(strings.ToUpper(e), uk+"=") {
					out[j] = kv
					return
				}
			}
		}
		seen[uk] = true
		out = append(out, kv)
	}

	for _, e := range base {
		add(e)
	}
	for k, v := range extra {
		add(k + "=" + v)
	}
	if f.DenyNetwork {
		// force marker for child processes that honor it
		add("AI_CLOUDHUB_NETWORK=deny")
		add("NO_PROXY=*")
	}
	return out
}

// FilterOSEnviron is FilterEnv(os.Environ(), extra, f).
func FilterOSEnviron(extra map[string]string, f EnvFilter) []string {
	return FilterEnv(os.Environ(), extra, f)
}

func keyAllowed(key string, prefixes []string) bool {
	uk := strings.ToUpper(key)
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "_") {
			if strings.HasPrefix(uk, strings.ToUpper(p)) {
				return true
			}
			continue
		}
		// exact
		if uk == strings.ToUpper(p) {
			return true
		}
		// treat bare prefix as PREFIX_ as well
		if strings.HasPrefix(uk, strings.ToUpper(p)+"_") {
			return true
		}
	}
	return false
}
