---
name: releases
description: How the components of the liken repository version and release. Covers the CalVer scheme, the one release tag, which components a tag publishes, the development builds on main, the pinned base images, the labels and annotations that a publish writes on an image, pinning a development build, and the liken OS channel publish. Use when tagging, publishing an image, pinning a development build, or reasoning about a component's version. To verify a published image or a release file, or to read an SBOM or a vulnerability scan, use the sbom-and-scans skill.
---

# Releases and development builds

Every component in this repository versions on one calendar scheme,
and one tag releases all of them. `.github/workflows/ci.yaml` carries
out every release and every development build. `ci/`, the program
that writes that workflow from each component's `package.toml`,
decides what each run publishes.

**Publishing needs the repository variable `PUBLISH` set to `true`.**
Without it, a tag and a push to `main` run their checks and push
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

`corrosion`, outside this repository, uses the same CalVer scheme with
its own tags. `make -C liken release` on its `liken` branch tags the
next serial of the day and publishes `ghcr.io/liken-sh/corrosion` at
that tag. The `corrosion` entry of `toolchains/images.toml` pins that
image by tag and digest, and `library-operator/corrosion/Dockerfile`
names it bare.

## What a tag releases

A pushed tag such as `2026.10.02-001` is a release. For each component
that publishes, the plan job in `ci.yaml` reads the component's newest
published release, finds the git tag of that release, and diffs the
component's paths from there to the tagged commit:

* A component whose outputs changed releases under the tag's version.
  The checks and the images that read the change since the newest
  green run on `main` run, as the section "The checks a run runs"
  says. Its publish job then builds every image of the component and
  pushes the images and then its deploy artifact.
* A component that did not change keeps its version. Its pods do not
  restart.
* A component with no release yet, or whose release has no git tag
  here, releases.

A component's paths are its own directory and the directories of every
component its `package.toml` names in `[depends]`, through the whole
graph. `docs/`, `plans/`, `AGENTS.md`, `README.md`, `skills/`,
`smoke/`, `.pre-commit-config.yaml`, `.testcoverage.yml`, and
`.gitignore` at the top of a component go into no output, and neither
do Go tests or `testdata/`. A change there does not release the
component. A `.dockerignore` is an input: it decides what an image's
build receives. A component's `package.toml` can name more such paths in
`exclude` under `[outputs]`: `liken` names `deploy/monitoring/`,
because a fleet fetches it by git and the channel holds no part of it,
and `brand` names its prose, `voice.md`, `motion.md`, and
`editing-prompt.md`. A change to a `package.toml` that touches only its
`[[jobs]]` or its `exclude` paths runs the component's checks and
releases nothing, because the jobs are what the checks run and no build
reads either part. A change to any other part of it releases the
component. A pinned component is the exception: its recipe hashes its
whole `package.toml`, so any change to the file needs a new revision.
A change under `plans/` also runs none of the component's jobs,
because no build, test, or drill reads a plan. A push or a pull
request that changes only plans, in the top-level `plans/` or in a
component's `plans/`, starts no `ci` run at all. A release tag always
runs, because GitHub does not apply path filters to a tag push.

The git tag of a release is the bare version for a release made in this
repository, or `<component>/<version>` for a release the component made
in its own repository.

The plan job's summary on the run page lists each component, whether
it releases, and why. On every push to a branch that starts a run,
the same summary shows what a tag at that commit would release, so
read it before you tag.

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

## The checks a run runs

A run runs only the jobs and the images that a change can affect, and
a run that publishes selects them the same way as a run that does not.
A push to `main` and a release tag diff with the head of the newest
`main` run that passed, so a job whose files did not change since then
passed on the same files in that run. When the plan cannot find that
run, every job runs.

* A job runs when a changed file is one that its toolchain reads. A Go
  job reads no Rust source and no page of the manual. A Rust job reads
  no Go source and no page of the manual. A manual's `hugo` job reads
  no Rust source. The OS build reads no page of its manual. A `prek`
  job reads every file.
* A job reads a dependency only through its toolchain: the modules
  that its `go.mod` replaces, the crates that its crate takes by path,
  or brand's theme for a manual. A pinned base reaches only images.
* An image runs when what BuildKit sends it changed: its contexts less
  their ignore files, its Dockerfile, its bake target, its smoke check,
  or an image it builds on. A run that publishes also runs each image
  that reads the `VERSION` build argument, so the image passes its
  smoke check at the version that the publish pushes.
* An image of a pinned component whose tag is published with the
  tree's recipe never builds in a check, because the published image
  is the build of that recipe. When a change reaches it, the images
  job pulls the published image and runs its smoke check, and an image
  with no smoke check runs nothing. A dry run of its publish builds
  nothing either, because a publish of a published tag only signs it
  again.
* The site deploys when a manual's job or a job with a coverage
  profile ran, when the site's build changed, or when every job runs.

The publish waits for the checks that ran, and it builds every image
of the component from the layer cache. The plan job's summary lists
the jobs and the images that run for each component, and why.

## Pinned components

The base images, `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`, the
INDI images of `indi`, and the CA bundle of `trust` are pinned
components. A pinned component's `package.toml` states a
`version` and a `revision`, and it publishes under its own tag,
`<version>-<revision>`, such as `20260928-1`. That tag never looks
like a release version. The version of a base is the date of the
snapshot it installs from. Each image also carries a label
`sh.liken.upstream.<name>` for each entry of `[package.upstream]` in
its `package.toml`, such as `sh.liken.upstream.indi`, so the registry
shows which upstream release a dated tag holds. A label of a base
passes into every image built `FROM` it, so an image built on a pinned
base writes each of the base's `sh.liken.*` labels with an empty
value, as "What an image says about itself" gives.

A base installs from a dated snapshot of Debian sid. `indi` installs
from dated snapshots of Ubuntu, the INDI PPA, and the PHD2 PPA, and
CI builds its 17 images only when its tag is new. `astrometry`, the
plate solver that `astrophotography-operator` runs, is pinned on the
bases' Debian snapshot and moves with them. `trust` pins the CA bundle
that the OS build and every image that verifies TLS read, so one bump
reaches all of them. A change to a base's recipe needs a new revision,
and so does each pinned component that builds on it.

A pinned component publishes when its tag is not on ghcr yet: on the
first push to `main` or the first release tag that carries the new
tag, while `PUBLISH` is `true`. A tag that is published already builds
nothing. The published image carries the hash of its recipe in the
label `sh.liken.recipe`, and the plan job fails when the tree's recipe
differs from it: the recipe changed, and the revision did not. The
`bump-components` skill holds the rule for a new revision.

A pinned component moves `:latest` to its tag when it publishes, so a
person who pulls a base by hand gets the current pin. No build reads
`:latest`. A consumer reads a published base only by its digest, and
only after `ci bases` checks it:

* the base's tag in the tree resolves to a digest on ghcr.io;
* a signature of the base's own workflow, `component-<base>.yaml` at
  `refs/heads/main` or at a release tag, covers that digest;
* the `sh.liken.recipe` label read through that digest equals the
  tree's recipe.

The recipe covers every input of the build, so an image that passes is
the one a build from the tree would make, and the signature proves
that CI built it. `make bases` runs the check on a workstation and
writes `docker-bake.override.hcl`, which `docker buildx bake` reads
after `docker-bake.hcl`. The plan job runs the same command once for
each run, and each images job and publish job writes its output as
the same file, so every build of a run uses the same bases. The plan's
summary has a section, "The pinned bases", that lists each base and
whether the run pulls it or builds it from the tree, with the reason.

A base that fails a check, or whose tag is not published yet, builds
from the tree. So a commit that raises a base's revision builds the
new base and tests its consumers on it at that commit, and every run
after the base's publish pulls it. A change to a base still reaches
each consumer, and the consumer releases because its dependency's
directory changed. The workstation harness in `media-operator/local/`
names its bake files with `-f` and reads no override file, so it
builds every base from the tree.

## Where the outputs go

* Each image goes to `ghcr.io/liken-sh/<image>`, under the version.
  A release also moves `:latest`, unless a newer release has it. A
  pinned component's image goes under its own tag, and `:latest`
  moves to that tag.
* Each `deploy/` directory goes to
  `ghcr.io/liken-sh/<component>-deploy` as an OCI artifact, after
  every image of the component. The artifact's `kustomization.yaml`
  names the component's images at the same version, so a cluster that
  follows the artifact moves the manifests and the images in one step.
  `flux push artifact` writes the source and the revision, in the form
  `<version>@sha1:<commit>`, and the publish adds the artifact's
  `title`, `description`, `version`, `created`, `licenses`,
  `documentation`, `url`, and `vendor` as annotations. `created` is
  the committer time of the commit, not the time of the push.
* The OS goes to the channel at `https://releases.liken.sh`, through
  `liken/releases/publish.sh`, with its SBOM and a signature bundle
  beside each file.

A published version never changes. A rerun of a failed tag pushes only
what the failed run did not.

## What an image says about itself

`ci publish` writes the keys of the OCI image specification and the
`sh.liken.*` keys onto each image when it pushes it, through `--set`
arguments to `docker buildx bake`, from the image's `package.toml`
and the commit. No Dockerfile writes a `LABEL` or a `MAINTAINER`, and
`ci pins` fails on one, so a build of a Dockerfile in a fork carries
no key that says the image is `liken`'s. Each OCI key goes in three
places: a label of the image's config, an annotation of the image
index, and an annotation of each platform's image manifest. ghcr.io
shows the description, the source, and the license on the package
page from the index's annotations, and `docker inspect` reads the
config. The attestation manifests carry no annotation.

| Key | Value | Where |
| --- | --- | --- |
| `org.opencontainers.image.title` | the image's name | config, index, manifest |
| `org.opencontainers.image.description` | `description` of the image's `[[outputs.images]]` entry | config, index, manifest |
| `org.opencontainers.image.version` | the release or development version, or a pinned image's `<version>-<revision>` | config, index, manifest |
| `org.opencontainers.image.revision` | the full sha of the commit | config, index, manifest |
| `org.opencontainers.image.created` | the committer time of the commit | config, index, manifest |
| `org.opencontainers.image.source` | `https://github.com/liken-sh/liken` | config, index, manifest |
| `org.opencontainers.image.url` | `https://liken.sh/` | config, index, manifest |
| `org.opencontainers.image.documentation` | the component's manual, `https://liken.sh/<prefix>/`, or its directory in the repository at the commit when it has no manual | config, index, manifest |
| `org.opencontainers.image.vendor` | `liken` | config, index, manifest |
| `org.opencontainers.image.licenses` | the `license` of a tracked component's `package.toml`; a pinned image writes none | config, index, manifest |
| `org.opencontainers.image.base.name` | the image the build starts from: a pulled pinned base by its tag, or an outside image of `toolchains/images.toml` by its fully qualified name | index, manifest |
| `org.opencontainers.image.base.digest` | the digest of that image's index | index, manifest |
| `sh.liken.recipe` | a pinned image's recipe | config |
| `sh.liken.upstream.<name>` | each entry of a pinned component's `[package.upstream]` | config |

The `created` field of the config, and of each history entry that the
build adds, is the time of the build, and the provenance attestation
records the same time. The publish sets no `SOURCE_DATE_EPOCH`,
because BuildKit writes that argument into each `WORKDIR` step, and a
publish that sets it misses the layer cache that the images job writes
and builds every image again.

`licenses` names the license of `liken`'s own code in the image. A
tracked image can hold programs of other projects as well, such as
the GPL programs of a base, and a pinned image holds only the
packages of other projects, so it writes no `licenses` key. The
image's SBOM names the license of each package.

The base keys are annotations alone, because an annotation does not
pass through `FROM`. An image on `scratch`, and an image whose run
built its pinned base from the tree, write no base key: scratch is no
image, and a base built from the tree was never published.

A label of the image that a Dockerfile builds `FROM` passes into the
new config, and Docker cannot delete it. So the publish walks the
image's `FROM` chain through the tree, and the image writes each label
key that an image of the repository in the chain writes. A key that
the image does not own takes the value `""`. `media-operator-player`,
on `mpv`, writes `sh.liken.recipe` and each `sh.liken.upstream.<name>`
of `mpv`, `ffmpeg`, `vaapi`, and `vulkan` as `""`, and `mpv` writes
the upstream keys of the bases under it as `""`.

After the push and before the signature, the publish reads the
labels of the pushed config back from ghcr.io and fails when they
differ from the labels it wrote, in any key or value. A label that an
outside base starts to carry after a bump of `toolchains/images.toml`
fails there, so it never reaches a signed image. A run again checks
the published image the same way before it signs it. `:latest` is a
copy of the version's index, so it names the same digest and carries
the same keys.

To read each level through a digest that `cosign verify` checked:

```sh
docker buildx imagetools inspect "$image@$digest" --format '{{ json .Image.Config.Labels }}'
docker buildx imagetools inspect --raw "$image@$digest" | jq .annotations
```

A pinned image's label set is part of its recipe format. A published
tag never changes, so a change to the labels that the publish writes
on a pinned image raises `recipeFormat` in `ci/recipe.go`, and every
pinned component then needs a new revision. `pinnedLabelsOfFormat` in
`ci/imagemeta_test.go` holds the labels of each format, and its test
fails on a change to them that keeps the format.

## SBOMs, signatures, and vulnerability scans

Every image that a publish job pushes carries an SBOM and the
provenance of its build, and cosign signs it keyless. Each OS release
carries `sbom.cdx.json` and a Sigstore bundle beside each file. Trivy
scans each image, and grype scans the SBOM of the OS. The
`sbom-and-scans` skill holds how each one is written, the commands
that verify an image or a release file, and the scans. Load it before
you verify a published image, or read or change an SBOM, a signature,
or a scan.

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
