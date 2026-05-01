---
name: release
description: Release a new version of firpty — bump VERSION, update CHANGELOG.md, commit, tag, push, and track the GoReleaser run.
---

## Version determination

If the user provides a version, use it. Otherwise:

1. Read current version from `VERSION`.
2. Look at entries under `## [Unreleased]` in `CHANGELOG.md`:
   - `### Added` or `### Removed` → **minor** bump (0.1.0 → 0.2.0).
   - Only `### Fixed` or `### Changed` → **patch** bump (0.1.0 → 0.1.1).
   - Empty → ask the user whether to proceed or abort.

## Steps

1. **Build & test** — `make all` (includes the 100% coverage gate).
2. **Determine version** per the rules above; state it.
3. **Update CHANGELOG.md** — rename `## [Unreleased]` to `## [VERSION] - YYYY-MM-DD` and insert a fresh empty `## [Unreleased]` above it (reverse-chronological).
4. **Update VERSION** — write the new version with a single trailing newline.
5. **Commit** — `git status` to confirm contents, then `git add -A && git commit -m "release: vVERSION"`.
6. **Tag** — `git tag -a vVERSION -m "release: vVERSION"`.
7. **Verify** — `make build` then `./bin/firpty version` prints the new version.
8. **Publish** (after user confirms) — `make publish` pushes commit + tag; GoReleaser CI builds cross-platform binaries and creates the GitHub Release.

Always pass `-m` to `git commit` / `git tag -a` to avoid opening an editor. To move a tag, `git tag -d vVERSION` then re-create.

## Post-publish: track GitHub Actions

Always poll automatically after `make publish`:

```bash
gh run list --limit 10 --json status,conclusion,name,headSha,createdAt,databaseId
```

Do **not** use `--branch` — tag-triggered workflows don't appear under a branch filter. Match runs by `headSha` against the release commit.

Loop every 30s until all runs for the release SHA are `completed`. On `failure`/`cancelled`:

```bash
gh run view <run-id> --log-failed | tail -40
```
