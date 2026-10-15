package service

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func TestInstallLaunchdWritesPlistAndLoads(t *testing.T) {
	home := setHome(t)
	calls := stubRun(t)

	info, err := installLaunchd("/Applications/9router-go", []string{"--rtk=true"})
	if err != nil {
		t.Fatalf("installLaunchd() error = %v", err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	if !fileExists(plist) {
		t.Fatalf("plist not written to %s", plist)
	}
	if info.Backend != BackendLaunchd || !info.Enabled || !info.Active {
		t.Errorf("Info = %+v, want launchd enabled and active", info)
	}
	// Reinstalls must unload the old job before loading the new one.
	if len(*calls) != 2 {
		t.Fatalf("launchctl calls = %v, want unload then load", *calls)
	}
	if !slices.Contains((*calls)[0].args, "unload") || !slices.Contains((*calls)[1].args, "load") {
		t.Errorf("calls = %v, want unload before load", *calls)
	}
}

func TestInstallLaunchdLoadFailure(t *testing.T) {
	setHome(t)
	stubRunFunc(t, func(name string, args ...string) (string, error) {
		if slices.Contains(args, "load") {
			return "load failed", errors.New("boom")
		}
		return "", nil
	})

	info, err := installLaunchd("/Applications/9router-go", nil)
	if err == nil {
		t.Fatal("installLaunchd() error = nil, want error when load fails")
	}
	if info.Enabled {
		t.Errorf("Info.Enabled = true after a failed load: %+v", info)
	}
}

func TestLaunchdPlistPathNeedsHome(t *testing.T) {
	stubNoHome(t)
	if got := launchdPlistPath(); got != "" {
		t.Errorf("launchdPlistPath() = %q, want empty without a home directory", got)
	}
}

func TestStatusLaunchd(t *testing.T) {
	t.Run("plist present and job listed", func(t *testing.T) {
		home := setHome(t)
		writeFile(t, filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), "<plist/>")
		stubRunFunc(t, func(string, ...string) (string, error) { return launchdLabel + "\t0\tcom.9router.gateway", nil })

		info, ok := statusLaunchd()
		if !ok {
			t.Fatal("statusLaunchd() ok = false, want true")
		}
		if !info.Enabled || !info.Active {
			t.Errorf("Info = %+v, want enabled and active", info)
		}
	})

	t.Run("plist present but the job is not listed", func(t *testing.T) {
		home := setHome(t)
		writeFile(t, filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), "<plist/>")
		stubRunFunc(t, func(string, ...string) (string, error) { return "other job", errors.New("not found") })

		info, ok := statusLaunchd()
		if !ok {
			t.Fatal("statusLaunchd() ok = false, want true")
		}
		if info.Active {
			t.Errorf("Info.Active = true, want false when the job is not listed")
		}
	})

	t.Run("no plist and no home", func(t *testing.T) {
		setHome(t)
		stubRun(t)
		if _, ok := statusLaunchd(); ok {
			t.Error("statusLaunchd() ok = true without a plist, want false")
		}
		stubNoHome(t)
		if _, ok := statusLaunchd(); ok {
			t.Error("statusLaunchd() ok = true without a home directory, want false")
		}
	})
}

func TestControlLaunchd(t *testing.T) {
	t.Run("start loads the plist", func(t *testing.T) {
		setHome(t)
		calls := stubRun(t)
		if err := controlLaunchd("start"); err != nil {
			t.Fatalf("controlLaunchd(start) error = %v", err)
		}
		if len(*calls) != 1 || !slices.Contains((*calls)[0].args, "load") {
			t.Errorf("calls = %v, want a single launchctl load", *calls)
		}
	})

	t.Run("start kicks an already loaded job", func(t *testing.T) {
		setHome(t)
		calls := stubRunFunc(t, func(name string, args ...string) (string, error) {
			if slices.Contains(args, "load") {
				return "service already loaded", errors.New("already loaded")
			}
			return "", nil
		})
		if err := controlLaunchd("start"); err != nil {
			t.Fatalf("controlLaunchd(start) error = %v", err)
		}
		if len(*calls) != 2 || !slices.Contains((*calls)[1].args, "start") {
			t.Errorf("calls = %v, want a fallback launchctl start", *calls)
		}
	})

	t.Run("stop unloads without disabling", func(t *testing.T) {
		setHome(t)
		calls := stubRun(t)
		if err := controlLaunchd("stop"); err != nil {
			t.Fatalf("controlLaunchd(stop) error = %v", err)
		}
		if len(*calls) != 1 || !slices.Contains((*calls)[0].args, "unload") {
			t.Errorf("calls = %v, want launchctl unload", *calls)
		}
		if slices.Contains((*calls)[0].args, "-w") {
			t.Errorf("stop must not disable the job: %v", (*calls)[0].args)
		}
	})

	t.Run("restart unloads then loads", func(t *testing.T) {
		setHome(t)
		calls := stubRun(t)
		if err := controlLaunchd("restart"); err != nil {
			t.Fatalf("controlLaunchd(restart) error = %v", err)
		}
		if len(*calls) != 2 || !slices.Contains((*calls)[0].args, "unload") || !slices.Contains((*calls)[1].args, "load") {
			t.Errorf("calls = %v, want unload then load", *calls)
		}
	})

	t.Run("unknown operation is rejected", func(t *testing.T) {
		setHome(t)
		stubRun(t)
		if err := controlLaunchd("explode"); err == nil {
			t.Error("controlLaunchd(explode) error = nil, want error")
		}
	})

	t.Run("missing home is reported", func(t *testing.T) {
		stubNoHome(t)
		stubRun(t)
		if err := controlLaunchd("start"); err == nil {
			t.Error("controlLaunchd(start) error = nil without a home directory")
		}
	})
}
