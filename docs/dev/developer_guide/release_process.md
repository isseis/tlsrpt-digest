# Release Process

This document explains the release process for `tlsrpt-digest`. Pushing a tag triggers GitHub Actions to automatically build binaries and create a GitHub Release.

---

## Overview

1. Verify the latest commit on the main branch
2. Create a version tag (`git tag -a vX.Y.Z HEAD`)
3. Push the tag (`git push origin vX.Y.Z`)
4. The GitHub Actions Release workflow starts
5. GoReleaser builds linux/amd64 and linux/arm64 binaries, generates tar.gz archives and checksums.txt
6. A GitHub Release is created with the archives and changelog attached

---

## Prerequisites

- All CI checks on the `main` branch must be green.
- `go.mod` / `go.sum` must be up to date (no differences after running `go mod tidy`). GoReleaser verifies this before the release and fails if any differences are found.

---

## Release Steps

### 1. Verify the latest state of the main branch

```bash
git checkout main
git pull origin main
make test && make lint
```

### 2. Decide on a version

This project follows [Semantic Versioning](https://semver.org/).

| Type of change | Version bump | Example |
|---|---|---|
| Backward-compatible bug fix | Patch (increment Z) | `v1.2.3` → `v1.2.4` |
| Backward-compatible new feature | Minor (increment Y, reset Z) | `v1.2.3` → `v1.3.0` |
| Breaking change | Major (increment X, reset Y and Z) | `v1.2.3` → `v2.0.0` |

To check the most recent release tag, run:

```bash
git tag --list 'v*' --sort=-version:refname | head -5
```

### 3. Create and push a tag

```bash
git tag -a vX.Y.Z HEAD -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

The tag format is `v[digits].[digits].[digits]` (e.g., `v1.0.0`, `v0.2.1`). Note that the workflow trigger pattern `v[0-9]*.[0-9]*.[0-9]*` is a GitHub Actions glob format; tags such as `v1.2.3-rc1` or `vtest` may also match in some cases. To avoid unintended releases, always create tags in the `vX.Y.Z` format (digits only).

### 4. Verify the GitHub Actions workflow

Check the [Actions tab](https://github.com/isseis/tlsrpt-digest/actions) to confirm the Release workflow has started. When the workflow completes, a new release will appear on the [Releases page](https://github.com/isseis/tlsrpt-digest/releases).

---

## Release Artifacts

The following files are attached to the GitHub Release.

| File | Contents |
|---|---|
| `tlsrpt-digest_X.Y.Z_linux_amd64.tar.gz` | Linux (x86-64) binary and accompanying files |
| `tlsrpt-digest_X.Y.Z_linux_arm64.tar.gz` | Linux (ARM64) binary and accompanying files |
| `checksums.txt` | SHA-256 checksums for all archives |

Each tar.gz archive contains the following files:

- `tlsrpt-digest` (binary)
- `LICENSE`
- `README.md`
- `README.ja.md`

---

## Changelog Generation Rules

GoReleaser automatically generates a changelog from the git log between the previous tag and the current tag. Commits beginning with the following prefixes (including scoped variants) are excluded from the changelog.

| Exclude pattern | Example |
|---|---|
| `docs:` / `docs(…):` | `docs(readme): update installation guide` |
| `test:` / `test(…):` | `test(imap): add edge case` |
| `chore:` / `chore(…):` | `chore(deps): bump golangci-lint` |

---

## If a Tag Was Pushed by Mistake

Delete the tag and, if the workflow is currently running, cancel it from the GitHub Actions page.

```bash
git tag -d vX.Y.Z
git push origin :refs/tags/vX.Y.Z
```

To cancel the workflow, go to the [Actions tab](https://github.com/isseis/tlsrpt-digest/actions), select the relevant run, and click "Cancel workflow".

If a GitHub Release has already been created, delete it manually from the GitHub Releases page.

---

## Related Files

| File | Contents |
|---|---|
| [`.goreleaser.yml`](../../../.goreleaser.yml) | GoReleaser configuration (build, archive, changelog) |
| [`.github/workflows/release.yml`](../../../.github/workflows/release.yml) | Release workflow definition |
