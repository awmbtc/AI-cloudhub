//go:build linux

package sandbox

import (
	"fmt"
	"strings"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
)

// Linux address families (socket domain arg0).
const (
	afUNIX  = 1  // AF_UNIX / AF_LOCAL
	afINET  = 2  // AF_INET
	afINET6 = 10 // AF_INET6
)

// runnerBaseAllowlist is shared by default/strict/netdeny: Go runtime + agent FS I/O.
//
// Dangerous calls (mount, ptrace, kexec, reboot, pivot_root, modules, bpf, …)
// are NOT listed → EPERM. NOT a production security audit.
var runnerBaseAllowlist = []string{
	// process / threads
	"brk", "arch_prctl", "prctl", "clone", "clone3", "fork", "vfork",
	"execve", "execveat", "exit", "exit_group", "wait4", "waitid",
	"getpid", "getppid", "gettid", "getuid", "geteuid", "getgid", "getegid",
	"getgroups",
	"setpgid", "getpgid", "setsid", "set_tid_address", "set_robust_list",
	"rseq", "sched_getaffinity", "sched_yield", "sched_getscheduler",
	"sched_getparam", "sched_setscheduler", "sched_setaffinity",
	"kill", "tgkill", "rt_sigaction", "rt_sigprocmask", "rt_sigreturn",
	"rt_sigpending", "rt_sigtimedwait", "rt_sigqueueinfo", "sigaltstack",
	// memory
	"mmap", "mprotect", "mremap", "munmap", "madvise", "mincore", "mlock",
	"munlock", "mlockall", "munlockall", "membarrier",
	// time
	"clock_gettime", "clock_getres", "clock_nanosleep", "nanosleep",
	"gettimeofday", "time", "timer_create", "timer_settime", "timer_gettime",
	"timer_delete", "timerfd_create", "timerfd_settime", "timerfd_gettime",
	// fs open / rw
	"open", "openat", "openat2", "close", "close_range", "read", "write",
	"readv", "writev", "pread64", "pwrite64", "preadv", "pwritev",
	"preadv2", "pwritev2", "lseek", "fadvise64", "fallocate",
	"pipe", "pipe2", "dup", "dup2", "dup3", "fcntl", "ioctl", "flock",
	"fsync", "fdatasync", "ftruncate", "truncate",
	"getdents", "getdents64", "getcwd", "chdir", "fchdir",
	"stat", "fstat", "lstat", "newfstatat", "fstatat", "statx", "statfs", "fstatfs",
	"access", "faccessat", "faccessat2",
	"readlink", "readlinkat", "symlink", "symlinkat",
	"unlink", "unlinkat", "rename", "renameat", "renameat2",
	"mkdir", "mkdirat", "rmdir",
	"chmod", "fchmod", "fchmodat",
	"umask", "utime", "utimes", "utimensat", "futimesat",
	"copy_file_range", "sendfile", "splice", "tee", "vmsplice",
	"getxattr", "fgetxattr", "lgetxattr", "listxattr", "flistxattr", "llistxattr",
	"setxattr", "fsetxattr", "lsetxattr", "removexattr", "fremovexattr", "lremovexattr",
	// epoll / poll / select / event
	"epoll_create", "epoll_create1", "epoll_ctl", "epoll_wait", "epoll_pwait",
	"epoll_pwait2", "poll", "ppoll", "select", "pselect6",
	"eventfd", "eventfd2", "signalfd", "signalfd4",
	// sockets (unrestricted domains unless netdeny — see apply)
	"socket", "socketpair", "bind", "listen", "accept", "accept4",
	"connect", "getsockname", "getpeername", "getsockopt", "setsockopt",
	"shutdown", "sendto", "sendmsg", "sendmmsg", "recvfrom", "recvmsg", "recvmmsg",
	// misc userspace
	"uname", "sysinfo", "times", "getrlimit", "setrlimit", "prlimit64", "getrusage",
	"getrandom", "capget", "futex", "getpriority", "setpriority",
	"memfd_create",
	"restart_syscall",
}

// runnerDefaultExtras are allowed only in profile=default (looser for legacy agents).
var runnerDefaultExtras = []string{
	"setgid", "setuid", "setresgid", "setresuid", "setgroups",
	"capset",
	"mknod", "mknodat",
	"chown", "fchown", "fchownat", "lchown",
}

// unrestrictedSocketNames are removed from the plain allowlist when NetDeny is on;
// they are re-added with AF_UNIX-only argument conditions.
var unrestrictedSocketNames = map[string]bool{
	"socket":     true,
	"socketpair": true,
}

// runnerAllowlist returns the unrestricted syscall names for the configured profile.
// When NetDeny, socket/socketpair are omitted here and attached with conditions.
func runnerAllowlist() []string {
	var base []string
	switch ProfileName() {
	case "strict", "netdeny":
		base = append([]string{}, runnerBaseAllowlist...)
	default:
		base = make([]string, 0, len(runnerBaseAllowlist)+len(runnerDefaultExtras))
		base = append(base, runnerBaseAllowlist...)
		base = append(base, runnerDefaultExtras...)
	}
	if !NetDeny() {
		return base
	}
	out := make([]string, 0, len(base))
	for _, n := range base {
		if unrestrictedSocketNames[n] {
			continue
		}
		out = append(out, n)
	}
	return out
}

// ApplyRunnerDefault installs a deny-by-default seccomp BPF filter on the
// current process (and all Go threads via TSYNC). The filter is inherited by
// child processes (agent command).
//
// Uses pure-Go github.com/elastic/go-seccomp-bpf (no CGO / libseccomp).
// Profile: AI_CLOUDHUB_SECCOMP_PROFILE=default|strict|netdeny
// Net: AI_CLOUDHUB_SECCOMP_NET=deny → socket/socketpair AF_UNIX only.
// See docs/SECCOMP.md.
func ApplyRunnerDefault() error {
	if !seccomp.Supported() {
		return fmt.Errorf("sandbox: seccomp not supported by this kernel")
	}

	info, err := arch.GetInfo("")
	if err != nil {
		return fmt.Errorf("sandbox: seccomp arch: %w", err)
	}

	names := filterKnownSyscalls(info, runnerAllowlist())
	if len(names) == 0 {
		return fmt.Errorf("sandbox: seccomp allowlist empty for arch %s", info.Name)
	}

	groups := []seccomp.SyscallGroup{
		{
			Action: seccomp.ActionAllow,
			Names:  names,
		},
	}

	if NetDeny() {
		// Allow socket/socketpair only when domain (arg0) == AF_UNIX.
		// AF_INET / AF_INET6 fall through to default ActionErrno.
		unixOnly := make([]seccomp.NameWithConditions, 0, 2)
		for _, name := range []string{"socket", "socketpair"} {
			if _, ok := info.SyscallNames[name]; !ok {
				continue
			}
			unixOnly = append(unixOnly, seccomp.NameWithConditions{
				Name: name,
				Conditions: seccomp.ArgumentConditions{
					{
						Argument:  0,
						Operation: seccomp.Equal,
						Value:     afUNIX,
					},
				},
			})
		}
		if len(unixOnly) > 0 {
			groups = append(groups, seccomp.SyscallGroup{
				Action:             seccomp.ActionAllow,
				NamesWithCondtions: unixOnly,
			})
		}
	}

	filter := seccomp.Filter{
		NoNewPrivs: true,
		Flag:       seccomp.FilterFlagTSync,
		Policy: seccomp.Policy{
			DefaultAction: seccomp.ActionErrno,
			Syscalls:      groups,
		},
	}
	if err := seccomp.LoadFilter(filter); err != nil {
		return fmt.Errorf("sandbox: load seccomp filter: %w", err)
	}
	return nil
}

func filterKnownSyscalls(info *arch.Info, names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		if _, ok := info.SyscallNames[n]; !ok {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// hasName reports whether name is in the list (test helper).
func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// Keep AF_INET* constants referenced (documented for netdeny filters).
var (
	_ = afINET
	_ = afINET6
	_ = strings.Builder{}
)
