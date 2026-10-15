package changelogfrag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var mergeTime = time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC)

// release.yml extracts the release page's notes with `awk "/## \[$TAG\]/"`, so a
// tag containing a bracket would miss that match and the page would silently
// fall back to raw commit subjects.
func TestMergeRejectsUnusableTag(t *testing.T) {
	tests := []struct {
		name string
		tag  string
	}{
		{name: "empty tag", tag: "   "},
		{name: "tag with an open bracket", tag: "v1.9.11[1]"},
		{name: "tag with a close bracket", tag: "v1.9.11]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t, map[string]string{
				"CHANGELOG.md":         released,
				".changes/219-chat.md": "### chat fix",
			})

			if _, err := Merge(root, tt.tag, mergeTime); err == nil {
				t.Fatalf("Merge(%q) succeeded, want error", tt.tag)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	frags := map[string]string{
		"CHANGELOG.md":         released,
		".changes/219-chat.md": "### chat fix",
		".changes/217-x.md":    "### policy fix",
	}

	t.Run("files the fragments under the tag, newest first", func(t *testing.T) {
		got, err := Merge(newRepo(t, frags), "v1.9.11", mergeTime)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}

		want := []string{
			"## [v1.9.11] - 2026-10-09",
			"### chat fix",
			"### policy fix",
			"## [v1.9.9] - 2026-10-01",
		}
		assertOrder(t, got, want)
	})

	t.Run("drops the emptied [Unreleased] section", func(t *testing.T) {
		// `[Unreleased]` that lingers above a release is a section that always
		// exists and never holds anything, which is worse than not having it.
		got, err := Merge(newRepo(t, frags), "v1.9.11", mergeTime)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}

		if strings.Contains(got, unreleasedHeading) {
			t.Errorf("[Unreleased] survived the merge, leaving a section that is always empty:\n%s", got)
		}
	})

	t.Run("creates the file when there is no changelog yet", func(t *testing.T) {
		root := newRepo(t, map[string]string{".changes/219-chat.md": "### chat fix"})

		got, err := Merge(root, "v1.9.11", mergeTime)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}

		if !strings.HasPrefix(got, "# Changelog\n") {
			t.Errorf("a first release must still produce a titled file, got:\n%s", got)
		}
		assertOrder(t, got, []string{"## [v1.9.11] - 2026-10-09", "### chat fix"})
	})

	t.Run("refuses to run with nothing pending", func(t *testing.T) {
		// Silently rewriting the file here would stamp a release heading with a
		// date covering work that was never described.
		if _, err := Merge(newRepo(t, map[string]string{"CHANGELOG.md": released}), "v1.9.11", mergeTime); err == nil {
			t.Fatal("Merge with no fragments succeeded, want error")
		}
	})

	// A merge removes the `[Unreleased]` heading, so the next release takes a
	// different branch. When that branch prepended above the file title instead
	// of below it, the second release silently destroyed the `# Changelog`
	// heading for every reader afterwards.
	t.Run("two releases in a row keep the title and stay in order", func(t *testing.T) {
		root := newRepo(t, frags)

		first, err := Merge(root, "v1.9.11", mergeTime)
		if err != nil {
			t.Fatalf("first Merge: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, MainFile), []byte(first), 0o644); err != nil {
			t.Fatalf("write changelog: %v", err)
		}
		if err := os.Remove(filepath.Join(root, Dir, "219-chat.md")); err != nil {
			t.Fatalf("remove fragment: %v", err)
		}

		second, err := Merge(root, "v1.9.12", mergeTime)
		if err != nil {
			t.Fatalf("second Merge: %v", err)
		}

		if !strings.HasPrefix(second, "# Changelog\n") {
			t.Errorf("the second release dropped the file title:\n%s", second)
		}
		assertOrder(t, second, []string{
			"# Changelog",
			"## [v1.9.12] - 2026-10-09",
			"## [v1.9.11] - 2026-10-09",
			"### policy fix",
			"## [v1.9.9] - 2026-10-01",
		})
	})

	// The repository's own `[Unreleased]` section held every entry written
	// before the split — 675 lines, covering shipped versions. A merge that
	// replaced that section with the fragments alone deleted all of it on the
	// first release, and the unit tests above missed it because their fixture
	// had an empty `[Unreleased]`.
	t.Run("entries already under [Unreleased] ship with the release", func(t *testing.T) {
		root := newRepo(t, map[string]string{
			"CHANGELOG.md":         "# Changelog\n\n## [Unreleased]\n\n### pre-split fix\n\n- detail\n\n## [v1.9.9] - 2026-10-01\n\n- initial\n",
			".changes/219-chat.md": "### chat fix",
		})

		got, err := Merge(root, "v1.9.11", mergeTime)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}

		assertOrder(t, got, []string{
			"## [v1.9.11] - 2026-10-09",
			"### chat fix",
			"### pre-split fix",
			"## [v1.9.9] - 2026-10-01",
		})
	})

	// Entry bodies quote the old marker when they describe the shared-section
	// workflow ("every PR wrote under `## [Unreleased]`"). A substring search
	// mistakes that mention for the section: the release landed mid-paragraph,
	// the quoted text was replaced, and no version heading was created.
	t.Run("a quoted [Unreleased] mention is not a section", func(t *testing.T) {
		root := newRepo(t, map[string]string{
			"CHANGELOG.md":         "# Changelog\n\n## [v1.9.9] - 2026-10-01\n\nEvery PR wrote under `## [Unreleased]` back then.\n",
			".changes/219-chat.md": "### chat fix",
		})

		got, err := Merge(root, "v1.9.11", mergeTime)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}

		assertOrder(t, got, []string{
			"# Changelog",
			"## [v1.9.11] - 2026-10-09",
			"### chat fix",
			"## [v1.9.9] - 2026-10-01",
			"Every PR wrote under `## [Unreleased]` back then.",
		})
	})
}

func TestApplyMerge(t *testing.T) {
	t.Run("consumes the fragments it inlined", func(t *testing.T) {
		root := newRepo(t, map[string]string{
			"CHANGELOG.md":         released,
			".changes/219-chat.md": "### chat fix",
		})

		if err := ApplyMerge(root, "v1.9.11", mergeTime); err != nil {
			t.Fatalf("ApplyMerge: %v", err)
		}

		// A fragment left behind would be inlined again by the next merge.
		entries, err := os.ReadDir(filepath.Join(root, Dir))
		if err != nil {
			t.Fatalf("read .changes: %v", err)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), FileSuffix) {
				t.Errorf("fragment %s survived the merge", e.Name())
			}
		}

		onDisk, err := os.ReadFile(filepath.Join(root, MainFile))
		if err != nil {
			t.Fatalf("read changelog: %v", err)
		}
		if !strings.Contains(string(onDisk), "### chat fix") {
			t.Errorf("entry was not written to the changelog:\n%s", onDisk)
		}
	})

	t.Run("leaves the changelog untouched when there is nothing to merge", func(t *testing.T) {
		root := newRepo(t, map[string]string{"CHANGELOG.md": released})
		before, err := os.ReadFile(filepath.Join(root, MainFile))
		if err != nil {
			t.Fatalf("read changelog: %v", err)
		}

		if err := ApplyMerge(root, "v1.9.11", mergeTime); err == nil {
			t.Fatal("ApplyMerge with no fragments succeeded, want error")
		}

		after, err := os.ReadFile(filepath.Join(root, MainFile))
		if err != nil {
			t.Fatalf("read changelog: %v", err)
		}
		if string(after) != string(before) {
			t.Errorf("a failed merge rewrote the changelog:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
}

// assertOrder walks want in sequence and fails on the first part that is missing
// or out of order. Position is the contract here: the file is read top to bottom
// and every failure above was a reordering, not a disappearance.
func assertOrder(t *testing.T, got string, want []string) {
	t.Helper()
	at := -1
	for _, part := range want {
		next := strings.Index(got[at+1:], part)
		if next < 0 {
			t.Fatalf("missing %q after offset %d in:\n%s", part, at, got)
		}
		at += next + 1
	}
}
