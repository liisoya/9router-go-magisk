package service

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInstallSystemdUserWritesAndEnablesUnit(t *testing.T) {
	home := setHome(t)
	calls := stubRun(t)

	info, err := installSystemd("/usr/local/bin/9router-go", []string{"--rtk=true"})
	if err != nil {
		t.Fatalf("installSystemd() error = %v", err)
	}
	unit := filepath.Join(home, ".config", "systemd", "user", unitName)
	if !fileExists(unit) {
		t.Fatalf("user unit not written to %s", unit)
	}
	if info.Backend != BackendSystemdUser || !info.Enabled || !info.Active {
		t.Errorf("Info = %+v, want backend systemd-user, enabled and active", info)
	}
	if info.Path != unit {
		t.Errorf("Info.Path = %q, want %q", info.Path, unit)
	}
	data, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "WantedBy=default.target") {
		t.Errorf("user unit must target default.target:\n%s", data)
	}
	for _, c := range *calls {
		if c.name != "systemctl" {
			continue
		}
		if !slices.Contains(c.args, "--user") {
			t.Errorf("systemctl call %v missing --user on the user scope", c.args)
		}
	}
}

func TestInstallSystemdErrorPaths(t *testing.T) {
	tests := []struct {
		name       string
		failOn     string
		wantErr    bool
		wantActive bool
	}{
		{name: "daemon-reload failure aborts the install", failOn: "daemon-reload", wantErr: true},
		{name: "enable failure aborts the install", failOn: "enable", wantErr: true},
		{name: "start failure is best-effort and reports the polled state", failOn: "start", wantErr: false, wantActive: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setHome(t)
			stubRunFunc(t, func(name string, args ...string) (string, error) {
				for _, a := range args {
					if a == tt.failOn || (tt.failOn == "start" && a == "is-active") {
						return "", errors.New("boom")
					}
				}
				return "", nil
			})

			info, err := installSystemd("/usr/local/bin/9router-go", nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("installSystemd() err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if !info.Enabled {
					t.Errorf("Enabled = %v, want true (enable succeeded)", info.Enabled)
				}
				if info.Active != tt.wantActive {
					t.Errorf("Active = %v, want %v", info.Active, tt.wantActive)
				}
			}
		})
	}
}

func TestStatusSystemdUser(t *testing.T) {
	t.Run("user unit present and running", func(t *testing.T) {
		home := setHome(t)
		writeFile(t, filepath.Join(home, ".config", "systemd", "user", unitName), "[Unit]\n")
		stubRun(t)

		info, ok := statusSystemd()
		if !ok {
			t.Fatal("statusSystemd() ok = false, want true")
		}
		if info.Backend != BackendSystemdUser || !info.Enabled || !info.Active {
			t.Errorf("Info = %+v, want enabled+active systemd-user", info)
		}
	})

	t.Run("user unit present but disabled and inactive", func(t *testing.T) {
		home := setHome(t)
		writeFile(t, filepath.Join(home, ".config", "systemd", "user", unitName), "[Unit]\n")
		stubRunFunc(t, func(string, ...string) (string, error) { return "", errors.New("inactive") })

		info, ok := statusSystemd()
		if !ok {
			t.Fatal("statusSystemd() ok = false, want true")
		}
		if info.Enabled || info.Active {
			t.Errorf("Info = %+v, want disabled and inactive", info)
		}
	})
}

func TestControlSystemd(t *testing.T) {
	t.Run("start waits for the unit to become active", func(t *testing.T) {
		stubRun(t)
		if err := controlSystemd("start"); err != nil {
			t.Errorf("controlSystemd(start) error = %v", err)
		}
	})

	t.Run("stop waits for the unit to leave active", func(t *testing.T) {
		stubRunFunc(t, func(name string, args ...string) (string, error) {
			if slices.Contains(args, "is-active") {
				return "", errors.New("inactive")
			}
			return "", nil
		})
		if err := controlSystemd("stop"); err != nil {
			t.Errorf("controlSystemd(stop) error = %v", err)
		}
	})

	t.Run("unknown operation is rejected", func(t *testing.T) {
		stubRun(t)
		if err := controlSystemd("explode"); err == nil {
			t.Error("controlSystemd(explode) error = nil, want error")
		}
	})
}

// Linger is what keeps the unit alive after logout, and it was only reached
// when `systemctl start` succeeded — the early return on the failure path
// skipped it. A failed start is exactly the case install exists for: the
// gateway already holds the port from `9router-go start`, the unit still
// boots, and losing linger there killed it at logout while the CLI reported
// the entry as enabled.
func TestInstallSystemdEnablesLingerOnBothPaths(t *testing.T) {
	tests := []struct {
		name    string
		failOn  string
		wantRun bool
	}{
		{name: "start succeeds", wantRun: true},
		{name: "start fails", failOn: "start", wantRun: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setHome(t)
			t.Setenv("USER", "bima")
			calls := stubRunFunc(t, func(name string, args ...string) (string, error) {
				if name == "loginctl" {
					return "", nil
				}
				for _, a := range args {
					if a == tt.failOn {
						return "", errors.New("boom")
					}
				}
				return "", nil
			})

			if _, err := installSystemd("/usr/local/bin/9router-go", nil); err != nil {
				t.Fatalf("installSystemd() error = %v", err)
			}

			idx := -1
			for i, c := range *calls {
				if c.name == "loginctl" && slices.Contains(c.args, "enable-linger") {
					idx = i
				}
			}
			if tt.wantRun && idx < 0 {
				t.Errorf("calls = %v, want a loginctl enable-linger", *calls)
			}
		})
	}
}

func TestLogHint(t *testing.T) {
	if got := logHint(); !strings.Contains(got, "journalctl --user") {
		t.Errorf("logHint() = %q, want the user journal", got)
	}
}
