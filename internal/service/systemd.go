package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"9router/proxy/internal/constants"
)

// unitName is the service name under the user manager.
const unitName = "9router-go.service"

// systemdScope is the systemctl prefix for the per-user manager. Every
// install/uninstall/status/control call goes through it: omitting --user sends
// the command to the system bus, which prompts for root and leaves the user
// unit untouched. This package only ever manages the unprivileged user scope.
func systemdScope() []string {
	return []string{"--user"}
}

func userUnitPath() string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "systemd", "user", unitName)
}

// renderSystemdUnit renders the user unit. DATA_DIR is baked in only when it
// is exported in the installing shell: at boot the supervisor's environment
// has no shell exports, while .env beside the binary is still found by the
// server itself, so only the shell-only value needs preserving.
func renderSystemdUnit(exe string, args []string) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=9router-go AI API proxy gateway\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("ExecStart=" + commandLine(exe, args) + "\n")
	if dir := filepath.Dir(exe); dir != "" {
		b.WriteString("WorkingDirectory=" + escapeSystemdPath(filepath.ToSlash(dir)) + "\n")
	}
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		b.WriteString("Environment=" + quoteArg("DATA_DIR="+dataDir) + "\n")
	}
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=3\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

// escapeSystemdPath renders the value of a systemd setting that takes a single
// path — WorkingDirectory=, the only one this package emits.
//
// It is deliberately not quoted. systemd parses that setting with
// config_parse_working_directory, which runs path_simplify_and_warn(
// PATH_CHECK_ABSOLUTE) on the whole remainder of the line: unlike ExecStart= it
// does not unquote, so a quoted path reaches the check with the quote attached
// and the unit is rejected —
//
//	WorkingDirectory= path is not absolute: "/usr/local/bin"
//
// Whitespace is not a separator here either, so a spaced path needs no quoting
// to survive. The one transformation that does apply is specifier expansion
// (unit_path_printf), so a literal '%' must be doubled.
func escapeSystemdPath(value string) string {
	return strings.ReplaceAll(value, "%", "%%")
}

// systemctl builds a systemctl invocation against the user manager. Every call
// must go through it so --user can never be dropped.
func systemctl(opArgs ...string) (string, error) {
	full := append(append([]string{}, systemdScope()...), opArgs...)
	return run("systemctl", full...)
}

func installSystemd(exe string, args []string) (Info, error) {
	path := userUnitPath()
	if path == "" {
		return Info{}, fmt.Errorf("service.Install: cannot resolve home directory for user unit")
	}
	if err := os.MkdirAll(filepath.Dir(path), constants.FilePermDir); err != nil {
		return Info{}, fmt.Errorf("service.Install: create unit dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderSystemdUnit(exe, args)), constants.FilePermFile); err != nil {
		return Info{}, fmt.Errorf("service.Install: write %s: %w", path, err)
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return Info{Backend: BackendSystemdUser, Path: path, Exec: commandLine(exe, args)}, err
	}
	if _, err := systemctl("enable", unitName); err != nil {
		return Info{Backend: BackendSystemdUser, Path: path, Exec: commandLine(exe, args)}, err
	}
	info := Info{Backend: BackendSystemdUser, Path: path, Exec: commandLine(exe, args), Enabled: true}
	// Starting now is best-effort: when the gateway already runs from
	// `9router-go start` the port is taken and the unit still starts on the
	// next boot, which is the point of the install. The active state is
	// polled rather than trusted from the start job: systemctl reports
	// success when the job is queued, even if the process then exits.
	if _, err := systemctl("start", unitName); err != nil {
		info.Active = systemctlActive()
		tryEnableLinger()
		return info, nil
	}
	info.Active = waitSystemdActive()
	tryEnableLinger()
	return info, nil
}

// waitSystemdActive polls until the unit is active or the budget runs out.
func waitSystemdActive() bool {
	deadline := time.Now().Add(3 * time.Second)
	for {
		if systemctlActive() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func uninstallSystemd() (Info, bool) {
	path := userUnitPath()
	if path == "" {
		return Info{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return Info{}, false
	}
	_, _ = systemctl("disable", "--now", unitName)
	// A failed removal must not report success: Status still shows the entry,
	// so the caller sees the truth instead of a removal that never happened.
	if err := os.Remove(path); err != nil {
		return Info{}, false
	}
	_, _ = systemctl("daemon-reload")
	// A Restart=always unit can leave a queued restart job behind the reload;
	// stopping and resetting it keeps no ghost behind.
	_, _ = systemctl("stop", unitName)
	_, _ = systemctl("reset-failed", unitName)
	return Info{Backend: BackendSystemdUser, Path: path}, true
}

func statusSystemd() (Info, bool) {
	path := userUnitPath()
	if path == "" {
		return Info{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return Info{}, false
	}
	return Info{
		Backend: BackendSystemdUser,
		Path:    path,
		Enabled: systemctlEnabled(),
		Active:  systemctlActive(),
	}, true
}

func systemctlEnabled() bool {
	_, err := systemctl("is-enabled", "--quiet", unitName)
	return err == nil
}

func systemctlActive() bool {
	_, err := systemctl("is-active", "--quiet", unitName)
	return err == nil
}

// tryEnableLinger keeps user units alive after logout. It only applies to the
// user scope and is best-effort: failures surface as a hint from the caller.
func tryEnableLinger() {
	if user := strings.TrimSpace(os.Getenv("SUDO_USER")); user != "" {
		_, _ = run("loginctl", "enable-linger", user)
		return
	}
	if user := strings.TrimSpace(os.Getenv("USER")); user != "" {
		_, _ = run("loginctl", "enable-linger", user)
	}
}
