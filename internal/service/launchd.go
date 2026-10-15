package service

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"9router/proxy/internal/constants"
)

// launchdLabel is the agent label and plist file name.
const launchdLabel = "com.9router.gateway"

func launchdPlistPath() string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

// renderLaunchdPlist renders the agent plist: ProgramArguments carries the
// executable and flags as separate elements, so no shell quoting applies.
func renderLaunchdPlist(exe string, args []string) string {
	programArgs := append([]string{exe}, args...)
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("\t<key>Label</key>\n\t<string>" + launchdLabel + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range programArgs {
		b.WriteString("\t\t<string>" + escapeXML(arg) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func installLaunchd(exe string, args []string) (Info, error) {
	path := launchdPlistPath()
	if path == "" {
		return Info{}, fmt.Errorf("service.Install: cannot resolve home directory for launchd agent")
	}
	if err := os.MkdirAll(filepath.Dir(path), constants.FilePermDir); err != nil {
		return Info{}, fmt.Errorf("service.Install: create LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(exe, args)), constants.FilePermFile); err != nil {
		return Info{}, fmt.Errorf("service.Install: write %s: %w", path, err)
	}
	// Unload-then-load keeps reinstalls idempotent across flag changes.
	_, _ = run("launchctl", "unload", "-w", path)
	if _, err := run("launchctl", "load", "-w", path); err != nil {
		return Info{Backend: BackendLaunchd, Path: path, Exec: commandLine(exe, args)}, err
	}
	return Info{Backend: BackendLaunchd, Path: path, Exec: commandLine(exe, args), Enabled: true, Active: true}, nil
}

func uninstallLaunchd() (Info, bool) {
	path := launchdPlistPath()
	if path == "" {
		return Info{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return Info{}, false
	}
	_, _ = run("launchctl", "unload", "-w", path)
	// Removal is the source of truth: an uninstall that cannot delete the
	// plist must not report success, so Status still shows the entry.
	if err := os.Remove(path); err != nil {
		return Info{}, false
	}
	return Info{Backend: BackendLaunchd, Path: path}, true
}

func statusLaunchd() (Info, bool) {
	path := launchdPlistPath()
	if path == "" {
		return Info{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return Info{}, false
	}
	out, _ := run("launchctl", "list", launchdLabel)
	return Info{Backend: BackendLaunchd, Path: path, Enabled: true, Active: strings.Contains(out, launchdLabel)}, true
}
