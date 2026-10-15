// Package changelogfrag assembles the changelog the dashboard renders from a
// released CHANGELOG.md plus the per-PR fragments in .changes/.
//
// Every pull request used to prepend its entry at the top of `## [Unreleased]`.
// That made lines 5-6 the hottest hunk in the repository, so any two PRs landing
// in the same window collided — and this repo merges through GitHub's web merge
// button, which ignores .gitattributes merge drivers entirely, so there was no
// setting that could have merged them automatically. A fragment per PR is a new
// unique file, so two concurrent PRs never share a line and cannot conflict.
//
// The fragments are served live rather than only folded in at release time:
// CHANGELOG.md alone would leave the dashboard showing whatever shipped last,
// which is strictly worse than what it shows today.
package changelogfrag

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// Dir is the fragment directory, relative to the repository root.
	Dir = ".changes"

	// MainFile is the released changelog, relative to the repository root.
	MainFile = "CHANGELOG.md"

	unreleasedHeading = "## [Unreleased]"

	// FileSuffix is what a fragment's name must end in to be rendered.
	FileSuffix = ".md"
)

// Result is the assembled changelog plus the fragments that went into it.
type Result struct {
	// Markdown is CHANGELOG.md with the fragments spliced into the
	// `[Unreleased]` section, ready to hand to the dashboard.
	Markdown string
	// Fragments lists the fragment file names, in the order they were spliced.
	Fragments []string
	// Err is set only when nothing could be read at all, so a caller can fall
	// back to the remote copy. A partial read is not an error: a changelog
	// missing one pending entry still beats failing the whole modal.
	Err error
}

type fragment struct {
	name string
	body string
}

// Assemble reads the released changelog and the pending fragments under root,
// and returns whatever it could read. A missing CHANGELOG.md with fragments
// present is a valid state, and so is a changelog with no fragments at all.
func Assemble(root string) Result {
	frags := readFragments(root)
	names := make([]string, len(frags))
	bodies := make([]string, len(frags))
	for i, f := range frags {
		names[i] = f.name
		bodies[i] = f.body
	}

	main, _ := os.ReadFile(filepath.Join(root, MainFile))
	switch {
	case len(strings.TrimSpace(string(main))) > 0:
		return Result{
			Markdown:  splice(string(main), bodies),
			Fragments: names,
		}
	case len(bodies) > 0:
		return Result{
			Markdown:  splice("", bodies),
			Fragments: names,
		}
	default:
		return Result{Err: errors.New("changelogfrag: no changelog or fragments found")}
	}
}

// readFragments loads every fragment, newest first.
//
// "Newest first" matches how CHANGELOG.md has always been read, and it is why
// the sort is on names and not on timestamps: fragments are named
// `<pr-number>-<slug>.md`, and a PR number orders them exactly the way they
// were merged. mtimes would reorder the whole list on any fresh clone or
// rebase, which would rewrite released history in the dashboard.
//
// A fragment that cannot be read, or that is empty, is skipped rather than
// fatal. One bad file must not take the changelog down for everyone who still
// has a healthy CHANGELOG.md.
func readFragments(root string) []fragment {
	entries, err := os.ReadDir(filepath.Join(root, Dir))
	if err != nil {
		return nil
	}

	collected := make([]fragment, 0, len(entries))
	for _, e := range entries {
		if !isFragment(e) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, Dir, e.Name()))
		if err != nil {
			continue
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			continue
		}
		collected = append(collected, fragment{name: e.Name(), body: body})
	}

	// Descending, because a higher PR number was written later and the
	// changelog has always been read newest-first: 219 goes above 217.
	sort.SliceStable(collected, func(i, j int) bool {
		return naturalLess(collected[j].name, collected[i].name)
	})
	return collected
}

// isFragment reports whether a directory entry is a changelog entry rather than
// documentation about the entries. `.changes/README.md` lives in the same
// directory and explains the workflow; rendering it into the dashboard would
// put contributor instructions above the release notes in the changelog modal.
func isFragment(e os.DirEntry) bool {
	if e.IsDir() || !strings.HasSuffix(e.Name(), FileSuffix) {
		return false
	}
	return !strings.EqualFold(strings.TrimSuffix(e.Name(), FileSuffix), "README")
}

// naturalLess compares two fragment names by digit runs as numbers, so
// `219-a.md` sorts after `88-b.md` the way it reads. A lexicographic sort puts
// `1000-` before `88-` because '1' < '8', and once PR numbers outgrow three
// digits that interleaves unrelated releases — the kind of quiet wrongness
// that makes a changelog untrustworthy.
func naturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		aDigit, bDigit := isDigit(a[ai]), isDigit(b[bi])
		if aDigit && bDigit {
			aStart, bStart := ai, bi
			for ai < len(a) && isDigit(a[ai]) {
				ai++
			}
			for bi < len(b) && isDigit(b[bi]) {
				bi++
			}
			aNum := strings.TrimLeft(a[aStart:ai], "0")
			bNum := strings.TrimLeft(b[bStart:bi], "0")
			if len(aNum) != len(bNum) {
				return len(aNum) < len(bNum)
			}
			if aNum != bNum {
				return aNum < bNum
			}
			continue
		}
		if a[ai] != b[bi] {
			return a[ai] < b[bi]
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// splice inserts the fragments directly beneath the `[Unreleased]` heading so
// they read as the newest work, leaving every released section untouched.
//
// A changelog with no `[Unreleased]` section — everything already folded into a
// release — gets the fragments prepended instead. Dropping them would silently
// hide shipped work, which is the one failure mode this split was meant to
// avoid.
func splice(changelog string, frags []string) string {
	if len(frags) == 0 {
		return changelog
	}

	// Blank-line separated, not newline separated. A fragment usually ends on a
	// list item and the next one opens with `###`; without the blank line,
	// marked keeps that heading inside the list item, so the entry renders as
	// body text of the previous bullet instead of as its own heading.
	block := unreleasedHeading + "\n\n" + strings.Join(frags, "\n\n") + "\n"

	at := strings.Index(changelog, unreleasedHeading)
	if at < 0 {
		return block + "\n" + strings.TrimLeft(changelog, "\n")
	}

	insert := at + len(unreleasedHeading)
	rest := changelog[insert:]
	if !strings.HasPrefix(rest, "\n") {
		rest = "\n" + rest
	}
	return changelog[:insert] + "\n\n" + strings.Join(frags, "\n\n") + "\n" + rest
}
