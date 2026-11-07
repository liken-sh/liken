---
name: releases
description: How the components of the liken repository version and release. Covers the CalVer scheme, the one release tag, which components a tag publishes, the development builds on main, the pinned base images, each image's SBOM, provenance, and cosign signature and how to verify them, the OS release's SBOM and the signature of each of its files and how to verify them, pinning a development build, and the liken OS channel publish. Use when tagging, publishing an image, verifying a published image, pinning a development build, or reasoning about a component's version.
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
shows which upstream release a dated tag holds.

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
* The OS goes to the channel at `https://releases.liken.sh`, through
  `liken/releases/publish.sh`, with its SBOM and a signature bundle
  beside each file.

A published version never changes. A rerun of a failed tag pushes only
what the failed run did not.

## Image attestations and signatures

Every image that a publish job pushes, in a development build, a
release, or a pinned component's tag, carries two buildx attestations:
an SPDX SBOM, and the build's SLSA provenance in max mode, which
records the build arguments, the named contexts, the Dockerfile, and
the digest of each base image and download. The commit is in the
image's `org.opencontainers.image.revision` label. buildx stores both
attestations in the image's index on ghcr.io, as a manifest of the
platform `unknown/unknown` beside the image's own manifest. The SBOM
comes from buildx's syft scanner, which `toolchains/images.toml` pins
as `buildkit-syft-scanner`, so the program that writes each SBOM
changes only with a commit. `ci/sign.go` sets both attestations on the
one target that the publish pushes, so a base that the build needs as
a context carries none. The bake file carries none either, because the
images job and a workstation load the image into Docker.

syft lists what a program or a package database records about itself:
a Go binary's build info, the crates that cargo-auditable records,
and the dpkg records of a closure. It finds nothing in a file that a
Dockerfile fetches by its checksum, or in a static program that
buildroot built. So an image that ships such files carries a CycloneDX
record of them at `/usr/share/doc/liken/<image>.cdx.json`, which
`image-sbom` from `toolchains/manifest` writes during the build from
the pin's `pin.toml`, or from buildroot's SBOM for the bluetoothd
image. syft copies each record's components into the image's SBOM,
with the purl, the CPE, and the license of the record, and gives the
record's path as their `sourceInfo`. An image that copies another
image's tree, such as `COPY --from=trust / /`, copies its record too.
"Pins and downloads" in the root `AGENTS.md` gives the rule.

After the push, the publish job runs cosign, from the image that
`toolchains/images.toml` pins, and signs the digest of the index
under each name of the image. The signature is keyless: the job holds
`id-token: write`, Fulcio certifies the component's workflow at its
ref as the signer, and Rekor records the signature. cosign pushes the
signature to the image's own ghcr.io repository, as an OCI referrer
of the digest, or under the tag `sha256-<digest>` on a registry that
has no referrers API. Only the publish job of a component with images
and the OS's publish job hold `id-token: write`. A dry run builds the
same attestations and signs nothing.

A signature that fails fails the publish, before `:latest` moves and
before the deploy artifact names the image. The rerun finds the
version published, and signs it again by its tag, which cosign
resolves to the digest that the failed run pushed.

To verify an image, resolve its tag to a digest once, and read
everything by that digest:

```sh
image=ghcr.io/liken-sh/git-csi-driver
digest="$(docker buildx imagetools inspect "$image:2026.11.01-001" --format '{{ .Manifest.Digest }}')"
cosign verify "$image@$digest" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/liken-sh/liken/\.github/workflows/component-[a-z0-9-]+\.yaml@refs/(heads/main|tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3})$'
docker buildx imagetools inspect "$image@$digest" --format '{{ json .SBOM }}'
docker buildx imagetools inspect "$image@$digest" --format '{{ json .Provenance }}'
```

`liken verify image "$image:2026.11.01-001"` runs the same checks
in-process, with sigstore-go and the trusted root pinned in
`liken/verify/`: it resolves the tag to the index's digest, verifies
the signature against the same identity, and reports the signer and
the package count of each platform's SBOM. `liken sbom <reference>`
prints the SBOM's packages after the same check, and `-json` prints
the SPDX document. The commands above stay the reference for what
the check does.

To list the packages of the SBOM that came from a record, with the
purl and the CPE of each:

```sh
docker buildx imagetools inspect "$image@$digest" --format '{{ json .SBOM.SPDX }}' |
  jq -r '.packages[] | select((.sourceInfo // "") | test("/usr/share/doc/liken/.*[.]cdx[.]json"))
    | [.name, .versionInfo, (.externalRefs[]?.referenceLocator)] | join(" ")'
```

`cosign verify` checks that Fulcio issued the certificate to a
workflow of this repository from GitHub's OIDC issuer, that the
workflow ran at `main` or at a release tag, that the signature covers
the digest, and that Rekor holds it. A branch or a pull request never
publishes, and the identity refuses a signature from any other ref.
To require one component, put its workflow in the identity, as in
`component-git-csi-driver\.yaml`. The SBOM and the provenance carry no
signature of their own. The index names each of them by its digest,
so they are the ones that the signature covers when they are read
through the digest that `cosign verify` checked.

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

## The OS release's SBOM and signatures

Each OS release carries `sbom.cdx.json`, a CycloneDX SBOM, at
`https://releases.liken.sh/<version>/sbom.cdx.json`. `release.yaml`
names it by its digest like every other artifact, so it is on each
machine's boot slot too. `make release` writes it before the bundle,
with `liken/releases/sbom/scan.sh`, from three sources:

* each `pin.toml` of a pin that the release ships: the kernel, k3s,
  the firmware, and the rest, each with its purl, version, sha256,
  license, and download URL. An entry with `scope = "excluded"`,
  which only the build reads, keeps that scope;
* buildroot's SBOM, `liken/userland/dist/sbom.cdx.json`, for the
  static programs and every package they link, host packages with
  the scope `excluded`;
* syft, pinned in `toolchains/images.toml`, over the root filesystem
  that the image build staged, each image archive in it, the toolkit,
  and the programs that k3s writes out of its own binary. This is
  where liken's own Go programs and their modules, and k3s's
  containerd, runc, and CNI plugins, come from.

`liken/releases/sbom` merges them. A program that syft finds at a
path that `scan.sh` claims for a manifest or a buildroot package,
such as `/bin/k3s`, keeps the manifest's or buildroot's component,
and the modules it links depend on that component. The subject,
`metadata.component`, is the release at its CalVer version. The
`compositions` state `complete` for buildroot's packages and for each
of liken's own Go programs, and `incomplete` for the release as a
whole and for k3s, because syft identifies only programs that record
what they contain. The merge checks the document against the
CycloneDX schema in `liken/releases/sbom/schema/` before it writes
the file. The lab's working-tree channel carries an SBOM of the
manifests and buildroot alone, with no syft scan.

Count the components by source, and read one:

```sh
sbom=releases/dist/<version>/sbom.cdx.json
jq -r '.components[] | .properties[] | select(.name == "liken:source") | .value' "$sbom" | sort | uniq -c
jq '.components[] | select(.name == "k3s")' "$sbom"
jq '.compositions' "$sbom"
```

The publish job signs `release.yaml` and every file that it names,
keyless, with the cosign that `toolchains/images.toml` pins, through
`liken/releases/sign.sh`, before `publish.sh` uploads the directory.
Fulcio certifies `component-liken.yaml` at the release tag, Rekor
records each signature, and the Sigstore bundle of each file is at
`<file>.sigstore.json` beside it. To verify a downloaded file:

```sh
v=2026.11.01-001
curl -fsSLO "https://releases.liken.sh/$v/release.yaml"
curl -fsSLO "https://releases.liken.sh/$v/release.yaml.sigstore.json"
cosign verify-blob release.yaml --bundle release.yaml.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/liken-sh/liken/\.github/workflows/component-liken\.yaml@refs/tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$'
```

The OS publishes from release tags alone, so the identity names no
branch. `liken verify release -digest sha256:<hex> <version>`
runs this check on `release.yaml` and on every artifact it names, and
checks each artifact's digest and the SBOM's schema too.
`liken sbom <version>` checks `release.yaml` and the SBOM the same way
and prints the SBOM's packages. `liken/verify/` holds both checks,
and `liken/verify/latest.sh` reports and bumps the Sigstore trusted
root that they read. A verified `release.yaml` also vouches for each artifact by
its digest, as it does for a machine.

To rehearse the signing on a lab release, sign with a throwaway key.
The script then writes nothing to the public Rekor, so the verify
skips the log:

```sh
cosign=$(awk -F' = ' '$1 == "cosign" { gsub(/"/, "", $2); print $2 }' ../toolchains/images.toml)
key=$(mktemp -d)
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -e COSIGN_PASSWORD= -v "$key:/k" -w /k "$cosign" generate-key-pair
COSIGN_PASSWORD= releases/sign.sh <version> "$key/cosign.key"
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$key:/k:ro" -v "$PWD/releases/dist/<version>:/r:ro" -w /r "$cosign" \
  verify-blob --key /k/cosign.pub --insecure-ignore-tlog --bundle sbom.cdx.json.sigstore.json sbom.cdx.json
rm -rf "$key"
```

## Rules

* The tag is the release act. Everything after it is verification.
* Do not tag a commit that CI has not proven on `main`.
* Push nothing to `main` until the tag's run is green. A new push does
  not cancel the tag's run, but its development builds race the
  release's pushes.
* Do not retag a release whose run failed. Stop and report.
