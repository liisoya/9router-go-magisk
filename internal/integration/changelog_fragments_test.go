//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Changelog served to the dashboard must show work that has merged but not yet
// been released, which is what the .changes/ fragment is for: no PR touches
// CHANGELOG.md, so pending entries live in their own files and are spliced in at
// serve time. A regression here makes every merged-but-untagged fix invisible.
func TestChangelogShowsPendingFragments(t *testing.T) {
	env := newEnv(t)

	// HandleChangelog resolves .changes/ relative to the working directory, and
	// `go test` runs with the package directory, so walk up to the repo root.
	root := repoRoot(t)
	fragment := filepath.Join(root, ".changes", "99900-integration-probe.md")
	if err := os.MkdirAll(filepath.Dir(fragment), 0o755); err != nil {
		t.Fatalf("mkdir .changes: %v", err)
	}
	if err := os.WriteFile(fragment, []byte("### 🐛 INTEGRATION PROBE ENTRY\n\n- detail\n"), 0o644); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(fragment) })

	res := env.Get(t, "/api/changelog")
	if res.Status != 200 {
		t.Fatalf("GET /api/changelog = %d, want 200", res.Status)
	}

	body := string(res.Body)
	if !strings.Contains(body, "INTEGRATION PROBE ENTRY") {
		t.Errorf("a pending .changes/ fragment did not reach the dashboard, so "+
			"merged-but-unreleased work is invisible:\n%s", head(body))
	}
	// Splice the pending entries in above the released history rather than in
	// place of it. Matched on the section shape, not on a specific version, so
	// this does not need editing at every release.
	if !strings.Contains(body, "\n## [v") {
		t.Errorf("released sections were dropped when fragments were spliced in:\n%s", head(body))
	}
	if !strings.HasPrefix(body, "# Changelog") {
		t.Errorf("changelog lost its title:\n%s", head(body))
	}
	// The contributor instructions sit in the same directory as the entries.
	if strings.Contains(body, "How to write one") {
		t.Errorf(".changes/README.md was rendered as a changelog entry:\n%s", head(body))
	}
}

// repoRoot walks up from the test's working directory to the module root, which
// is where CHANGELOG.md and .changes/ live.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above the working directory")
		}
		dir = parent
	}
}

func head(s string) string {
	if len(s) > 1500 {
		return s[:1500] + "\n…"
	}
	return s
}
