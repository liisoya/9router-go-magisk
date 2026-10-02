//go:build windows

package proc

import (
	"errors"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is the pseudo exit code Windows reports for a process that has
// not exited yet.
const stillActive = 259

// ErrStopUnsupported reports that this platform has no graceful-stop signal.
// Windows has no SIGTERM and os.Process.Signal(syscall.SIGTERM) fails with
// "not supported by windows", so Terminate escalates straight to a kill.
var ErrStopUnsupported = errors.New("proc: graceful stop unsupported on this platform")

// pidAlive reports whether the PID is a running process.
//
// The exit code is checked because a process this package spawned and released
// without waiting on keeps its PID as a zombie and still reports STILL_ACTIVE
// until the handle is reused — reporting that as alive would make
// `9router-go stop` wait on a process that is already gone.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}

// requestStop always fails on Windows; see ErrStopUnsupported.
func requestStop(int) error {
	return ErrStopUnsupported
}

// forceKill is TerminateProcess.
func forceKill(pid int) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.TerminateProcess(handle, 1)
}

// resolveLink expands symlinks so a respawn execs the real file.
func resolveLink(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

// detached hides the console window. Go creates the child as a console
// subsystem binary, which pops a new window unless CREATE_NO_WINDOW is set.
func detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// waitExit polls until the PID is gone or the budget runs out.
func waitExit(pid int, waitMS int) bool {
	deadline := time.Now().Add(time.Duration(waitMS) * time.Millisecond)
	for {
		if !pidAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
