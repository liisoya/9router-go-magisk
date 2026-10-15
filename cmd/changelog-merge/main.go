// Command changelog-merge folds the pending per-PR fragments in .changes/ into
// CHANGELOG.md under a new version heading, then empties the folder.
//
// This runs at release time, not per PR. It is the only step that writes
// CHANGELOG.md, which is what keeps that file out of every concurrent PR diff.
//
// Usage:
//
//	go run ./cmd/changelog-merge --tag v1.9.11
//	go run ./cmd/changelog-merge --tag v1.9.11-exp.1 --dry-run
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"9router/proxy/internal/changelogfrag"
)

func main() {
	tag := flag.String("tag", "", "release tag to file the pending fragments under, e.g. v1.9.11")
	dryRun := flag.Bool("dry-run", false, "print the merged changelog instead of writing it")
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	if strings.TrimSpace(*tag) == "" {
		fail(errors.New("--tag is required, e.g. --tag v1.9.11"))
	}

	now := time.Now()

	if *dryRun {
		merged, err := changelogfrag.Merge(*root, *tag, now)
		if err != nil {
			fail(err)
		}
		fmt.Print(merged)
		return
	}

	if err := changelogfrag.ApplyMerge(*root, *tag, now); err != nil {
		fail(err)
	}
	fmt.Printf("merged .changes/ into %s under ## [%s]\n", changelogfrag.MainFile, *tag)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "changelog-merge:", err)
	os.Exit(1)
}
