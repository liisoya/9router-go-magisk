package service

import (
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// TestLaunchdPlistRoundTripsThroughXMLParser checks the plist is well-formed and
// that args with &, <, > and quotes decode back byte for byte.
func TestLaunchdPlistRoundTripsThroughXMLParser(t *testing.T) {
	exe := "/Applications/9router-go"
	args := []string{"--rtk=true", "--name=A & B <c> \"d\""}
	dict, version := parseLaunchdPlist(t, renderLaunchdPlist(exe, args))

	if version != "1.0" {
		t.Errorf("plist version = %q, want 1.0", version)
	}
	if dict["Label"] != launchdLabel {
		t.Errorf("Label = %v, want %q", dict["Label"], launchdLabel)
	}
	if dict["RunAtLoad"] != true {
		t.Errorf("RunAtLoad = %v, want true", dict["RunAtLoad"])
	}
	if dict["KeepAlive"] != true {
		t.Errorf("KeepAlive = %v, want true", dict["KeepAlive"])
	}
	got, ok := dict["ProgramArguments"].([]string)
	if !ok {
		t.Fatalf("ProgramArguments = %T, want a string array", dict["ProgramArguments"])
	}
	if want := append([]string{exe}, args...); !slices.Equal(got, want) {
		t.Errorf("ProgramArguments = %q, want %q (escaping must round-trip)", got, want)
	}
}

// TestLaunchdPlistAcceptedByValidator runs plutil on macOS or xmllint elsewhere;
// skipped where neither exists.
func TestLaunchdPlistAcceptedByValidator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.plist")
	plist := renderLaunchdPlist("/Applications/9router-go", []string{"--rtk=true", "--name=A & B"})
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}

	if plutil, err := exec.LookPath("plutil"); err == nil {
		if out, err := exec.Command(plutil, "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("plutil -lint rejected the rendered plist: %v\n%s", err, out)
		}
		return
	}
	if xmllint, err := exec.LookPath("xmllint"); err == nil {
		// --nonet keeps libxml2 from fetching the Apple DTD over the network.
		if out, err := exec.Command(xmllint, "--noout", "--nonet", path).CombinedOutput(); err != nil {
			t.Fatalf("xmllint rejected the rendered plist: %v\n%s", err, out)
		}
		return
	}
	t.Skip("neither plutil nor xmllint is installed")
}

func parseLaunchdPlist(t *testing.T, data string) (map[string]any, string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(data))
	root := launchdPlistNextStart(t, dec)
	if root.Name.Local != "plist" {
		t.Fatalf("plist root element = <%s>, want <plist>", root.Name.Local)
	}
	version := ""
	for _, attr := range root.Attr {
		if attr.Name.Local == "version" {
			version = attr.Value
		}
	}
	dict, ok := decodeLaunchdPlistValue(t, dec, launchdPlistNextStart(t, dec)).(map[string]any)
	if !ok {
		t.Fatal("plist root is not a dict")
	}
	return dict, version
}

func decodeLaunchdPlistValue(t *testing.T, dec *xml.Decoder, start xml.StartElement) any {
	t.Helper()
	switch start.Name.Local {
	case "dict":
		dict := map[string]any{}
		for {
			tok, err := dec.Token()
			if err != nil {
				t.Fatalf("plist dict: %v", err)
			}
			switch el := tok.(type) {
			case xml.StartElement:
				if el.Name.Local != "key" {
					t.Fatalf("plist dict: <%s>, want <key>", el.Name.Local)
				}
				key := strings.TrimSpace(readLaunchdPlistText(t, dec, "key"))
				dict[key] = decodeLaunchdPlistValue(t, dec, launchdPlistNextStart(t, dec))
			case xml.EndElement:
				return dict
			}
		}
	case "array":
		var array []string
		for {
			tok, err := dec.Token()
			if err != nil {
				t.Fatalf("plist array: %v", err)
			}
			switch el := tok.(type) {
			case xml.StartElement:
				value := decodeLaunchdPlistValue(t, dec, el)
				text, ok := value.(string)
				if !ok {
					t.Fatalf("plist array element = %T, want string", value)
				}
				array = append(array, text)
			case xml.EndElement:
				return array
			}
		}
	case "string":
		return readLaunchdPlistText(t, dec, "string")
	case "true":
		_ = dec.Skip()
		return true
	case "false":
		_ = dec.Skip()
		return false
	}
	t.Fatalf("plist: unexpected element <%s>", start.Name.Local)
	return nil
}

func readLaunchdPlistText(t *testing.T, dec *xml.Decoder, name string) string {
	t.Helper()
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("plist <%s>: %v", name, err)
		}
		switch el := tok.(type) {
		case xml.CharData:
			b.Write(el)
		case xml.EndElement:
			return b.String()
		}
	}
}

func launchdPlistNextStart(t *testing.T, dec *xml.Decoder) xml.StartElement {
	t.Helper()
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("plist: %v", err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start
		}
	}
}
