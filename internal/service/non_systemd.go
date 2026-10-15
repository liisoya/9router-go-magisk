package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"9router/proxy/internal/constants"
)

// openRCServicePath is the service script location on OpenRC hosts. It is a
// var so tests can redirect the system-owned path onto a temp dir.
var openRCServicePath = "/etc/init.d/9router-go"

// runitSystemDir and runitSystemLink are the root-owned runit locations: the
// supervised service directory and its /var/service activation symlink. They
// are vars so tests can exercise the root branch without privileges.
var (
	runitSystemDir  = "/etc/sv/9router-go"
	runitSystemLink = "/var/service/9router-go"
)

// renderOpenRCScript renders the OpenRC service script. It supervises the
// gateway directly instead of forking a `9router-go start` daemon, so there
// is exactly one supervisor and one pid file owner.
func renderOpenRCScript(exe string, args []string) string {
	return "#!/sbin/openrc-run\n" +
		"# 9router-go gateway — managed by `9router-go service install/uninstall`.\n" +
		"description=\"9router-go AI API proxy gateway\"\n" +
		"command=" + quoteArg(exe) + "\n" +
		"command_args=\"" + openRCArgs(args) + "\"\n" +
		"command_background=\"yes\"\n" +
		"pidfile=\"/run/9router-go.pid\"\n" +
		"depend() {\n" +
		"\tneed net\n" +
		"\tafter firewall\n" +
		"}\n"
}

func openRCArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, quoteArg(a))
	}
	return strings.Join(quoted, " ")
}

func installOpenRC(exe string, args []string) (Info, error) {
	if geteuid() != 0 {
		// OpenRC is a system service with no per-user scope: it genuinely
		// needs root. Refuse rather than silently installing a different
		// backend, and point at the unprivileged alternative.
		return Info{}, fmt.Errorf("service.Install: openrc is a system service and needs root — re-run with sudo, or use a user-level backend with --backend cron")
	}
	if err := os.WriteFile(openRCServicePath, []byte(renderOpenRCScript(exe, args)), 0755); err != nil {
		return Info{}, fmt.Errorf("service.Install: write %s: %w", openRCServicePath, err)
	}
	if _, err := run("rc-update", "add", "9router-go", "default"); err != nil {
		return Info{Backend: BackendOpenRC, Path: openRCServicePath, Exec: commandLine(exe, args)}, err
	}
	info := Info{Backend: BackendOpenRC, Path: openRCServicePath, Exec: commandLine(exe, args), Enabled: true}
	if _, err := run("rc-service", "9router-go", "start"); err != nil {
		return info, nil
	}
	info.Active = true
	return info, nil
}

func uninstallOpenRC() (Info, bool) {
	if _, err := os.Stat(openRCServicePath); err != nil {
		return Info{}, false
	}
	_, _ = run("rc-service", "9router-go", "stop")
	_, _ = run("rc-update", "del", "9router-go", "default")
	// Removal is the source of truth: never report success when the script
	// survived (e.g. uninstall run without root).
	if err := os.Remove(openRCServicePath); err != nil {
		return Info{}, false
	}
	return Info{Backend: BackendOpenRC, Path: openRCServicePath}, true
}

func statusOpenRC() (Info, bool) {
	if _, err := os.Stat(openRCServicePath); err != nil {
		return Info{}, false
	}
	out, _ := run("rc-service", "9router-go", "status")
	active := strings.Contains(out, "started")
	return Info{Backend: BackendOpenRC, Path: openRCServicePath, Enabled: true, Active: active}, true
}

// runitServiceDir resolves the service directory: the system tree when root,
// a ~/service tree otherwise (supervised by the user's own runsvdir).
func runitServiceDir() string {
	if geteuid() == 0 {
		return runitSystemDir
	}
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "service", "9router-go")
}

// runitTarget is the argument sv takes: the service name under the system
// supervisor, the service directory under a user's own runsvdir.
func runitTarget() string {
	if geteuid() == 0 {
		return "9router-go"
	}
	return runitServiceDir()
}

// renderRunitRun renders the runit run script, which must exec the supervised
// process in the foreground.
func renderRunitRun(exe string, args []string) string {
	return "#!/bin/sh\n" +
		"# 9router-go gateway — managed by `9router-go service install/uninstall`.\n" +
		"exec " + commandLine(exe, args) + "\n"
}

func installRunit(exe string, args []string) (Info, error) {
	dir := runitServiceDir()
	if dir == "" {
		return Info{}, fmt.Errorf("service.Install: cannot resolve home directory for runit service")
	}
	if err := os.MkdirAll(dir, constants.FilePermDir); err != nil {
		return Info{}, fmt.Errorf("service.Install: create service dir: %w", err)
	}
	runPath := filepath.Join(dir, "run")
	if err := os.WriteFile(runPath, []byte(renderRunitRun(exe, args)), 0755); err != nil {
		return Info{}, fmt.Errorf("service.Install: write %s: %w", runPath, err)
	}
	info := Info{Backend: BackendRunit, Path: runPath, Exec: commandLine(exe, args), Enabled: true}
	if geteuid() == 0 {
		_ = symlink(dir, runitSystemLink)
		if _, err := run("sv", "start", "9router-go"); err == nil {
			info.Active = true
		}
		return info, nil
	}
	if _, err := run("sv", "start", dir); err == nil {
		info.Active = true
	}
	return info, nil
}

func uninstallRunit() (Info, bool) {
	dir := runitServiceDir()
	if dir == "" {
		return Info{}, false
	}
	runPath := filepath.Join(dir, "run")
	if _, err := os.Stat(runPath); err != nil {
		return Info{}, false
	}
	if geteuid() == 0 {
		_, _ = run("sv", "stop", "9router-go")
		_ = remove(runitSystemLink)
	} else {
		_, _ = run("sv", "stop", dir)
	}
	// The run script is the entry: keep the failure honest so Status still
	// reports the service instead of claiming a removal that never happened.
	if err := remove(runPath); err != nil {
		return Info{}, false
	}
	_ = remove(dir)
	return Info{Backend: BackendRunit, Path: runPath}, true
}

func statusRunit() (Info, bool) {
	dir := runitServiceDir()
	if dir == "" {
		return Info{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, "run")); err != nil {
		return Info{}, false
	}
	out, _ := run("sv", "status", dir)
	return Info{Backend: BackendRunit, Path: filepath.Join(dir, "run"), Enabled: true, Active: strings.Contains(out, "run:")}, true
}
