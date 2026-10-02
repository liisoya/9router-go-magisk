// Package proc holds the portable process primitives shared by everything in
// this repo that manages an external process: the headroom proxy and the
// 9router daemon itself.
//
// Every platform answers the same three questions — is this PID alive, ask it
// to stop, and force it to stop — so callers never branch on runtime.GOOS.
package proc

import (
	"os"
	"syscall"
)

// Alive reports whether pid belongs to a live process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return pidAlive(pid)
}

// Terminate asks pid to shut down gracefully and waits up to waitMS for it to
// exit, escalating to a forced kill when it overruns. It reports whether the
// process is gone once the call returns.
func Terminate(pid int, waitMS int) bool {
	if !Alive(pid) {
		return true
	}
	if err := requestStop(pid); err != nil {
		// The signal path is unavailable (Windows has no SIGTERM); force it.
		_ = ForceKill(pid)
		return waitExit(pid, waitMS)
	}
	if waitExit(pid, waitMS) {
		return true
	}
	_ = ForceKill(pid)
	return waitExit(pid, 2000)
}

// ForceKill terminates pid immediately, without a graceful drain.
func ForceKill(pid int) error {
	if pid <= 0 {
		return os.ErrInvalid
	}
	return forceKill(pid)
}

// SelfExecutable returns this binary's path with symlinks resolved, the exact
// file a respawn must exec.
func SelfExecutable() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := resolveLink(execPath)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// Detached returns the process attributes that let a child outlive the
// terminal that started it: a new session on POSIX, no console window on
// Windows.
func Detached() *syscall.SysProcAttr {
	return detached()
}
