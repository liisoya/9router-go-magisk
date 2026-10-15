package service

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// hostStub describes the OS facts Detect and the Windows guard read. Zero
// values are meaningful: an empty bins/dirs map means nothing was found.
type hostStub struct {
	goos string
	euid int
	bins map[string]string
	dirs map[string]bool
}

// stubHost swaps the package seams for the duration of the test, so every OS,
// privilege, and init branch can be exercised from one runner.
func stubHost(t *testing.T, h hostStub) {
	t.Helper()
	previousGoos, previousEuid := goos, geteuid
	previousLook, previousIsDir := lookPath, isDir
	goos = h.goos
	geteuid = func() int { return h.euid }
	lookPath = func(name string) string { return h.bins[name] }
	isDir = func(path string) bool { return h.dirs[path] }
	t.Cleanup(func() {
		goos, geteuid, lookPath, isDir = previousGoos, previousEuid, previousLook, previousIsDir
	})
}

// stubRunFunc records supervisor invocations and answers them with fn. A nil fn
// answers every call with empty output and no error, matching stubRun.
func stubRunFunc(t *testing.T, fn func(name string, args ...string) (string, error)) *[]svcCall {
	t.Helper()
	previous := run
	calls := &[]svcCall{}
	run = func(name string, args ...string) (string, error) {
		*calls = append(*calls, svcCall{name: name, args: slices.Clone(args)})
		if fn == nil {
			return "", nil
		}
		return fn(name, args...)
	}
	t.Cleanup(func() { run = previous })
	return calls
}

// redirectInitPaths points the root-owned OpenRC and runit locations at one
// temp tree, so those backends' root branches can be tested without touching
// the real /etc or /var.
func redirectInitPaths(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	previousOpenRC := openRCServicePath
	previousRunitDir, previousRunitLink := runitSystemDir, runitSystemLink
	openRCServicePath = filepath.Join(root, "etc", "init.d", "9router-go")
	runitSystemDir = filepath.Join(root, "etc", "sv", "9router-go")
	runitSystemLink = filepath.Join(root, "var", "service", "9router-go")
	t.Cleanup(func() {
		openRCServicePath = previousOpenRC
		runitSystemDir, runitSystemLink = previousRunitDir, previousRunitLink
	})
	return root
}

// stubCrontab swaps the crontab readers for an in-memory one. It returns the
// last content written so tests can assert what would reach the user's crontab.
func stubCrontab(t *testing.T, initial string) *string {
	t.Helper()
	previousRead, previousWrite := readCrontab, writeCrontab
	content := initial
	written := &content
	readCrontab = func() string { return *written }
	writeCrontab = func(next string) error {
		*written = next
		return nil
	}
	t.Cleanup(func() { readCrontab, writeCrontab = previousRead, previousWrite })
	return written
}

// setHome points the user-scoped paths (systemd user unit, launchd plist,
// autostart desktop, runit ~/service) at a fresh temp home. It swaps the
// userHomeDir seam rather than HOME: os.UserHomeDir reads %USERPROFILE% on
// Windows, so a HOME-only override resolves to the runner's real home there
// and the tests would write into it.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	stubUserHomeDir(t, home, nil)
	return home
}

// stubNoHome simulates a machine with no resolvable home directory, which the
// user-scoped backends must reject rather than fall back to a real path.
func stubNoHome(t *testing.T) {
	t.Helper()
	stubUserHomeDir(t, "", errors.New("no home directory"))
}

// stubUserHomeDir swaps the home-directory seam. A non-nil err simulates a
// machine where the home cannot be resolved.
func stubUserHomeDir(t *testing.T, home string, err error) {
	t.Helper()
	previous := userHomeDir
	userHomeDir = func() (string, error) { return home, err }
	t.Cleanup(func() { userHomeDir = previous })
}

// stubWindowsTaskUser pins the account the Windows scheduled task is created
// for, so tests do not depend on the runner's USERNAME.
func stubWindowsTaskUser(t *testing.T, user string) {
	t.Helper()
	previous := windowsTaskUser
	windowsTaskUser = func() string { return user }
	t.Cleanup(func() { windowsTaskUser = previous })
}

// linkStub records the symlink and removal calls the runit backend makes.
// It still performs them for real when the runner allows it, so assertions
// about the resulting filesystem keep working on Linux; on Windows, where
// creating a symlink needs SeCreateSymbolicLinkPrivilege, the real call is
// skipped and only the request is recorded. That keeps the runit activation
// path covered on every runner instead of skipped outright.
type linkStub struct {
	created []string
	removed []string
	links   bool
}

func stubLinks(t *testing.T) *linkStub {
	t.Helper()
	previousSymlink, previousRemove := symlink, remove
	stub := &linkStub{links: probeSymlink(t)}
	symlink = func(oldname, newname string) error {
		stub.created = append(stub.created, newname)
		if !stub.links {
			return nil
		}
		return previousSymlink(oldname, newname)
	}
	remove = func(path string) error {
		stub.removed = append(stub.removed, path)
		return previousRemove(path)
	}
	t.Cleanup(func() { symlink, remove = previousSymlink, previousRemove })
	return stub
}

// probeSymlink reports whether this runner can create a symlink, by trying it
// in a temp dir rather than assuming from the OS: developer mode, elevated
// shells and CI images differ.
func probeSymlink(t *testing.T) bool {
	t.Helper()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return os.Symlink(target, filepath.Join(t.TempDir(), "link")) == nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
