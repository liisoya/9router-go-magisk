package changelogfrag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo writes a temporary repository root holding the given files. Keys are
// slash-separated paths relative to the root, so a case can create both
// CHANGELOG.md and .changes/*.md without a fixture tree per case.
func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}

const released = "# Changelog\n\n## [Unreleased]\n\n## [v1.9.9] - 2026-10-01\n\n- initial\n"

// The split exists because every PR used to edit the same lines at the top of
// `[Unreleased]`, which is why two PRs landing together collided. These cases
// pin the property that actually replaced it: distinct fragments both survive.
func TestAssemble(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		wantParts []string
		absent    []string
		wantErr   bool
	}{
		{
			name: "fragments appear newest-first inside [Unreleased]",
			files: map[string]string{
				"CHANGELOG.md":           released,
				".changes/217-policy.md": "### policy fix",
				".changes/219-chat.md":   "### chat fix",
			},
			wantParts: []string{"### chat fix", "### policy fix", "## [v1.9.9] - 2026-10-01", "- initial"},
		},
		{
			// A lexicographic sort puts "1000-" before "88-" because '1' < '8',
			// which would silently reorder shipped work in the dashboard.
			name: "PR numbers order numerically, not lexicographically",
			files: map[string]string{
				"CHANGELOG.md":         released,
				".changes/88-small.md": "### entry 88",
				".changes/1000-big.md": "### entry 1000",
				".changes/219-mid.md":  "### entry 219",
			},
			wantParts: []string{"### entry 1000", "### entry 219", "### entry 88"},
		},
		{
			name: "a changelog with no fragments is served unchanged",
			files: map[string]string{
				"CHANGELOG.md": released,
			},
			wantParts: []string{"## [Unreleased]", "## [v1.9.9] - 2026-10-01", "- initial"},
		},
		{
			// Everything already folded into a release: there is no heading to
			// splice into, and dropping the fragments would hide shipped work.
			name: "a changelog with no [Unreleased] section still shows fragments",
			files: map[string]string{
				"CHANGELOG.md":         "# Changelog\n\n## [v1.9.9] - 2026-10-01\n\n- initial\n",
				".changes/219-chat.md": "### chat fix",
			},
			wantParts: []string{"## [Unreleased]", "### chat fix", "## [v1.9.9] - 2026-10-01"},
		},
		{
			// Before the first release CHANGELOG.md may not exist yet.
			name: "fragments alone are enough to serve the dashboard",
			files: map[string]string{
				".changes/219-chat.md": "### chat fix",
			},
			wantParts: []string{"## [Unreleased]", "### chat fix"},
		},
		{
			name: "non-markdown files and empty fragments are ignored",
			files: map[string]string{
				"CHANGELOG.md":            released,
				".changes/219-chat.md":    "### chat fix",
				".changes/NOTES.txt":      "scratch note, not a changelog entry",
				".changes/220-blank.md":   "\n\n   \n",
				".changes/archive/old.md": "### archived draft",
			},
			wantParts: []string{"### chat fix"},
		},
		{
			// `.changes/README.md` explains the workflow to contributors. It is a
			// sibling of the entries, not one, and rendering it would put
			// contributor instructions at the top of the changelog modal.
			name: "the directory README is not rendered as an entry",
			files: map[string]string{
				"CHANGELOG.md":         released,
				".changes/README.md":   "# Changelog fragments\n\nHow to write one.",
				".changes/219-chat.md": "### chat fix",
			},
			wantParts: []string{"### chat fix"},
			absent:    []string{"How to write one."},
		},
		{
			name:    "nothing at all reports an error so the caller falls back to remote",
			files:   map[string]string{},
			wantErr: true,
		},
		{
			// One unreadable file must not cost the reader the whole changelog.
			name: "a missing changelog with unreadable fragments still errors",
			files: map[string]string{
				".changes/": "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Assemble(newRepo(t, tt.files))

			if (got.Err != nil) != tt.wantErr {
				t.Fatalf("Err = %v, wantErr %v", got.Err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			// Order is the contract, so assert on position, not on presence.
			at := -1
			for _, part := range tt.wantParts {
				next := strings.Index(got.Markdown[max(at+1, 0):], part)
				if next < 0 {
					t.Fatalf("missing %q after offset %d in:\n%s", part, at, got.Markdown)
				}
				at += next + 1
			}
			for _, unwanted := range append(tt.absent, "scratch note") {
				if strings.Contains(got.Markdown, unwanted) {
					t.Errorf("%q was rendered into the changelog", unwanted)
				}
			}
		})
	}
}

// The dashboard shows pending work, so a fragment that was added must be
// visible — a silently dropped fragment reads to the operator as their merged
// work never having existed.
func TestAssembleReportsFragmentNames(t *testing.T) {
	root := newRepo(t, map[string]string{
		"CHANGELOG.md":           released,
		".changes/217-policy.md": "### policy fix",
		".changes/219-chat.md":   "### chat fix",
	})

	got := Assemble(root)

	want := []string{"219-chat.md", "217-policy.md"}
	if len(got.Fragments) != len(want) {
		t.Fatalf("Fragments = %v, want %v", got.Fragments, want)
	}
	for i := range want {
		if got.Fragments[i] != want[i] {
			t.Errorf("Fragments[%d] = %q, want %q", i, got.Fragments[i], want[i])
		}
	}
}

// A released heading must survive verbatim: release.yml extracts notes by
// awk-matching `## [<tag>]`, and the splice adds text right above that boundary.
func TestAssembleKeepsReleasedSectionHeadingIntact(t *testing.T) {
	root := newRepo(t, map[string]string{
		"CHANGELOG.md":         released,
		".changes/219-chat.md": "### chat fix",
	})

	got := Assemble(root)

	if strings.Count(got.Markdown, "## [v1.9.9] - 2026-10-01") != 1 {
		t.Errorf("released heading is not intact once, got:\n%s", got.Markdown)
	}
	if strings.Index(got.Markdown, "### chat fix") > strings.Index(got.Markdown, "## [v1.9.9]") {
		t.Errorf("fragment was placed after the released section, got:\n%s", got.Markdown)
	}
}

// The dashboard renders this through marked, and a fragment that starts with a
// heading directly after another fragment's list item is parsed as a lazy
// continuation line — the entry vanishes into the previous bullet. The blank
// line between fragments is the whole fix, so pin its absence.
func TestAssembleSeparatesFragmentsWithBlankLine(t *testing.T) {
	root := newRepo(t, map[string]string{
		"CHANGELOG.md":         released,
		".changes/219-chat.md": "### chat fix\n\n- detail\n",
		".changes/217-x.md":    "### policy fix\n\n- detail\n",
	})

	got := Assemble(root)

	if strings.Contains(got.Markdown, "- detail\n### ") {
		t.Errorf("fragments are not blank-line separated, a heading would be parsed as list continuation:\n%s", got.Markdown)
	}
	for _, body := range []string{"### chat fix", "### policy fix"} {
		at := strings.Index(got.Markdown, body)
		if at < 0 {
			t.Fatalf("missing %q in:\n%s", body, got.Markdown)
		}
		if got.Markdown[at-1] != '\n' || (at > 1 && got.Markdown[at-2] != '\n') {
			t.Errorf("%q is not preceded by a blank line:\n%s", body, got.Markdown)
		}
	}
}
