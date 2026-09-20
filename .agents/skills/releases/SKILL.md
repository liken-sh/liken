---
name: releases
description: How the liken-sh repositories version and release. Covers the CalVer scheme, cutting a release tag in an operator repository, the development image builds on main, and the liken OS channel publish. Use when tagging, publishing an image, pinning a development build, or reasoning about a version across repositories.
---

# Releases and development builds

Every repository in the organization versions on the same calendar
scheme, with a few exceptions. This skill carries the scheme and the
release flow for each kind of repository, so a session at the
organization root can cut or reason about a release without opening
every repository's own rules.

Read the repository's own `AGENTS.md` first. The operator repositories
repeat the development build rules there, and the `liken` OS repository
carries its own `release` skill under `.agents/skills/release`, which
owns the exact steps for the OS.

## The version scheme

CalVer, `yyyy.mm.dd-nnn`:

* `yyyy.mm.dd` is the day of the release.
* `nnn` is a three-digit serial within that day, starting at `001`.
  Run `git tag -l "$(date +%Y.%m.%d)-*"` and take the next number.
* The tag is the bare version, with no `v` prefix, and it is
  lightweight, not annotated.

The scheme is the same in the `liken` OS repository and in every
operator repository. Tags in `liken` before `2026.08.18-002` carry a
`v` prefix. On a day that has tags in both forms, list both to find the
highest serial:
`git tag -l "$(date +%Y.%m.%d)-*" "v$(date +%Y.%m.%d)-*"`.

`corrosion` versions with semver, such as `v1.0.0` or
`corro-client-v0.2.0-alpha.0`, because it is a general library.
`brand` and `plugins` publish no tags.

## Operator repositories

This is the flow in `audio-operator`, `bluetooth-operator`,
`display-operator`, `equipment-operator`, `git-csi-driver`,
`library-operator`, `media-operator`, `people-operator`, and
`per-node-csi-driver`. Each repository's `AGENTS.md` states it.

A pushed tag is a release. `release.yaml` builds every image in the
repository beside the `ci.yaml` run of the same commit, waits for that
run to pass, and pushes the images under the version tag and under
`:latest`. The images go to `ghcr.io/liken-sh/<repository>`. The
workflow refuses a tag that is not `yyyy.mm.dd-nnn` before it builds
anything.

A push to `main` is a development build. `release.yaml` derives the
version from `git describe` of the most recent release tag, plus a
suffix: `2026.09.03-007-dev-003-abcdef01` is three commits past
`2026.09.03-007`, at commit `abcdef01`. The suffix sorts after its
release and before the next one. A development build never moves
`:latest`, so a cluster that pulls a release keeps pulling releases.

To run a development build, pin the manifests to the full 40-character
commit sha and the image to the build's version:

```yaml
resources:
  - https://github.com/liken-sh/<repository>//deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/<repository>
    newTag: 2026.09.03-007-dev-003-abcdef01
```

The CI run's step summary prints both lines for a commit. A `git fetch`
by sha needs all forty characters, so the short sha inside the version
is not enough for `ref=`.

## The liken OS

The OS releases through `liken`'s own `release` skill, which owns the
steps. Two facts let a session tell the kinds of build apart:

* A published release runs from serial `001` up. The tag triggers
  `release.yaml`, which builds, runs the smoke drills, publishes the
  artifacts and the source mirrors to `https://releases.liken.sh`, and
  prints the catalog entry: the version and the sha256 of that
  release's `release.yaml`. Deployments adopt a release from the
  catalog, so a release session does not touch a cluster.
* A lab release uses a `-9xx` serial, for example
  `make release VERSION=$(date +%Y.%m.%d)-901`. Serial `000` is the
  working-tree channel the media targets bundle, and published releases
  run from `001` up, so `-9xx` collides with neither and is
  recognizable at a glance.

## Rules

* The tag is the release act. Everything after it is verification.
* Do not tag a commit that CI has not proven.
* Do not retag a release whose workflow failed. Stop and report.
