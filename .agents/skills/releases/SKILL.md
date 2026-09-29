---
name: releases
description: How the components of the liken repository version and release. Covers the CalVer scheme, the one release tag, which components a tag publishes, the development builds on main, the pinned base images, pinning a development build, and the liken OS channel publish. Use when tagging, publishing an image, pinning a development build, or reasoning about a component's version.
---

# Releases and development builds

Every component in this repository versions on one calendar scheme,
and one tag releases all of them. `.github/workflows/ci.yaml` carries
out every release and every development build. `ci/`, the program
that writes that workflow from each component's `package.toml`,
decides what each run publishes.

**Publishing needs the repository variable `PUBLISH` set to `true`.**
Without it, a tag and a push to `main` run every check and push
nothing. Check it with `gh variable get PUBLISH -R liken-sh/liken`. If
it is not `true`, stop and tell me; do not push a tag.

## The version scheme

CalVer, `yyyy.mm.dd-nnn`:

* `yyyy.mm.dd` is the day of the release.
* `nnn` is a three-digit serial within that day, starting at `001`.
  Run `git tag -l "$(date +%Y.%m.%d)-*"` and take the next number.
* The tag is the bare version, with no `v` prefix, and it is
  lightweight, not annotated.

The tags that the components brought from their own repositories carry
the component's name as a prefix, such as
`display-operator/2026.09.27-001`. They are history: nothing creates a
prefixed tag now, and the release workflow does not run for one. The
OS's tags before `2026.08.18-002` carry a `v` prefix.

`corrosion`, outside this repository, versions with semver, because it
is a general library.

## What a tag releases

A pushed tag such as `2026.10.02-001` is a release. For each component
that publishes, the plan job in `ci.yaml` reads the component's newest
published release, finds the git tag of that release, and diffs the
component's paths from there to the tagged commit:

* A component whose outputs changed releases under the tag's version.
  Its checks run, its images build and pass their smoke checks, and
  its publish job pushes the images and then its deploy artifact.
* A component that did not change keeps its version. Its pods do not
  restart.
* A component with no release yet, or whose release has no git tag
  here, releases.

A component's paths are its own directory and the directories of every
component its `package.toml` names in `[depends]`, through the whole
graph. `docs/`, `plans/`, `AGENTS.md`, and `README.md` at the top of a
component go into no output, so a change there does not release it.

The git tag of a release is the bare version for a release made in this
repository, or `<component>/<version>` for a release the component made
in its own repository.

The plan job's summary on the run page lists each component, whether
it releases, and why. On every push to a branch, the same summary shows
what a tag at that commit would release, so read it before you tag.

The `record` job writes the GitHub release for the tag: every
component and its version at that tag, and the OS's catalog entry when
the tag released the OS.

## Development builds

A push to `main` publishes a development build of each component whose
outputs changed in the push. The version comes from `git describe` of
the newest release tag, plus a suffix:
`2026.10.02-001-dev-017-abcdef01` is 17 commits past
`2026.10.02-001`, at commit `abcdef01`. The count is for the whole
repository. The suffix sorts after its release and before the next
one. A development build never moves `:latest`.

The OS publishes no development builds. The channel holds releases
only.

## Pinned components

The base images, `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`, are
pinned components. A pinned component's `package.toml` states a
`version` and a `revision`, and it publishes under its own tag,
`<version>-<revision>`, such as `20260928-1`. That tag never looks
like a release version. The version of a base is the date of the Debian
snapshot it installs from.

A pinned component publishes when its tag is not on ghcr yet: on the
first push to `main` or the first release tag that carries the new
tag, while `PUBLISH` is `true`. A tag that is published already builds
nothing. The published image carries the hash of its recipe in the
label `sh.liken.recipe`, and the plan job fails when the tree's recipe
differs from it: the recipe changed, and the revision did not. The
`bump-components` skill holds the rule for a new revision.

A pinned component never moves `:latest`. No consumer reads a
published base: every consumer builds `FROM` the base in the tree,
through `docker-bake.hcl`, so a change to a base reaches each
consumer at the same commit, and the consumer releases because its
dependency's directory changed.

## Where the outputs go

* Each image goes to `ghcr.io/liken-sh/<image>`, under the version.
  A release also moves `:latest`, unless a newer release has it. A
  pinned component's image goes under its own tag and leaves `:latest`
  as it is.
* Each `deploy/` directory goes to
  `ghcr.io/liken-sh/<component>-deploy` as an OCI artifact, after
  every image of the component. The artifact's `kustomization.yaml`
  names the component's images at the same version, so a cluster that
  follows the artifact moves the manifests and the images in one step.
* The OS goes to the channel at `https://releases.liken.sh`, through
  `liken/releases/publish.sh`.

A published version never changes. A rerun of a failed tag pushes only
what the failed run did not.

## Pin a component

A cluster follows a component through a Flux `OCIRepository` on the
deploy artifact, with the version as its tag:

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: display-operator
  namespace: flux-system
spec:
  interval: 1h
  url: oci://ghcr.io/liken-sh/display-operator-deploy
  ref:
    tag: 2026.10.02-001
```

A development build pins the same way, with its development version as
the tag. No pin names a commit sha.

## The liken OS

The OS is the `liken/` component. The `release` skill under
`.agents/skills/release` owns the steps of a release session. Two facts
let a session tell the kinds of build apart:

* A published release runs from serial `001` up. Its publish job builds
  the release, runs the UEFI smoke drill, publishes the artifacts and
  the source mirrors to `https://releases.liken.sh`, and prints the
  catalog entry: the version and the sha256 of that release's
  `release.yaml`. Deployments adopt a release from the catalog, so a
  release session does not touch a cluster.
* A lab release uses a `-9xx` serial, for example
  `make release VERSION=$(date +%Y.%m.%d)-901`. Serial `000` is the
  working-tree channel the media targets bundle, and published releases
  run from `001` up, so `-9xx` collides with neither.

A tag releases the OS when any of its paths changed since its previous
release, whatever the tag was meant for. An OS release reboots every
machine in a fleet that follows releases.

## Rules

* The tag is the release act. Everything after it is verification.
* Do not tag a commit that CI has not proven on `main`.
* Push nothing to `main` until the tag's run is green. A new push does
  not cancel the tag's run, but its development builds race the
  release's pushes.
* Do not retag a release whose run failed. Stop and report.
