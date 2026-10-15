package service

import "testing"

// TestDetect covers every branch of backend selection: the Windows and macOS
// short-circuits, the override escape hatch (whose systemd aliases all resolve
// to the unprivileged user unit), the generic non-Linux fallback, and the full
// Linux init priority ladder — including OpenRC, which is detected wherever it
// is the host init and refused without root at install time.
func TestDetect(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		euid     int
		override string
		bins     []string
		dirs     []string
		want     Backend
	}{
		{
			name: "windows with schtasks picks the task scheduler",
			goos: "windows", bins: []string{"schtasks.exe"}, want: BackendWindowsTask,
		},
		{
			name: "windows without schtasks has no backend",
			goos: "windows", want: BackendNone,
		},
		{
			name: "darwin always picks launchd",
			goos: "darwin", want: BackendLaunchd,
		},
		{
			name: "override wins over the darwin short-circuit",
			goos: "darwin", override: "cron", bins: []string{"crontab"}, want: BackendCron,
		},
		{
			name: "override wins over the windows short-circuit",
			goos: "windows", override: "cron", bins: []string{"schtasks.exe", "crontab"}, want: BackendCron,
		},
		{
			name: "generic unix with cron picks cron",
			goos: "freebsd", bins: []string{"crontab"}, want: BackendCron,
		},
		{
			name: "generic unix without cron has no backend",
			goos: "freebsd", want: BackendNone,
		},
		{
			name: "linux systemd as root still picks the unprivileged user unit",
			goos: "linux", euid: 0, bins: []string{"systemctl"}, dirs: []string{"/run/systemd/system"}, want: BackendSystemdUser,
		},
		{
			name: "linux systemd as user picks the user unit",
			goos: "linux", euid: 1000, bins: []string{"systemctl"}, dirs: []string{"/run/systemd/system"}, want: BackendSystemdUser,
		},
		{
			name: "stray systemctl shim without /run/systemd/system falls through to openrc",
			goos: "linux", bins: []string{"systemctl", "openrc-run"}, want: BackendOpenRC,
		},
		{
			name: "linux openrc via rc-service",
			goos: "linux", bins: []string{"rc-service"}, want: BackendOpenRC,
		},
		{
			name: "openrc is detected without root too; install enforces the root requirement",
			goos: "linux", euid: 1000, bins: []string{"rc-service", "crontab"}, want: BackendOpenRC,
		},
		{
			name: "linux runit via sv",
			goos: "linux", bins: []string{"sv"}, want: BackendRunit,
		},
		{
			name: "linux runit via the /run/runit directory",
			goos: "linux", dirs: []string{"/run/runit"}, want: BackendRunit,
		},
		{
			name: "linux falls back to cron last",
			goos: "linux", bins: []string{"crontab"}, want: BackendCron,
		},
		{
			name: "linux with nothing installed has no backend",
			goos: "linux", want: BackendNone,
		},
		{
			name: "linux systemd outranks openrc, runit, and cron",
			goos: "linux", bins: []string{"systemctl", "openrc-run", "sv", "crontab"},
			dirs: []string{"/run/systemd/system"}, want: BackendSystemdUser,
		},
		{
			name: "openrc outranks runit and cron",
			goos: "linux", bins: []string{"openrc-run", "sv", "crontab"}, want: BackendOpenRC,
		},
		{
			name: "override forces systemd-user",
			goos: "linux", override: "systemd-user", want: BackendSystemdUser,
		},
		{
			name: "override forces launchd even on linux",
			goos: "linux", override: "launchd", want: BackendLaunchd,
		},
		{
			name: "override forces openrc",
			goos: "linux", override: "openrc", want: BackendOpenRC,
		},
		{
			name: "override forces runit",
			goos: "linux", override: "runit", want: BackendRunit,
		},
		{
			name: "override forces cron",
			goos: "linux", override: "cron", want: BackendCron,
		},
		{
			name: "generic override systemd as root stays on the user unit",
			goos: "linux", euid: 0, override: "systemd", want: BackendSystemdUser,
		},
		{
			name: "generic override systemd as user maps to the user unit",
			goos: "linux", euid: 1000, override: "systemd", want: BackendSystemdUser,
		},
		{
			name: "alias system stays on the user unit as root",
			goos: "linux", euid: 0, override: "system", want: BackendSystemdUser,
		},
		{
			name: "alias system behaves like systemd as user",
			goos: "linux", euid: 1000, override: "system", want: BackendSystemdUser,
		},
		{
			name: "unknown override is ignored and detection continues",
			goos: "linux", override: "nonsense", bins: []string{"crontab"}, want: BackendCron,
		},
		{
			name: "override is trimmed before matching",
			goos: "linux", override: "  cron  ", want: BackendCron,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(BackendOverrideEnv, tt.override)
			bins := map[string]string{}
			for _, b := range tt.bins {
				bins[b] = "/usr/bin/" + b
			}
			dirs := map[string]bool{}
			for _, d := range tt.dirs {
				dirs[d] = true
			}
			stubHost(t, hostStub{goos: tt.goos, euid: tt.euid, bins: bins, dirs: dirs})

			if got := Detect(); got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHasSystemdRequiresTheRuntimeDirectory(t *testing.T) {
	tests := []struct {
		name string
		bins []string
		dirs []string
		want bool
	}{
		{"systemctl plus the runtime dir is real systemd", []string{"systemctl"}, []string{"/run/systemd/system"}, true},
		{"a stray systemctl shim alone is not systemd", []string{"systemctl"}, nil, false},
		{"the runtime dir without systemctl is not usable", nil, []string{"/run/systemd/system"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bins := map[string]string{}
			for _, b := range tt.bins {
				bins[b] = "/usr/bin/" + b
			}
			dirs := map[string]bool{}
			for _, d := range tt.dirs {
				dirs[d] = true
			}
			stubHost(t, hostStub{goos: "linux", bins: bins, dirs: dirs})
			if got := hasSystemd(); got != tt.want {
				t.Errorf("hasSystemd() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasOpenRC(t *testing.T) {
	tests := []struct {
		bins []string
		want bool
	}{
		{[]string{"openrc-run"}, true},
		{[]string{"rc-service"}, true},
		{[]string{"rc-update"}, false},
		{nil, false},
	}
	for _, tt := range tests {
		t.Run("bins="+joinStrings(tt.bins), func(t *testing.T) {
			bins := map[string]string{}
			for _, b := range tt.bins {
				bins[b] = "/sbin/" + b
			}
			stubHost(t, hostStub{goos: "linux", bins: bins})
			if got := hasOpenRC(); got != tt.want {
				t.Errorf("hasOpenRC() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasRunit(t *testing.T) {
	tests := []struct {
		name string
		bins []string
		dirs []string
		want bool
	}{
		{"sv present", []string{"sv"}, nil, true},
		{"runtime dir present", nil, []string{"/run/runit"}, true},
		{"neither", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bins := map[string]string{}
			for _, b := range tt.bins {
				bins[b] = "/usr/bin/" + b
			}
			dirs := map[string]bool{}
			for _, d := range tt.dirs {
				dirs[d] = true
			}
			stubHost(t, hostStub{goos: "linux", bins: bins, dirs: dirs})
			if got := hasRunit(); got != tt.want {
				t.Errorf("hasRunit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasCron(t *testing.T) {
	stubHost(t, hostStub{goos: "linux", bins: map[string]string{"crontab": "/usr/bin/crontab"}})
	if !hasCron() {
		t.Error("hasCron() = false with crontab on PATH")
	}
	stubHost(t, hostStub{goos: "linux"})
	if hasCron() {
		t.Error("hasCron() = true without crontab on PATH")
	}
}

func joinStrings(in []string) string {
	if len(in) == 0 {
		return "none"
	}
	out := in[0]
	for _, s := range in[1:] {
		out += "+" + s
	}
	return out
}
