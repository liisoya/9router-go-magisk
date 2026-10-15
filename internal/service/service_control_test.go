package service

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestControlDispatchesToTheInstalledBackend(t *testing.T) {
	t.Run("nothing installed", func(t *testing.T) {
		setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		stubRun(t)

		if _, err := Start(); !errors.Is(err, ErrNotInstalled) {
			t.Errorf("Start() error = %v, want ErrNotInstalled", err)
		}
	})

	t.Run("cron has no supervisor", func(t *testing.T) {
		setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, renderCronLine("/usr/local/bin/9router-go", nil)+"\n")
		stubRun(t)

		if _, err := Start(); !errors.Is(err, ErrNoSupervisor) {
			t.Errorf("Start() error = %v, want ErrNoSupervisor", err)
		}
	})

	t.Run("systemd-user", func(t *testing.T) {
		home := setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		calls := stubRun(t)
		writeFile(t, filepath.Join(home, ".config", "systemd", "user", unitName), "[Unit]\n")

		if _, err := Restart(); err != nil {
			t.Fatalf("Restart() error = %v", err)
		}
		if !hasCall(*calls, "systemctl", "restart") {
			t.Errorf("calls = %v, want systemctl restart", *calls)
		}
	})

	t.Run("launchd", func(t *testing.T) {
		home := setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		calls := stubRun(t)
		writeFile(t, filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), "<plist/>")

		if _, err := Stop(); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
		if !hasCall(*calls, "launchctl", "unload") {
			t.Errorf("calls = %v, want launchctl unload", *calls)
		}
	})

	t.Run("openrc", func(t *testing.T) {
		setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		calls := stubRun(t)
		writeFile(t, openRCServicePath, "#!/sbin/openrc-run\n")

		if _, err := Start(); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if !hasCall(*calls, "rc-service", "start") {
			t.Errorf("calls = %v, want rc-service start", *calls)
		}
	})

	t.Run("runit", func(t *testing.T) {
		home := setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		calls := stubRun(t)
		writeFile(t, filepath.Join(home, "service", "9router-go", "run"), "#!/bin/sh\n")

		if _, err := Start(); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if !hasCall(*calls, "sv", "start") {
			t.Errorf("calls = %v, want sv start", *calls)
		}
	})

	t.Run("windows task", func(t *testing.T) {
		setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "windows"})
		stubCrontab(t, "")
		calls := stubRunFunc(t, func(name string, args ...string) (string, error) {
			if slices.Contains(args, "/query") {
				return windowsTaskName + "  Ready", nil
			}
			return "", nil
		})

		if _, err := Start(); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if !hasCall(*calls, "schtasks.exe", "/run") {
			t.Errorf("calls = %v, want schtasks /run", *calls)
		}
	})
}

func hasCall(calls []svcCall, name, flag string) bool {
	for _, c := range calls {
		if c.name == name && slices.Contains(c.args, flag) {
			return true
		}
	}
	return false
}

func TestCommandLineQuotesEveryPart(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		args []string
		want string
	}{
		{"plain", "/usr/local/bin/9router-go", []string{"--rtk=true"}, "/usr/local/bin/9router-go --rtk=true"},
		{"spaced executable", "/opt/my apps/9router-go", nil, `"/opt/my apps/9router-go"`},
		{"empty arg", "/bin/9router-go", []string{""}, "/bin/9router-go ''"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandLine(tt.exe, tt.args); got != tt.want {
				t.Errorf("commandLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEscapeXML(t *testing.T) {
	tests := []struct{ in, want string }{
		{`a&b`, "a&amp;b"},
		{`<tag>`, "&lt;tag&gt;"},
		{`"quoted"`, "&quot;quoted&quot;"},
		{"plain", "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := escapeXML(tt.in); got != tt.want {
				t.Errorf("escapeXML(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestOpenRCArgs(t *testing.T) {
	got := openRCArgs([]string{"--rtk=true", "/opt/my apps"})
	if !strings.Contains(got, "--rtk=true") || !strings.Contains(got, `"/opt/my apps"`) {
		t.Errorf("openRCArgs() = %q, want each arg quoted", got)
	}
}
