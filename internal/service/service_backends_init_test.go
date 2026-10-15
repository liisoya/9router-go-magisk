package service

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestInstallOpenRCNeedsRoot: OpenRC is a system service with no per-user
// scope, so a non-root install must fail with a clear hint rather than
// silently installing a different backend.
func TestInstallOpenRCNeedsRoot(t *testing.T) {
	stubHost(t, hostStub{goos: "linux", euid: 1000})
	crontab := stubCrontab(t, "")
	stubRun(t)

	if _, err := installOpenRC("/usr/local/bin/9router-go", nil); err == nil {
		t.Fatal("installOpenRC() error = nil without root, want the root requirement")
	}
	if *crontab != "" {
		t.Errorf("installOpenRC() wrote a crontab fallback, want to refuse: %q", *crontab)
	}
}

func TestInstallOpenRCAsRoot(t *testing.T) {
	redirectInitPaths(t)
	if err := os.MkdirAll(filepath.Dir(openRCServicePath), 0o755); err != nil {
		t.Fatal(err)
	}
	stubHost(t, hostStub{goos: "linux", euid: 0})
	calls := stubRun(t)

	info, err := installOpenRC("/usr/local/bin/9router-go", []string{"--rtk=true"})
	if err != nil {
		t.Fatalf("installOpenRC() error = %v", err)
	}
	if !fileExists(openRCServicePath) {
		t.Fatalf("openrc script not written to %s", openRCServicePath)
	}
	if info.Backend != BackendOpenRC || !info.Enabled || !info.Active {
		t.Errorf("Info = %+v, want openrc enabled and active", info)
	}
	var added, started bool
	for _, c := range *calls {
		if c.name == "rc-update" && slices.Contains(c.args, "add") {
			added = true
		}
		if c.name == "rc-service" && slices.Contains(c.args, "start") {
			started = true
		}
	}
	if !added || !started {
		t.Errorf("calls = %v, want rc-update add and rc-service start", *calls)
	}
}

func TestInstallOpenRCRootErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		failOn  string
		wantErr bool
	}{
		{name: "rc-update add failure aborts", failOn: "rc-update", wantErr: true},
		{name: "rc-service start failure is best-effort", failOn: "rc-service", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redirectInitPaths(t)
			if err := os.MkdirAll(filepath.Dir(openRCServicePath), 0o755); err != nil {
				t.Fatal(err)
			}
			stubHost(t, hostStub{goos: "linux", euid: 0})
			stubRunFunc(t, func(name string, args ...string) (string, error) {
				if name == tt.failOn {
					return "", errors.New("boom")
				}
				return "", nil
			})

			info, err := installOpenRC("/usr/local/bin/9router-go", nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("installOpenRC() err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && info.Active {
				t.Errorf("Info.Active = true after a failed start: %+v", info)
			}
		})
	}
}

func TestStatusOpenRC(t *testing.T) {
	t.Run("service script present and started", func(t *testing.T) {
		redirectInitPaths(t)
		writeFile(t, openRCServicePath, "#!/sbin/openrc-run\n")
		stubRunFunc(t, func(string, ...string) (string, error) { return "status: started", nil })

		info, ok := statusOpenRC()
		if !ok || info.Backend != BackendOpenRC || !info.Active {
			t.Errorf("statusOpenRC() = %+v, %v; want enabled+active openrc", info, ok)
		}
	})

	t.Run("service script present but stopped", func(t *testing.T) {
		redirectInitPaths(t)
		writeFile(t, openRCServicePath, "#!/sbin/openrc-run\n")
		stubRunFunc(t, func(string, ...string) (string, error) { return "status: stopped", nil })

		info, ok := statusOpenRC()
		if !ok || info.Active {
			t.Errorf("statusOpenRC() = %+v, %v; want present but inactive", info, ok)
		}
	})

	t.Run("no service script", func(t *testing.T) {
		redirectInitPaths(t)
		stubRun(t)
		if _, ok := statusOpenRC(); ok {
			t.Error("statusOpenRC() ok = true without a script, want false")
		}
	})
}

func TestUninstallOpenRC(t *testing.T) {
	t.Run("removes the script after stopping", func(t *testing.T) {
		redirectInitPaths(t)
		writeFile(t, openRCServicePath, "#!/sbin/openrc-run\n")
		calls := stubRun(t)

		if _, ok := uninstallOpenRC(); !ok {
			t.Fatal("uninstallOpenRC() = false, want true")
		}
		if fileExists(openRCServicePath) {
			t.Fatal("openrc script still present after uninstall")
		}
		var stopped, removed bool
		for _, c := range *calls {
			if c.name == "rc-service" && slices.Contains(c.args, "stop") {
				stopped = true
			}
			if c.name == "rc-update" && slices.Contains(c.args, "del") {
				removed = true
			}
		}
		if !stopped || !removed {
			t.Errorf("calls = %v, want rc-service stop and rc-update del", *calls)
		}
	})

	t.Run("nothing installed", func(t *testing.T) {
		redirectInitPaths(t)
		if _, ok := uninstallOpenRC(); ok {
			t.Error("uninstallOpenRC() = true with no script, want false")
		}
	})

	t.Run("removal failure is not reported as success", func(t *testing.T) {
		redirectInitPaths(t)
		// A non-empty directory at the path: Stat succeeds but Remove fails.
		if err := os.MkdirAll(filepath.Join(openRCServicePath, "keep"), 0o755); err != nil {
			t.Fatal(err)
		}
		stubRun(t)
		if _, ok := uninstallOpenRC(); ok {
			t.Error("uninstallOpenRC() = true when the script could not be removed, want false")
		}
	})
}

func TestControlOpenRC(t *testing.T) {
	calls := stubRun(t)
	if err := controlOpenRC("restart"); err != nil {
		t.Fatalf("controlOpenRC(restart) error = %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].name != "rc-service" || !slices.Contains((*calls)[0].args, "restart") {
		t.Errorf("calls = %v, want rc-service 9router-go restart", *calls)
	}
}

func TestRunitServiceDirAndTarget(t *testing.T) {
	t.Run("root uses the system tree", func(t *testing.T) {
		root := redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 0})
		if got := runitServiceDir(); got != runitSystemDir {
			t.Errorf("runitServiceDir() = %q, want %q", got, runitSystemDir)
		}
		if got := runitTarget(); got != "9router-go" {
			t.Errorf("runitTarget() = %q, want the service name", got)
		}
		if !filepath.IsAbs(root) {
			t.Errorf("temp root %q is not absolute", root)
		}
	})

	t.Run("user uses ~/service", func(t *testing.T) {
		home := setHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		want := filepath.Join(home, "service", "9router-go")
		if got := runitServiceDir(); got != want {
			t.Errorf("runitServiceDir() = %q, want %q", got, want)
		}
		if got := runitTarget(); got != want {
			t.Errorf("runitTarget() = %q, want %q", got, want)
		}
	})
}

func TestInstallRunitUser(t *testing.T) {
	home := setHome(t)
	stubHost(t, hostStub{goos: "linux", euid: 1000})
	calls := stubRun(t)

	info, err := installRunit("/usr/local/bin/9router-go", []string{"--rtk=true"})
	if err != nil {
		t.Fatalf("installRunit() error = %v", err)
	}
	run := filepath.Join(home, "service", "9router-go", "run")
	if !fileExists(run) {
		t.Fatalf("runit run script not written to %s", run)
	}
	if info.Backend != BackendRunit || !info.Enabled || !info.Active {
		t.Errorf("Info = %+v, want runit enabled and active", info)
	}
	if len(*calls) != 1 || (*calls)[0].name != "sv" || !slices.Contains((*calls)[0].args, "start") {
		t.Errorf("calls = %v, want sv start <dir>", *calls)
	}
}

func TestInstallRunitRootActivatesTheService(t *testing.T) {
	redirectInitPaths(t)
	if err := os.MkdirAll(filepath.Dir(runitSystemLink), 0o755); err != nil {
		t.Fatal(err)
	}
	links := stubLinks(t)
	stubHost(t, hostStub{goos: "linux", euid: 0})
	calls := stubRun(t)

	info, err := installRunit("/usr/local/bin/9router-go", nil)
	if err != nil {
		t.Fatalf("installRunit() error = %v", err)
	}
	if !fileExists(filepath.Join(runitSystemDir, "run")) {
		t.Fatalf("run script not written under %s", runitSystemDir)
	}
	// The activation symlink is what makes runsv pick the service up, so this
	// is the whole point of the root branch. It is asserted as a request
	// rather than a file: an unelevated Windows runner cannot create one, and
	// failing there left the path uncovered on every Windows machine.
	if len(links.created) != 1 || links.created[0] != runitSystemLink {
		t.Errorf("activation symlink requests = %v, want [%s]", links.created, runitSystemLink)
	}
	if links.links && !fileExists(runitSystemLink) {
		t.Errorf("activation symlink not created at %s", runitSystemLink)
	}
	if !info.Active {
		t.Errorf("Info.Active = false, want true when sv start succeeds")
	}
	if len(*calls) != 1 || !slices.Contains((*calls)[0].args, "9router-go") {
		t.Errorf("calls = %v, want sv start 9router-go", *calls)
	}
}

func TestInstallRunitErrorPaths(t *testing.T) {
	t.Run("start failure leaves the service enabled but inactive", func(t *testing.T) {
		setHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		stubRunFunc(t, func(string, ...string) (string, error) { return "", errors.New("boom") })

		info, err := installRunit("/usr/local/bin/9router-go", nil)
		if err != nil {
			t.Fatalf("installRunit() error = %v", err)
		}
		if !info.Enabled || info.Active {
			t.Errorf("Info = %+v, want enabled but inactive", info)
		}
	})

	t.Run("missing home is reported", func(t *testing.T) {
		stubNoHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		if _, err := installRunit("/usr/local/bin/9router-go", nil); err == nil {
			t.Error("installRunit() error = nil without a home directory")
		}
	})
}

func TestStatusRunit(t *testing.T) {
	t.Run("run script present and supervised", func(t *testing.T) {
		home := setHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		writeFile(t, filepath.Join(home, "service", "9router-go", "run"), "#!/bin/sh\n")
		stubRunFunc(t, func(string, ...string) (string, error) { return "run: 9router-go: (pid 1) 0s", nil })

		info, ok := statusRunit()
		if !ok || info.Backend != BackendRunit || !info.Active {
			t.Errorf("statusRunit() = %+v, %v; want enabled+active runit", info, ok)
		}
	})

	t.Run("run script present but down", func(t *testing.T) {
		home := setHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		writeFile(t, filepath.Join(home, "service", "9router-go", "run"), "#!/bin/sh\n")
		stubRunFunc(t, func(string, ...string) (string, error) { return "down: 9router-go: 1s, normally up", nil })

		info, ok := statusRunit()
		if !ok || info.Active {
			t.Errorf("statusRunit() = %+v, %v; want present but inactive", info, ok)
		}
	})

	t.Run("no run script", func(t *testing.T) {
		setHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		if _, ok := statusRunit(); ok {
			t.Error("statusRunit() ok = true without a run script, want false")
		}
	})

	t.Run("missing home", func(t *testing.T) {
		stubNoHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		if _, ok := statusRunit(); ok {
			t.Error("statusRunit() ok = true without a home directory, want false")
		}
	})
}

func TestUninstallRunit(t *testing.T) {
	t.Run("user removes the run script and its directory", func(t *testing.T) {
		home := setHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		dir := filepath.Join(home, "service", "9router-go")
		writeFile(t, filepath.Join(dir, "run"), "#!/bin/sh\n")
		calls := stubRun(t)

		if _, ok := uninstallRunit(); !ok {
			t.Fatal("uninstallRunit() = false, want true")
		}
		if fileExists(dir) {
			t.Fatal("runit service directory still present after uninstall")
		}
		if len(*calls) != 1 || !slices.Contains((*calls)[0].args, "stop") {
			t.Errorf("calls = %v, want sv stop <dir>", *calls)
		}
	})

	t.Run("root removes the activation symlink", func(t *testing.T) {
		redirectInitPaths(t)
		stubHost(t, hostStub{goos: "linux", euid: 0})
		links := stubLinks(t)
		writeFile(t, filepath.Join(runitSystemDir, "run"), "#!/bin/sh\n")
		if err := os.MkdirAll(filepath.Dir(runitSystemLink), 0o755); err != nil {
			t.Fatal(err)
		}
		// Provision the activation link through the seam rather than a bare
		// os.Symlink: creating one needs SeCreateSymbolicLinkPrivilege, so this
		// line failed on every unelevated Windows runner.
		if err := symlink(runitSystemDir, runitSystemLink); err != nil {
			t.Fatal(err)
		}
		stubRun(t)

		if _, ok := uninstallRunit(); !ok {
			t.Fatal("uninstallRunit() = false, want true")
		}
		// A leftover activation link keeps runsv starting the service, so the
		// uninstall is only honest once it is removed too.
		if !slices.Contains(links.removed, runitSystemLink) {
			t.Errorf("removal requests = %v, want %s", links.removed, runitSystemLink)
		}
		if links.links && fileExists(runitSystemLink) {
			t.Errorf("activation symlink still present at %s", runitSystemLink)
		}
	})

	t.Run("nothing installed", func(t *testing.T) {
		setHome(t)
		stubHost(t, hostStub{goos: "linux", euid: 1000})
		if _, ok := uninstallRunit(); ok {
			t.Error("uninstallRunit() = true with no run script, want false")
		}
	})
}

func TestControlRunit(t *testing.T) {
	home := setHome(t)
	stubHost(t, hostStub{goos: "linux", euid: 1000})
	calls := stubRun(t)

	if err := controlRunit("restart"); err != nil {
		t.Fatalf("controlRunit(restart) error = %v", err)
	}
	want := filepath.Join(home, "service", "9router-go")
	if len(*calls) != 1 || (*calls)[0].name != "sv" || !slices.Contains((*calls)[0].args, want) {
		t.Errorf("calls = %v, want sv restart %s", *calls, want)
	}
}
