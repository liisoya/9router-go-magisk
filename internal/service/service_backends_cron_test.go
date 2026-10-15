package service

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCronWritesEntryAndDesktopFile(t *testing.T) {
	home := setHome(t)
	crontab := stubCrontab(t, "")
	stubRun(t)

	info, err := installCron("/usr/local/bin/9router-go", []string{"--rtk=true"})
	if err != nil {
		t.Fatalf("installCron() error = %v", err)
	}
	if info.Backend != BackendCron || !info.Enabled {
		t.Errorf("Info = %+v, want enabled cron", info)
	}
	if !strings.Contains(*crontab, cronMarker) || !strings.Contains(*crontab, "@reboot") {
		t.Errorf("crontab = %q, want an @reboot entry carrying the marker", *crontab)
	}
	desktop := filepath.Join(home, ".config", "autostart", "9router-go.desktop")
	if !fileExists(desktop) {
		t.Fatalf("autostart desktop entry not written to %s", desktop)
	}
}

func TestInstallCronIsIdempotent(t *testing.T) {
	setHome(t)
	line := renderCronLine("/usr/local/bin/9router-go", nil)
	crontab := stubCrontab(t, "0 * * * * /usr/bin/backup\n"+line+"\n")
	stubRun(t)

	if _, err := installCron("/usr/local/bin/9router-go", nil); err != nil {
		t.Fatalf("installCron() error = %v", err)
	}
	if strings.Count(*crontab, cronMarker) != 1 {
		t.Errorf("crontab has %d owned lines, want exactly 1:\n%s", strings.Count(*crontab, cronMarker), *crontab)
	}
	if !strings.Contains(*crontab, "/usr/bin/backup") {
		t.Errorf("install dropped the user's own entry:\n%s", *crontab)
	}
}

func TestInstallCronWriteFailure(t *testing.T) {
	setHome(t)
	previous := writeCrontab
	writeCrontab = func(string) error { return errors.New("crontab denied") }
	t.Cleanup(func() { writeCrontab = previous })
	stubRun(t)

	if _, err := installCron("/usr/local/bin/9router-go", nil); err == nil {
		t.Fatal("installCron() error = nil when crontab write fails, want error")
	}
}

func TestStatusCron(t *testing.T) {
	t.Run("crontab owns an entry", func(t *testing.T) {
		setHome(t)
		stubCrontab(t, renderCronLine("/usr/local/bin/9router-go", nil)+"\n")
		stubRun(t)

		info, ok := statusCron()
		if !ok || info.Backend != BackendCron {
			t.Fatalf("statusCron() = %+v, %v; want cron", info, ok)
		}
		if info.Path != "crontab (@reboot)" {
			t.Errorf("Info.Path = %q, want the crontab marker", info.Path)
		}
	})

	t.Run("only the desktop entry is present", func(t *testing.T) {
		home := setHome(t)
		stubCrontab(t, "")
		stubRun(t)
		desktop := filepath.Join(home, ".config", "autostart", "9router-go.desktop")
		writeFile(t, desktop, renderAutostartDesktop("/usr/local/bin/9router-go", nil))

		info, ok := statusCron()
		if !ok || info.Path != desktop {
			t.Errorf("statusCron() = %+v, %v; want the desktop entry at %s", info, ok, desktop)
		}
	})

	t.Run("nothing installed", func(t *testing.T) {
		setHome(t)
		stubCrontab(t, "")
		stubRun(t)
		if _, ok := statusCron(); ok {
			t.Error("statusCron() ok = true with no entry, want false")
		}
	})
}

func TestUninstallCron(t *testing.T) {
	t.Run("removes the crontab entry and the desktop entry", func(t *testing.T) {
		home := setHome(t)
		crontab := stubCrontab(t, "0 * * * * /usr/bin/backup\n"+renderCronLine("/usr/local/bin/9router-go", nil)+"\n")
		stubRun(t)
		desktop := filepath.Join(home, ".config", "autostart", "9router-go.desktop")
		writeFile(t, desktop, renderAutostartDesktop("/usr/local/bin/9router-go", nil))

		if _, ok := uninstallCron(); !ok {
			t.Fatal("uninstallCron() = false, want true")
		}
		if strings.Contains(*crontab, cronMarker) {
			t.Errorf("crontab still owns an entry:\n%s", *crontab)
		}
		if !strings.Contains(*crontab, "/usr/bin/backup") {
			t.Errorf("uninstall dropped the user's own entry:\n%s", *crontab)
		}
		if fileExists(desktop) {
			t.Errorf("desktop entry still present at %s", desktop)
		}
	})

	t.Run("removes a foreign desktop file only when it carries the marker", func(t *testing.T) {
		home := setHome(t)
		stubCrontab(t, "")
		stubRun(t)
		desktop := filepath.Join(home, ".config", "autostart", "9router-go.desktop")
		writeFile(t, desktop, "[Desktop Entry]\nExec=someone-elses-app\n")

		if _, ok := uninstallCron(); ok {
			t.Fatal("uninstallCron() = true for a file this package does not own")
		}
		if !fileExists(desktop) {
			t.Error("uninstallCron() removed a desktop file it does not own")
		}
	})

	t.Run("nothing installed", func(t *testing.T) {
		setHome(t)
		stubCrontab(t, "")
		stubRun(t)
		if _, ok := uninstallCron(); ok {
			t.Error("uninstallCron() = true with no entry, want false")
		}
	})

	// The crontab entry is gone but the desktop entry survived, so the gateway
	// would still autostart at the next graphical login. Reporting success
	// there told the user something untrue; this pins the removal as the
	// source of truth, the rule launchd and OpenRC already follow.
	t.Run("a desktop entry that survives removal is not reported as uninstalled", func(t *testing.T) {
		home := setHome(t)
		stubCrontab(t, "")
		stubRun(t)
		desktop := filepath.Join(home, ".config", "autostart", "9router-go.desktop")
		writeFile(t, desktop, renderAutostartDesktop("/usr/local/bin/9router-go", nil))
		previousRemove := remove
		remove = func(string) error { return errors.New("permission denied") }
		t.Cleanup(func() { remove = previousRemove })

		if _, ok := uninstallCron(); ok {
			t.Error("uninstallCron() = true although the desktop entry survived")
		}
		if !fileExists(desktop) {
			t.Error("test setup: the desktop entry should still be present")
		}
	})
}
