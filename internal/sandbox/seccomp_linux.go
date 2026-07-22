//go:build linux

package sandbox

import (
	"fmt"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
)

// runnerBaseAllowlist is shared by default and strict profiles: Go runtime +
// typical agent child processes (I/O, mmap, futex, sockets, process control).
//
// Dangerous calls such as mount, kexec_load, reboot, ptrace, pivot_root,
// init_module, bpf (kernel), etc. are NOT listed and return EPERM.
//
// Names unknown on the current architecture are skipped at apply time so the
// same list works on amd64 and arm64. This is NOT a production security audit.
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
	// sockets (agent tooling; soft network deny is separate env policy)
	"socket", "socketpair", "bind", "listen", "accept", "accept4",
	"connect", "getsockname", "getpeername", "getsockopt", "setsockopt",
	"shutdown", "sendto", "sendmsg", "sendmmsg", "recvfrom", "recvmsg", "recvmmsg",
	// misc userspace
	"uname", "sysinfo", "times", "getrlimit", "setrlimit", "prlimit64", "getrusage",
	"getrandom", "capget", "futex", "getpriority", "setpriority",
	// memfd (common in modern tooling)
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

// runnerAllowlist returns the syscall names for the configured profile.
func runnerAllowlist() []string {
	if ProfileName() == "strict" {
		return append([]string{}, runnerBaseAllowlist...)
	}
	out := make([]string, 0, len(runnerBaseAllowlist)+len(runnerDefaultExtras))
	out = append(out, runnerBaseAllowlist...)
	out = append(out, runnerDefaultExtras...)
	return out
}

// ApplyRunnerDefault installs a deny-by-default seccomp BPF filter on the
// current process (and all Go threads via TSYNC). The filter is inherited by
// child processes (agent command).
//
// Uses pure-Go github.com/elastic/go-seccomp-bpf (no CGO / libseccomp).
// Sets no_new_privs, which prevents later privilege gains (e.g. setuid
// fusermount). Prefer applying immediately before the agent command so mount
// setup is unaffected; cleanup umount may fail after apply — already best-effort.
//
// Profile: AI_CLOUDHUB_SECCOMP_PROFILE=default|strict (see docs/SECCOMP.md).
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

	filter := seccomp.Filter{
		NoNewPrivs: true,
		Flag:       seccomp.FilterFlagTSync,
		Policy: seccomp.Policy{
			DefaultAction: seccomp.ActionErrno, // EPERM for non-allowlisted
			Syscalls: []seccomp.SyscallGroup{
				{
					Action: seccomp.ActionAllow,
					Names:  names,
				},
			},
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
