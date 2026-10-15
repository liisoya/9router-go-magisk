package changelogfrag

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Merge returns CHANGELOG.md with every pending fragment filed under a new
// `## [<tag>] - <date>` heading, placed above the current topmost release and
// below the file title.
//
// It does not touch the filesystem. The `[Unreleased]` section is renamed
// rather than duplicated: after a merge the pending entries *are* the release,
// and leaving an empty `[Unreleased]` above it would be a section that always
// exists and never contains anything.
func Merge(root, tag string, now time.Time) (string, error) {
	if strings.TrimSpace(tag) == "" {
		return "", errors.New("changelogfrag: tag is required")
	}
	if strings.ContainsAny(tag, "[]") {
		// The heading is later extracted by awk on /## \[<tag>\]/ in
		// release.yml; a bracket in the tag would break that match and the
		// release page would silently fall back to the commit log.
		return "", fmt.Errorf("changelogfrag: tag %q contains a bracket", tag)
	}

	frags := readFragments(root)
	if len(frags) == 0 {
		return "", errors.New("changelogfrag: no fragments to merge")
	}

	current, readErr := os.ReadFile(filepath.Join(root, MainFile))

	bodies := make([]string, 0, len(frags)+1)
	// Fragments first: they are the newer work, and this section has always been
	// read newest-first. The pre-split `[Unreleased]` body is older, so it lands
	// underneath — which is also where it appeared before the split.
	for _, f := range frags {
		bodies = append(bodies, f.body)
	}
	if unreleased, ok := unreleasedBody(string(current)); ok {
		// Anything already sitting under `[Unreleased]` ships with this release.
		// Dropping it would silently delete every entry written before the
		// fragment split — on this repository, 675 lines of history covering
		// the fixes behind several shipped versions.
		bodies = append(bodies, unreleased)
	}
	release := fmt.Sprintf("## [%s] - %s\n\n%s", tag, now.Format("2006-01-02"), strings.Join(bodies, "\n\n"))

	if readErr != nil {
		// A first release has no CHANGELOG.md yet; the fragments become it.
		return "# Changelog\n\n" + release + "\n", nil
	}

	return insertRelease(string(current), release), nil
}

// ApplyMerge writes the merged changelog and removes the fragments it consumed.
//
// The removal is deliberately not atomic with the write: a crash between the two
// leaves fragments already inlined, which the next merge would duplicate, while
// deleting first would lose the entries outright. Duplicated entries are
// recoverable; vanished release notes are not.
func ApplyMerge(root, tag string, now time.Time) error {
	merged, err := Merge(root, tag, now)
	if err != nil {
		return fmt.Errorf("changelogfrag.ApplyMerge: %w", err)
	}

	main := filepath.Join(root, MainFile)
	if err := os.WriteFile(main, []byte(merged), 0o644); err != nil {
		return fmt.Errorf("changelogfrag.ApplyMerge: write %s: %w", main, err)
	}

	for _, f := range readFragments(root) {
		path := filepath.Join(root, Dir, f.name)
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("changelogfrag.ApplyMerge: remove %s: %w", path, err)
		}
	}
	return nil
}

// insertRelease places a version section above the topmost existing one, so
// versions stay in descending order down the file. The `[Unreleased]` section it
// replaces is dropped: its content is now under the tag.
//
// After the first merge there is no `[Unreleased]` heading left, so every later
// merge lands in the no-heading branch — which is exactly why that branch has
// to preserve the `# Changelog` title. Prepending the release above it instead
// would leave the file starting with `## [v1.9.11]` and permanently drop the
// title the dashboard modal renders above everything else.
func insertRelease(changelog, release string) string {
	at := unreleasedHeadingIndex(changelog)
	if at < 0 {
		if end, ok := titleEnd(changelog); ok {
			return strings.TrimRight(changelog[:end], "\n") + "\n\n" + release + "\n\n" + strings.TrimLeft(changelog[end:], "\n")
		}
		return release + "\n\n" + strings.TrimLeft(changelog, "\n")
	}

	rest := changelog[at:]
	next := strings.Index(rest[len(unreleasedHeading):], "\n## ")
	if next < 0 {
		// Only `[Unreleased]` was present: the whole remainder is that section.
		return strings.TrimRight(changelog[:at], "\n") + "\n\n" + release + "\n"
	}
	boundary := at + len(unreleasedHeading) + next + 1
	return changelog[:at] + release + "\n\n" + changelog[boundary:]
}

// unreleasedBody returns the text currently under `[Unreleased]`, up to the
// next `## ` heading. It reports false when there is no such section or when it
// is empty, and either way the merge proceeds with just the fragments.
func unreleasedBody(changelog string) (string, bool) {
	at := unreleasedHeadingIndex(changelog)
	if at < 0 {
		return "", false
	}

	rest := changelog[at+len(unreleasedHeading):]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		rest = rest[:next]
	}
	body := strings.TrimSpace(rest)
	return body, body != ""
}

// unreleasedHeadingIndex returns the offset of the `## [Unreleased]` heading
// line, or -1. The match is line-anchored on purpose: entry bodies quote the
// marker when they describe the old shared-section workflow
// (e.g. "every PR wrote under `## [Unreleased]`"), and a substring search
// mistakes such a mention for the section itself — which once replaced the
// quoted text with a whole release mid-paragraph and left no version heading.
func unreleasedHeadingIndex(changelog string) int {
	offset := 0
	for _, line := range strings.SplitAfter(changelog, "\n") {
		if strings.TrimRight(line, "\r\n \t") == unreleasedHeading {
			return offset
		}
		offset += len(line)
	}
	return -1
}

// titleEnd reports the offset just past the changelog's H1 title. A changelog
// that opens without one is malformed but still servable, so the caller falls
// back to prepending rather than failing the release over a cosmetic defect.
func titleEnd(changelog string) (int, bool) {
	rest := strings.TrimLeft(changelog, "\n")
	line, offset := rest, 0
	if at := strings.Index(rest, "\n"); at >= 0 {
		line, offset = rest[:at], len(changelog)-len(rest)+at+1
	}
	if !strings.HasPrefix(line, "# ") {
		return 0, false
	}
	return offset, true
}
