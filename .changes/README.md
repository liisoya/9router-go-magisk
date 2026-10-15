# Changelog fragments

Each pull request adds **one file here** instead of editing the top of
`CHANGELOG.md`. The release process folds them in.

## Why

Every PR used to prepend its entry at the top of `## [Unreleased]`. That made
those few lines the most contested hunk in the repository, so two PRs that landed
in the same window collided there — and this repo merges through GitHub's web
merge button, which ignores `.gitattributes` merge drivers, so no setting made
them merge automatically. A per-PR fragment is a new unique file, so two
concurrent PRs never share a line and cannot conflict.

## How to write one

Name it `<pr-number>-<slug>.md`, e.g. `219-chat-402-model-scoped.md`. The PR
number sets the order: higher numbers are rendered first.

Write the entry exactly as it would have appeared under `## [Unreleased]` — a
`###` heading plus the bullets explaining what changed and why. Nothing else is
needed; the heading is added by the merge.

## How it reaches the dashboard

The dashboard reads this through `GET /api/changelog`, which assembles
`CHANGELOG.md` plus every fragment in this folder (`internal/changelogfrag`), so
merged work is visible **before** it ships a release. No waiting for a tag.

## At release time

```bash
./scripts/bump-version.sh 1.9.11
make changelog-merge TAG=v1.9.11
git add CHANGELOG.md .changes/
```

That files every fragment under `## [v1.9.11] - <date>`, drops the now-empty
`[Unreleased]` section, and empties this folder.