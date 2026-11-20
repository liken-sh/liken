---
name: releases
description: Reference for how the components of the liken repository version and release. Covers the CalVer scheme, the one release tag, which components a tag publishes and why, which checks a run runs, the development builds on main, the pinned components and their revisions, the labels and annotations that a publish writes on an image, pinning a component or a development build in a Flux OCIRepository, and the liken OS channel. Use when reasoning about a component's version or why a tag released it, when changing what a publish writes or what a run selects, when publishing an image, or when pinning a development build. To cut, tag, or ship a release, use the release skill, which is the procedure. To verify a published image or a release file, or to read an SBOM or a vulnerability scan, use the sbom-and-scans skill.
---

# Releases and development builds

Every component in this repository uses one calendar version scheme,
and one tag releases all of them. `.github/workflows/ci.yaml` runs
every release and every development build. `ci/` is the program that
writes that workflow from each component's `package.toml`, and it
decides what each run publishes.

**Publishing needs the repository variable `PUBLISH` set to `true`.**
Without it, a tag or a push to `main` runs its checks and pushes
nothing. `gh variable get PUBLISH -R liken-sh/liken` prints the
value. The `release` skill is the procedure that cuts a release, and
it checks this variable before the tag.

## The version scheme

CalVer, `yyyy.mm.dd-nnn`:

* `yyyy.mm.dd` is the day of the release, in the workstation's local
  date.
* `nnn` is a three-digit serial within that day, starting at `001`.
* The tag is the bare version, with no `v` prefix. It is lightweight,
  not annotated.

The tags that the components brought from their own repositories have
the component's name as a prefix, such as
`display-operator/2026.09.27-001`. These tags are history. Nothing
creates a prefixed tag, and the release workflow does not run for
one. The OS's tags before `2026.08.18-002` have a `v` prefix.

`corrosion` is outside this repository. It uses the same CalVer scheme
with its own tags. On its `liken` branch, `make -C liken release` tags
the next serial of the day and publishes `ghcr.io/liken-sh/corrosion`
at that tag. The `corrosion` entry of `toolchains/images.toml` pins
that image by tag and digest, and `library-operator/corrosion/Dockerfile`
refers to it by its bare name.

## What a tag releases

A pushed tag such as `2026.10.02-001` is a release. For each component
that publishes, the plan job in `ci.yaml` finds the component's newest
published release and the git tag of that release. Then it diffs the
component's paths from that tag to the tagged commit:

* If the component's outputs changed, the component releases under
  the tag's version. First, CI runs each check and image build whose
  inputs changed since the newest passing run on `main`, as "The
  checks a run runs" describes. Then the component's publish job
  builds every image of the component, pushes the images, and then
  pushes the component's deploy artifact.
* If the component did not change, it keeps its version. Its pods do
  not restart.
* A component with no release yet, or whose release has no git tag in
  this repository, releases.

A component's paths are its own directory and the directory of every
component that its `package.toml` lists in `[depends]`, through the
whole dependency graph. Some paths at the top of a component are not
part of any output, such as `docs/`, `plans/`, `README.md`, and
`smoke/`. `notOutputs` in `ci/graph.go` lists them. Go tests and
`testdata/` are not part of an output either. A change to them does
not release the component. A `.dockerignore` is an input, because it
decides which files an image's build receives.

A component's `package.toml` can list more such paths in `exclude`
under `[outputs]`. `liken` excludes `deploy/monitoring/`, because a
fleet fetches it by git and the channel contains no part of it.
`brand` excludes the files of its prose.

A change to a `package.toml` that touches only its `[[jobs]]` or its
`exclude` paths runs the component's checks and releases nothing. The
jobs define what the checks run, and no build reads either part. A
change to any other part of the file releases the component. A pinned
component is the exception: its recipe hashes the whole
`package.toml`, so any change to the file needs a new revision.

A change under `plans/` runs none of the component's jobs, because no
build, test, or drill reads a plan. A push or a pull request that
changes only plans, in the top-level `plans/` or in a component's
`plans/`, starts no `ci` run at all. A release tag always starts a
run, because GitHub does not apply path filters to a tag push.

The git tag of a release is the bare version for a release made in
this repository. For a release that the component made in its own
repository, it is `<component>/<version>`.

The plan job's summary on the run page lists each component, whether
it releases, and why. Every push to a branch that starts a run shows
the same summary for a tag at that commit. Step 2 of the `release`
skill reads it from the job's log before the tag.

The `record` job writes the GitHub release for the tag. The GitHub
release lists every component and its version at that tag. When the
tag released the OS, it also has the OS's catalog entry.

## Development builds

A push to `main` publishes a development build of each component whose
outputs changed in the push. The version is the output of
`git describe` from the newest release tag, plus a suffix:
`2026.10.02-001-dev-017-abcdef01` is 17 commits past
`2026.10.02-001`, at commit `abcdef01`. The count is for the whole
repository. The suffix sorts after its release and before the next
one. A development build never moves `:latest`.

The OS publishes no development builds. The channel contains releases
only.

## The checks a run runs

A run runs only the jobs and image builds that a change can affect. A
run that publishes selects them the same way as a run that does not.
A push to `main` and a release tag both diff against the head commit
of the newest `main` run that passed. A job whose files did not change
since that commit already passed on the same files in that run. When
the plan job cannot find that run, every job runs.

* A job runs when a changed file is one that its toolchain reads. A Go
  job reads no Rust source and no page of the manual. A Rust job reads
  no Go source and no page of the manual. A manual's `hugo` job reads
  no Rust source. The OS build reads no page of its manual. A `prek`
  job reads every file.
* A job sees a dependency only through its toolchain: the modules that
  its `go.mod` replaces, the crates that its crate depends on by path,
  or the theme in `brand` for a manual. A change to a pinned base
  affects only image builds.
* An image builds when the files that BuildKit sends to its build
  changed: its contexts, minus the files that their ignore files
  exclude, its Dockerfile, its bake target, its smoke check, or an
  image it builds on. A run that publishes also builds each image that
  reads the `VERSION` build argument, so the image passes its smoke
  check at the version that the publish pushes.
* A check never builds an image of a pinned component when its tag is
  already published with the tree's recipe, because the published
  image is the build of that recipe. When a change affects such an
  image, the images job pulls the published image and runs its smoke
  check. For an image with no smoke check, nothing runs. A dry run of
  its publish builds nothing either, because publishing a tag that is
  already published only signs it again.
* The site deploys when a manual's job or a job with a coverage
  profile ran, when the site's build changed, or when every job runs.

The publish waits for the checks that ran, and it builds every image
of the component from the layer cache. The plan job's summary lists
the jobs and image builds that run for each component, and why.

## Pinned components

A pinned component is one whose `package.toml` sets a `version` and a
`revision` under `[package]`. The base images, such as `mpv`, are
pinned components, and so are the INDI images of `indi` and the CA
bundle of `trust`. The component publishes under its own tag,
`<version>-<revision>`, such as `20260928-1`, which never looks like a
release version. The version of a base is the date of the snapshot
that it installs from.

Each image also has a label `sh.liken.upstream.<name>` for each entry
of `[package.upstream]` in its `package.toml`, such as
`sh.liken.upstream.indi`. So the registry shows which upstream release
a dated tag contains. A label of a base passes into every image built
`FROM` it. So an image built on a pinned base writes each `sh.liken.*`
label of the base with an empty value, as "What an image says about
itself" describes.

A base installs from a dated snapshot of Debian sid. `indi` installs
from dated snapshots of Ubuntu, the INDI PPA, and the PHD2 PPA. CI
builds the images of `indi` only when its tag is new. `astrometry` is the
plate solver that `astrophotography-operator` runs. It is pinned on
the Debian snapshot of the bases and moves with them. `trust` pins the
CA bundle that the OS build uses, and that every image that verifies
TLS uses, so one bump updates all of them. A change to a base's recipe
needs a new revision, and so does each pinned component that builds
on that base.

A pinned component publishes when its tag is not on ghcr.io yet, while
`PUBLISH` is `true`. That happens on the first push to `main`, or the
first release tag, that contains the new tag. A tag that is already
published builds nothing. The published image records the hash of its
recipe in the label `sh.liken.recipe`. The plan job fails when the
recipe in the tree differs from that label, because then the recipe
changed and the revision did not. The rule for a new revision is in
the `bump-components` skill.

A pinned component moves `:latest` to its tag when it publishes, so a
person who pulls a base by hand gets the current pin. No build reads
`:latest`. A consumer uses a published base only by its digest, and
only after `ci bases` checks these conditions:

* The base's tag in the tree resolves to a digest on ghcr.io.
* A signature from the base's own workflow covers that digest. The
  workflow is `component-<base>.yaml`, at `refs/heads/main` or at a
  release tag.
* The `sh.liken.recipe` label, read through that digest, equals the
  recipe in the tree.

The recipe covers every input of the build. So an image that passes
is the image that a build from the tree would make, and the signature
proves that CI built it. `make bases` runs the check on a workstation
and writes `docker-bake.override.hcl`. `docker buildx bake` reads that
file after `docker-bake.hcl`. The plan job runs the same command once
for each run. Each images job and each publish job writes the plan
job's output to the same file, so every build of a run uses the same
bases. The plan's summary has a section, "The pinned bases", that
lists each base, whether the run pulls it or builds it from the tree,
and the reason.

A base that fails a check, or whose tag is not published yet, builds
from the tree. So a commit that raises a base's revision builds the
new base and tests its consumers on it at that commit. Every run after
the base is published pulls it. A change to a base still reaches each
consumer, and the consumer releases, because the directory of its
dependency changed. The workstation harness in `media-operator/local/`
lists its bake files with `-f` and reads no override file, so it
builds every base from the tree.

## Where the outputs go

* Each image goes to `ghcr.io/liken-sh/<image>`, under the version.
  A release also moves `:latest`, unless a newer release has it. A
  pinned component's image goes under its own tag, and `:latest`
  moves to that tag.
* Each `deploy/` directory goes to
  `ghcr.io/liken-sh/<component>-deploy` as an OCI artifact, after
  every image of the component. The artifact's `kustomization.yaml`
  refers to the component's images at the same version, so a cluster
  that follows the artifact moves the manifests and the images in one
  step. `flux push artifact` writes the source and the revision, in
  the form `<version>@sha1:<commit>`. The publish adds the artifact's
  `title`, `description`, `version`, `created`, `licenses`,
  `documentation`, `url`, and `vendor` as annotations. `created` is
  the committer time of the commit, not the time of the push.
* The OS goes to the channel at `https://releases.liken.sh`, through
  `liken/releases/publish.sh`. Its SBOM goes with it, and so does a
  signature bundle next to each file.

A published version never changes. A rerun of a failed tag pushes only
what the failed run did not push.

## What an image says about itself

When `ci publish` pushes an image, it writes the keys of the OCI image
specification and the `sh.liken.*` keys onto it. It passes them as
`--set` arguments to `docker buildx bake`, with values from the
image's `package.toml` and the commit. No Dockerfile writes a `LABEL`
or a `MAINTAINER`, and `ci pins` fails on one. So an image built from
a Dockerfile in a fork has no key that says it is `liken`'s.

Each OCI key goes in three places: a label of the image's config, an
annotation of the image index, and an annotation of each platform's
image manifest. ghcr.io shows the description, the source, and the
license on the package page from the annotations of the index.
`docker inspect` shows the config. The attestation manifests have no
annotations.

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
| `org.opencontainers.image.licenses` | the `license` of a tracked component's `package.toml`. A pinned image writes none | config, index, manifest |
| `org.opencontainers.image.base.name` | the image the build starts from: a pulled pinned base by its tag, or an outside image of `toolchains/images.toml` by its fully qualified name | index, manifest |
| `org.opencontainers.image.base.digest` | the digest of that image's index | index, manifest |
| `sh.liken.recipe` | a pinned image's recipe | config |
| `sh.liken.upstream.<name>` | each entry of a pinned component's `[package.upstream]` | config |

The `created` field of the config is the time of the build, and so is
the `created` field of each history entry that the build adds. The
provenance attestation records the same time. The publish does not set
`SOURCE_DATE_EPOCH`, because BuildKit writes that argument into each
`WORKDIR` step. A publish that set it would miss the layer cache that
the images job writes, and would build every image again.

`licenses` is the license of `liken`'s own code in the image. A
tracked image can also contain programs of other projects, such as the
GPL programs of a base. A pinned image contains only the packages of
other projects, so it writes no `licenses` key. The image's SBOM lists
the license of each package.

The base keys are only annotations, because an annotation does not
pass through `FROM`. An image on `scratch` writes no base key, because
scratch is not an image. An image whose run built its pinned base from
the tree writes no base key either, because that base was never
published.

A label of the image that a Dockerfile builds `FROM` passes into the
new config, and Docker cannot delete it. So the publish follows the
image's `FROM` chain through the tree. The image writes every label
key that any image of the repository in that chain writes. A key that
the image does not own gets the value `""`. For example,
`media-operator-player` is built on `mpv`. It writes `sh.liken.recipe`
and each `sh.liken.upstream.<name>` of `mpv` and of each base under
`mpv` as `""`. `mpv` writes the upstream keys of the bases under it
as `""`.

After the push and before it signs, the publish reads the labels of
the pushed config back from ghcr.io. It fails when any key or value
differs from the labels that it wrote. So if an outside base starts to
add a label after a bump of `toolchains/images.toml`, the publish
fails at this step, and the label never reaches a signed image. A
later run that signs the published image checks it the same way first.
`:latest` is a copy of the version's index, so it has the same digest
and the same keys.

To read each level through a digest that `cosign verify` checked:

```sh
docker buildx imagetools inspect "$image@$digest" --format '{{ json .Image.Config.Labels }}'
docker buildx imagetools inspect --raw "$image@$digest" | jq .annotations
```

A pinned image's set of labels is part of its recipe format. A
published tag never changes. So a change to the labels that the
publish writes on a pinned image raises `recipeFormat` in
`ci/recipe.go`, and every pinned component then needs a new revision.
`pinnedLabelsOfFormat` in `ci/imagemeta_test.go` lists the labels of
each format. Its test fails when the labels change and the format
does not.

## SBOMs, signatures, and vulnerability scans

Every image that a publish job pushes has an SBOM and the provenance
of its build, and cosign signs it keyless. Each OS release includes
`sbom.cdx.json`, and a Sigstore bundle next to each file. Trivy scans
each image, and grype scans the SBOM of the OS. The `sbom-and-scans`
skill describes how each of these is made, the commands that verify an
image or a release file, and the scans. Load it before you verify a
published image, or read or change an SBOM, a signature, or a scan.

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
the tag. No pin uses a commit sha.

## The liken OS

The OS is the `liken/` component. The steps of a release session are
in the `release` skill under `.agents/skills/release`. A session tells
the kinds of build apart by the serial:

* A published release has a serial from `001` up. Its publish job
  builds the release, runs the UEFI smoke drill, and publishes the
  artifacts and the source mirrors to `https://releases.liken.sh`.
  Then it prints the catalog entry: the version and the sha256 of that
  release's `release.yaml`. Deployments adopt a release from the
  catalog, so a release session does not touch a cluster.
* A lab release uses a `-9xx` serial, for example
  `make release VERSION=$(date +%Y.%m.%d)-901`. Serial `000` is the
  working-tree channel that the media targets bundle, and published
  releases use `001` and up, so `-9xx` collides with neither.

A tag releases the OS when any of its paths changed since its previous
release, whatever the tag was meant for. An OS release reboots every
machine in a fleet that follows releases.

The rules of a release session, such as when to tag, what to do while
the tag's run is in progress, and what to do when it fails, are in the
`release` skill.
