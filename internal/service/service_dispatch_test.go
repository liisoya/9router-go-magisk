package service

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestInstallDispatchesToEveryBackend(t *testing.T) {
	tests := []struct {
		name     string
		host     hostStub
		override string
		wantBack Backend
		wantFile func(t *testing.T, home string) string
		wantErr  bool
	}{
		{
			name:    "no backend found is an explicit error",
			host:    hostStub{goos: "linux", euid: 1000},
			wantErr: true,
		},
		{
			name:     "systemd-user",
			host:     hostStub{goos: "linux", euid: 1000},
			override: string(BackendSystemdUser),
			wantBack: BackendSystemdUser,
			wantFile: func(_ *testing.T, home string) string {
				return filepath.Join(home, ".config", "systemd", "user", unitName)
			},
		},
		{
			name:     "launchd",
			host:     hostStub{goos: "darwin"},
			wantBack: BackendLaunchd,
			wantFile: func(_ *testing.T, home string) string {
				return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
			},
		},
		{
			name:     "openrc",
			host:     hostStub{goos: "linux", euid: 0},
			override: string(BackendOpenRC),
			wantBack: BackendOpenRC,
			wantFile: func(*testing.T, string) string { return openRCServicePath },
		},
		{
			name:     "runit",
			host:     hostStub{goos: "linux", euid: 1000},
			override: string(BackendRunit),
			wantBack: BackendRunit,
			wantFile: func(_ *testing.T, home string) string {
				return filepath.Join(home, "service", "9router-go", "run")
			},
		},
		{
			name:     "cron",
			host:     hostStub{goos: "linux", euid: 1000},
			override: string(BackendCron),
			wantBack: BackendCron,
			wantFile: func(_ *testing.T, home string) string {
				return filepath.Join(home, ".config", "autostart", "9router-go.desktop")
			},
		},
		{
			name:     "windows scheduled task",
			host:     hostStub{goos: "windows", bins: map[string]string{"schtasks.exe": `C:\Windows\System32\schtasks.exe`}},
			wantBack: BackendWindowsTask,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := setHome(t)
			redirectInitPaths(t)
			if err := os.MkdirAll(filepath.Dir(openRCServicePath), 0o755); err != nil {
				t.Fatal(err)
			}
			stubHost(t, tt.host)
			t.Setenv(BackendOverrideEnv, tt.override)
			stubCrontab(t, "")
			stubRun(t)

			info, err := Install([]string{"--rtk=true"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Install() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if info.Backend != tt.wantBack {
				t.Errorf("Install() backend = %q, want %q", info.Backend, tt.wantBack)
			}
			if tt.wantFile != nil {
				path := tt.wantFile(t, home)
				if !fileExists(path) {
					t.Errorf("Install() did not write %s", path)
				}
			}
		})
	}
}

func TestUninstallReportsWhenNothingIsInstalled(t *testing.T) {
	setHome(t)
	redirectInitPaths(t)
	stubHost(t, hostStub{goos: "linux", euid: 1000})
	stubCrontab(t, "")
	stubRun(t)

	if _, err := Uninstall(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall() error = %v, want ErrNotInstalled", err)
	}
}

func TestUninstallRemovesEveryBackendItFinds(t *testing.T) {
	home := setHome(t)
	redirectInitPaths(t)
	stubHost(t, hostStub{goos: "linux", euid: 1000})
	stubCrontab(t, renderCronLine("/usr/local/bin/9router-go", nil)+"\n")
	stubRun(t)

	writeFile(t, filepath.Join(home, ".config", "systemd", "user", unitName), "[Unit]\n")
	writeFile(t, filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), "<plist/>")

	removed, err := Uninstall()
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	got := map[Backend]bool{}
	for _, info := range removed {
		got[info.Backend] = true
	}
	for _, want := range []Backend{BackendSystemdUser, BackendLaunchd, BackendCron} {
		if !got[want] {
			t.Errorf("Uninstall() did not report removing %q: %v", want, removed)
		}
	}
}

func TestStatusReportsTheInstalledBackend(t *testing.T) {
	t.Run("systemd-user", func(t *testing.T) {
		home := setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		stubRun(t)
		writeFile(t, filepath.Join(home, ".config", "systemd", "user", unitName), "[Unit]\n")

		info, ok := Status()
		if !ok || info.Backend != BackendSystemdUser {
			t.Errorf("Status() = %+v, %v; want systemd-user", info, ok)
		}
	})

	t.Run("launchd", func(t *testing.T) {
		home := setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		stubRun(t)
		writeFile(t, filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), "<plist/>")

		info, ok := Status()
		if !ok || info.Backend != BackendLaunchd {
			t.Errorf("Status() = %+v, %v; want launchd", info, ok)
		}
	})

	t.Run("openrc", func(t *testing.T) {
		setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		stubRun(t)
		writeFile(t, openRCServicePath, "#!/sbin/openrc-run\n")

		info, ok := Status()
		if !ok || info.Backend != BackendOpenRC {
			t.Errorf("Status() = %+v, %v; want openrc", info, ok)
		}
	})

	t.Run("runit", func(t *testing.T) {
		home := setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		stubRun(t)
		writeFile(t, filepath.Join(home, "service", "9router-go", "run"), "#!/bin/sh\n")

		info, ok := Status()
		if !ok || info.Backend != BackendRunit {
			t.Errorf("Status() = %+v, %v; want runit", info, ok)
		}
	})

	t.Run("cron", func(t *testing.T) {
		setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, renderCronLine("/usr/local/bin/9router-go", nil)+"\n")
		stubRun(t)

		info, ok := Status()
		if !ok || info.Backend != BackendCron {
			t.Errorf("Status() = %+v, %v; want cron", info, ok)
		}
	})

	t.Run("windows task", func(t *testing.T) {
		setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "windows"})
		stubCrontab(t, "")
		stubRunFunc(t, func(name string, args ...string) (string, error) {
			if slices.Contains(args, "/query") {
				return windowsTaskName + "  Ready", nil
			}
			return "", nil
		})

		info, ok := Status()
		if !ok || info.Backend != BackendWindowsTask {
			t.Errorf("Status() = %+v, %v; want windows-task", info, ok)
		}
	})

	t.Run("nothing installed", func(t *testing.T) {
		setHome(t)
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubCrontab(t, "")
		stubRun(t)

		if info, ok := Status(); ok {
			t.Errorf("Status() = %+v, true; want false", info)
		}
	})
}
