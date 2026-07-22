# Seccomp (BYOC runner)

In-process Linux seccomp for `cmd/runner`. **CGO-free** (`github.com/elastic/go-seccomp-bpf`). Non-Linux builds are no-ops.

Not a full security audit — deny-by-default skeleton for agent children after mount.

## Enable

```bash
# Soft: log and continue if load fails
AI_CLOUDHUB_SECCOMP=1 ./.bin/runner -- agent-cmd

# Fail runner if filter cannot load
AI_CLOUDHUB_SECCOMP=1 AI_CLOUDHUB_SECCOMP_STRICT=1 ./.bin/runner -- agent-cmd

# Stricter allowlist (drops setuid/mknod/chown/capset extras)
AI_CLOUDHUB_SECCOMP=1 AI_CLOUDHUB_SECCOMP_PROFILE=strict ./.bin/runner -- agent-cmd

# Block AF_INET / AF_INET6 socket() (AF_UNIX only) — arg-level filter
AI_CLOUDHUB_SECCOMP=1 AI_CLOUDHUB_SECCOMP_PROFILE=netdeny ./.bin/runner -- agent-cmd
# or overlay any profile:
AI_CLOUDHUB_SECCOMP=1 AI_CLOUDHUB_SECCOMP_NET=deny ./.bin/runner -- agent-cmd
```

Truthy env values: `1` / `true` / `yes`.

## Profiles (`AI_CLOUDHUB_SECCOMP_PROFILE`)

| Profile | When | Notes |
|---------|------|--------|
| `default` | unset / unknown | Base allowlist + setuid/setgid/capset/mknod/chown family (legacy agents) |
| `strict` | `strict` | Base only — no setuid*, capset, mknod*, chown* |
| `netdeny` | `netdeny` | **strict** syscall set + **socket/socketpair arg0 == AF_UNIX only** |

### Network deny (`AI_CLOUDHUB_SECCOMP_NET`)

| Value | Effect |
|-------|--------|
| `deny` / `1` / `true` / `yes` | Restrict `socket`/`socketpair` domain to **AF_UNIX (1)** |
| unset / other | Unrestricted socket domains (still no mount/ptrace/…) |

Can combine with `default` or `strict` → logged as e.g. `default+netdeny`.

**How it works:** unrestricted `socket`/`socketpair` are removed from the plain allowlist; a second rule allows them only when arg0 equals `AF_UNIX`. `AF_INET` / `AF_INET6` hit default `EPERM`.

This is stronger than env-only `AI_CLOUDHUB_NETWORK=deny` (proxy strip) and pairs with `runner-netns.sh` / bwrap.

## Apply timing

Runner applies the filter **after** env filter, path jail, and mount setup, **immediately before** `exec` of the agent. Mount/rclone setup is not under the filter; children inherit it.

`no_new_privs` is set — setuid helpers after apply will not gain privilege.

## External wrappers (optional)

`scripts/runner-seccomp.sh` still supports:

1. Precompiled BPF + `bwrap --seccomp`
2. firejail
3. docker `--security-opt seccomp=scripts/seccomp/runner-default.json`

Prefer in-process `AI_CLOUDHUB_SECCOMP=1` when the runner binary can load filters.

## Limits

- `netdeny` blocks creating IP sockets; does not inspect `connect()` of pre-existing FDs.
- Not a substitute for path jail + env filter + agent scopes.
- D-001: user-host BYOC only; no platform mega runner pool.
