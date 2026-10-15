package service

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestTryEnableLinger covers the user-selection order: an install run through
// sudo must linger the invoking user, not root.
func TestTryEnableLinger(t *testing.T) {
	t.Run("prefers SUDO_USER", func(t *testing.T) {
		t.Setenv("SUDO_USER", "alice")
		t.Setenv("USER", "root")
		calls := stubRun(t)

		tryEnableLinger()
		if !hasCall(*calls, "loginctl", "alice") {
			t.Errorf("calls = %v, want loginctl enable-linger alice", *calls)
		}
	})

	t.Run("falls back to USER", func(t *testing.T) {
		t.Setenv("SUDO_USER", "")
		t.Setenv("USER", "bob")
		calls := stubRun(t)

		tryEnableLinger()
		if !hasCall(*calls, "loginctl", "bob") {
			t.Errorf("calls = %v, want loginctl enable-linger bob", *calls)
		}
	})

	t.Run("does nothing without a user", func(t *testing.T) {
		t.Setenv("SUDO_USER", "")
		t.Setenv("USER", "")
		calls := stubRun(t)

		tryEnableLinger()
		for _, c := range *calls {
			if c.name == "loginctl" {
				t.Errorf("unexpected %v call without a user", c)
			}
		}
	})
}

func TestHomeScopedPathsWithoutHome(t *testing.T) {
	stubNoHome(t)
	if got := userUnitPath(); got != "" {
		t.Errorf("userUnitPath() = %q, want empty without a home directory", got)
	}
	if got := autostartDesktopPath(); got != "" {
		t.Errorf("autostartDesktopPath() = %q, want empty without a home directory", got)
	}
}

func TestInstallLaunchdWithoutHome(t *testing.T) {
	stubNoHome(t)
	stubRun(t)
	if _, err := installLaunchd("/Applications/9router-go", nil); err == nil {
		t.Error("installLaunchd() error = nil without a home directory, want error")
	}
}

func TestInstallSystemdUserWithoutHome(t *testing.T) {
	stubNoHome(t)
	stubRun(t)
	if _, err := installSystemd("/usr/local/bin/9router-go", nil); err == nil {
		t.Error("installSystemd(user) error = nil without a home directory, want error")
	}
	if filepath.IsAbs(userUnitPath()) {
		t.Errorf("userUnitPath() = %q, want empty", userUnitPath())
	}
}

func TestInstallCronWithoutHomeStillWritesTheCrontab(t *testing.T) {
	stubNoHome(t)
	crontab := stubCrontab(t, "")
	stubRun(t)

	info, err := installCron("/usr/local/bin/9router-go", nil)
	if err != nil {
		t.Fatalf("installCron() error = %v", err)
	}
	if !info.Enabled {
		t.Errorf("Info.Enabled = false, want true (the crontab entry still installs)")
	}
	if !slices.Contains([]string{"crontab (@reboot)"}, info.Path) {
		t.Errorf("Info.Path = %q, want the crontab marker", info.Path)
	}
	if *crontab == "" {
		t.Error("installCron() wrote no crontab entry")
	}
}
