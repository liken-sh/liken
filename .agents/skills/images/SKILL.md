---
name: images
description: Describes the required form of a Dockerfile in the liken repository - the one way to build a Go program, cargo auditable for a Rust binary, a closure of Debian or Ubuntu packages on scratch with its dpkg records, and a static program that buildroot builds from liken/userland. Also covers how a Dockerfile names a base image of the repository, how every image builds through docker-bake.hcl, and what the ci pins check rejects. Use before writing, changing, or reviewing a Dockerfile, a closure script, a FROM line, or a buildroot recipe or fragment under liken/userland, when adding an image or a kubectl plugin to a component, when building a program from source in an image, when an image or its build cache is larger than expected, and when ci pins fails on a Dockerfile or CI's plan job fails because the recipe of a pinned component changed with no new revision.
---

# Images

syft and Trivy find out what an image contains by reading records
inside the image. A Go binary has its build info, a Rust binary has
the crate graph that cargo-auditable writes, and a closure has its
dpkg records. So each kind of program has one required form in a
Dockerfile, and the sections below describe each form.
`cd ci && go run . pins -root ..` fails on a Dockerfile that does not
follow its form, and prek runs that check on each commit that changes
a Dockerfile.

A file that a Dockerfile downloads is a pin. The rules for its
checksum, its `pin.toml`, and its `latest.sh` are in the `pins`
skill. The labels that a publish writes, and the tags of the pinned
base images, are in the `releases` skill. No Dockerfile writes a
`LABEL` or a `MAINTAINER`, because `ci publish` writes every label.

## Which section to read

* **Build an image, or change a `FROM` line:** "The bake file and the
  base images".
* **A change that moves the recipe of a base, or a CI failure about a
  revision:** "The recipe of a pinned component".
* **Fetch a file into an image:** "The bake file and the base images"
  and "The recipe of a pinned component". The rules of the download
  and its `pin.toml` are in the `pins` skill.
* **A Go program:** "Go programs".
* **A Rust program:** "Rust binaries". For a program added to an
  image that exists, or a crate in a workspace, also read "Add a Rust
  program to an image". For a crate of another component, read "Link
  a crate of another component".
* **Files copied out of Debian or Ubuntu packages:** "Closures".
* **A static program from another project:** "Static programs".
* **Add an image to a component:** "A new image in a component".
* **Add a kubectl plugin to an operator:** "The image of a kubectl
  plugin", then "Go programs".

## The bake file and the base images

Every image is built through `docker-bake.hcl`, on a workstation and
in CI. The bake target has the same name as the image. From the top
of the repository, run `make bases` first, and then bake:

```sh
make bases
docker buildx bake media-operator-player --load
```

Without `make bases`, bake builds each base from its own directory,
and installs the packages of the base from snapshot.debian.org again
instead of pulling the published base. After you change a `FROM`
line, run `make workflows` at the top of the repository. `ci/`
generates `docker-bake.hcl` and the workflows from the `FROM` lines,
and CI fails when the generated files differ from what the generator
writes.

A Dockerfile that builds on a base image of the repository names the
base by its bare name, as in `FROM mpv`. No Dockerfile names a
published tag of a base. When the published image of the base was
built from the recipe in the tree, the build uses the published
image. When it was not, bake builds the base from its own directory
at the same commit.

`make bases`, at the top of the repository, checks the published tag
of each pinned base that an image builds on. The check passes when
both of these are true:

- A signature from the base's own workflow covers the image's digest.
- The `sh.liken.recipe` label, read through that digest, is equal to
  the recipe in the tree.

`make bases` writes the reference and the digest of each base that
passes into `docker-bake.override.hcl`. Bake reads that file after
the generated file, and git ignores it. CI's plan job runs the same
command once in each run, and every build in the run uses its
output.

As a result, a release does not install a base's packages from
snapshot.debian.org again. A commit that raises the revision of a
base builds the new base from the tree, and builds the images on top
of it from the tree too, until the new tag of the base is published.
The reasons are in root plan 89.

### The recipe of a pinned component

A pinned component is one whose `package.toml` sets a `version`
under `[package]`, as `weston/package.toml` does. Only a pinned
component has a recipe and a `revision`. A component with no
`version`, such as `display-operator`, takes its version from the
release tag or the development build. So it needs no change when a
base under it changes. A new revision of a base cascades only to the
pinned components above it, as `PinnedDependents` in `ci/pinned.go`
finds them.

The recipe of a pinned component covers every file in each of its
build contexts, as `Recipe` in `ci/recipe.go` reads them. It also
covers the digest of each image from outside the repository that the
build uses, such as the `golang` entry of `toolchains/images.toml`.

Some build contexts are directories outside the component. Examples
are `toolchains/manifest` as the context `manifest`, and
`toolchains/` as the context `toolchains`. A change to any file in
them, even to a comment, changes the recipe of each pinned component
that uses it. For example, the `weston` target uses `golang` and
`manifest`, so a bump of Go or an edit of `toolchains/manifest`
changes the recipe of `weston`. A file that the context's
`.dockerignore` excludes is not part of the recipe.

A published tag never changes. So when a change moves the recipe of a
pinned component, CI's plan job fails with "the recipe of N pinned
components changed with no new revision". It keeps failing until the
same commit raises the revision of each of those components.

Before you push, run `cd ci && go run . bases -root ..` at the top of
the repository. It needs docker and access to ghcr.io. For each base,
it prints whether a build pulls the published image or builds the
base from the tree, and why. If a base builds from the tree and you
did not raise its revision, your change moved its recipe. The
`bump-components` skill describes the cascade of revisions from one
base to the bases above it.

## Go programs

A Go binary records the modules that it links in its build info.
syft and Trivy read that record to list the modules in an image.
Every Dockerfile that ships a Go program builds it the same way. That
way the record is in every binary, and a person, a manifest, or an
operator that writes a pod's command finds the program at the same
path in every image:

```dockerfile
FROM golang AS build
...
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /<name> .
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /<name> /<name>
COPY --from=build /notices /
ENTRYPOINT ["/<name>"]
```

- **The builder** is the `golang` entry of `toolchains/images.toml`.
  A CLI image runs the build stage with `--platform=$BUILDPLATFORM`
  and cross-compiles with `GOOS` and `GOARCH`.
- **`CGO_ENABLED=0`** builds a static binary, which runs on `scratch`
  and on any base, and the build needs no C toolchain. No Go program
  in the repository needs cgo. `media-capabilities` calls libva
  through purego, which needs no cgo. But its binary still names the
  dynamic loader, so it runs only on the `vaapi` base, which has
  libva.
- **`-trimpath`** leaves the paths of the build machine out of the
  binary.
- **`-s -w`** drops the symbol table and the DWARF data. It does not
  drop the build info, which is in its own section, and
  `go version -m <binary>` prints it. A panic's stack trace still
  shows its function names. No build runs upx, strip, or objcopy on a
  Go program, because upx and objcopy can hide or remove the build
  info.
- **`-X main.version=${VERSION}`** is there only when the program has
  a `version` variable.
- **The path.** The program is at `/<name>`, where `<name>` is the
  program's name, in every image that ships it, on any base. A second
  program in the same image, such as a pod build or a helper that a
  manifest runs, is at `/<its name>` too.
- **The notices.** `notices/go.sh` writes the license notices of the
  standard library and of each linked module. When the program makes
  HTTPS requests to hosts outside the cluster, the image copies the
  `trust` component.
- **The user.** When every pod that runs the image sets a non-root
  `runAsUser`, the image sets the same user with a numeric `USER`, so
  `docker run` runs the program the same way. When a pod runs the
  image as root, the image stays root and has no `USER`.

`cd ci && go run . pins -root ..` fails on each of these:

- A `go build` without `CGO_ENABLED=0`, `-trimpath`, or
  `-ldflags "-s -w"`.
- A program built, copied, or named in an `ENTRYPOINT` at a path
  other than `/<name>`.
- upx, strip, or objcopy run on a program.

## Rust binaries

A Rust binary has no record of the crates that it links. Without
one, syft and Trivy list none of those crates in an image, and a scan
matches no advisory against them. So every Dockerfile that builds
Rust runs `cargo auditable build` instead of `cargo build`.
cargo-auditable records the crate graph of the build in the binary's
`.dep-v0` section, and both scanners read that section.

The stub build, which compiles only the dependencies into their own
layer, also runs through cargo-auditable. The dependencies then
compile once, under the same wrapper as the real build.

`toolchains/cargo-auditable/fetch.sh` pins cargo-auditable, and the
`latest.sh` beside it reports and bumps the pin. One pin serves every
Rust image: each image uses `toolchains/` as the build context
`toolchains`, copies `fetch.sh` and `pin.sh` from it, and runs
`fetch.sh`. `cd ci && go run . pins -root ..` fails on a Dockerfile
that runs `cargo build`.

### Add a Rust program to an image

`media-operator/Dockerfile.screen` and `media-operator/package.toml`
show each of these steps.

- **The build context `toolchains`.** A build context is declared in
  the `contexts` of the image's `[[outputs.images]]` entry in
  `package.toml`. When the image gets its first Rust program, add
  `toolchains = "toolchains"` there. `media-operator-player`, for
  example, has no such context.
- **The other members of the workspace.** A Cargo workspace is a set
  of crates, its members, with one `Cargo.toml` at the top and one
  `Cargo.lock`. cargo loads the manifest of every member, even when
  the build names only one with `-p`. So a Dockerfile that builds a
  member copies the `Cargo.toml` of each other member, and writes a
  stub `src/main.rs` or `src/lib.rs` for it in the dependency stage.
  When you add a member, add these lines to each Dockerfile that
  builds a member of the same workspace, not only to the Dockerfile
  of the new member. When another member depends on a crate of
  another component by path, the Dockerfile also copies that crate's
  `Cargo.toml` from its build context, and the image needs that
  context, even when the program that you add does not use the crate.
  `media-operator/idle` depends on `media-screen`, so the build of
  `media-display` in `media-operator/Dockerfile.screen` copies the
  manifest of `media-screen` too. "Link a crate of another component"
  gives the steps.
- **The ignore file of the Dockerfile.** A Dockerfile can have its own
  ignore file, `<Dockerfile>.dockerignore`, beside it. In this
  repository, that file is usually an allowlist: it excludes `*` and
  then names each path that the build may read, as
  `media-operator/Dockerfile.player.dockerignore` does. Add each new
  path to it, such as the new member's `Cargo.toml` and `src/`.
  Otherwise the build does not see the files.

### Link a crate of another component

A crate can depend on a crate of another component by a relative
path, as `media-operator/idle` and `library-operator/media-browser`
depend on `media-screen`. The build context of the Dockerfile does not
include the other component, so the Dockerfile copies the crate from a
build context. `media-operator/Dockerfile.screen` and
`library-operator/media-browser/Dockerfile` show each step:

1. In `package.toml`, add the context to the image's `contexts`, as
   `media-screen = "media-screen"`.
2. In `package.toml`, add the component to `components` under
   `[depends]`. Then a change to the crate counts as a change to the
   consumer, and CI builds the consumer's images again.
3. In the dependency stage of the Dockerfile, copy the crate's
   `Cargo.toml` to the same path relative to `/src` that the crate has
   relative to the consumer, and write a stub `src/lib.rs` for it:
   `COPY --from=media-screen Cargo.toml /media-screen/`.
4. In the build stage, copy the crate's source over the stub, and
   `touch` it so that cargo compiles it again:
   `COPY --from=media-screen src/ /media-screen/src/`.
5. Build the consumer once on your workstation, and commit its
   `Cargo.lock`. The consumer's `Cargo.lock` records the crate and
   its dependencies, and the image builds with `--locked`, which fails
   when the lock file needs a change.

## Closures

A closure is an image built `FROM scratch` that contains only the
files a program needs, copied out of Debian or Ubuntu packages. The
packages themselves are not installed, so the image has no
`/var/lib/dpkg/status`, the file that a scanner reads to learn which
packages an image has. Without a package record, syft writes an SBOM
that lists almost none of the image's packages, and Trivy finds no
package to match against an advisory.

The closure library, `vulkan/closure.sh`, writes those records. A
closure that copies files from packages installed on its builder
sources the library through a build context, and calls `collect` and
then `notices`. Such a closure writes no ldd walk and no copy of the
notices of its own.

For each package that a file in the closure came from, `notices`
writes two files:

- `/var/lib/dpkg/status.d/<package>`, which is the package's stanza
  from the builder's dpkg status.
- `/var/lib/dpkg/status.d/<package>.md5sums`.

`notices` also copies `/etc/os-release` and the distribution's
version file, so a scanner knows which advisories apply. syft and
Trivy read this layout, which Google's distroless images use.

`indi/closure.sh` is a copy of the library, because the build context
of `indi` does not reach `vulkan/`. `cd ci && go run . pins -root ..`
fails when the two copies differ.

Some closures unpack a Debian package that their Dockerfile fetches
by checksum, as `library-operator/appearances/inference-closure.sh`
does. Such a closure writes the same record from the package's
control file, so the package needs no `pin.toml` entry.

## Static programs

A program from another project that the OS image includes, or that
an image's `FROM scratch` stage includes, must be one static binary.
A `liken` machine and those images have no dynamic loader and no
shared libraries.

buildroot builds these programs, from the BR2_EXTERNAL tree in
`liken/userland/`. buildroot builds the musl toolchain and every
library from source tarballs, and its `.hash` files pin each tarball.
Its `legal-info` and `utils/generate-cyclonedx` write the source offer
and the CycloneDX SBOM of each build.

No build uses an Alpine container with `apk add`, because `apk add`
installs the packages that Alpine has on the day of the build.
`cd ci && go run . pins -root ..` fails on an `apk add`.

- **A new program** is a buildroot package that a fragment of
  `liken/userland` selects. `os.config` selects the programs of the
  OS, and `liken/userland/build.sh` builds them and copies them into
  `dist/`. `bluetoothd.config` selects the programs of the bluetoothd
  image, and its Dockerfile builds them. `liken/userland/buildroot.sh`
  runs both builds with `common.config`. Make a change to buildroot's
  recipe as a hook in `external.mk`.
- **The cache of the OS's programs.** A cold build of the OS's
  programs takes most of the OS build job. So
  `liken/userland/artifact.sh` stores each finished `dist/` in
  `ghcr.io/liken-sh/userland`, tagged with a hash of the tree and the
  `buildroot` entry. `make` pulls the tag, and builds only when the
  pull fails. Only CI on `main` pushes a tag.
- **A program that buildroot builds only with shared libraries.** When
  buildroot's recipe builds a program only with shared libraries, a
  recipe in the tree builds it static, such as the recipe of
  open-iscsi in `iscsi/`. A patch to a project's source must be one that upstream can accept,
  such as a build definition that respects meson's `default_library`.
- **The pin of buildroot.** buildroot is pinned to a commit on its
  `master` branch in `liken/userland/VERSION`. Its builder is the
  `buildroot` entry of `toolchains/images.toml`. The bluetoothd
  Dockerfile repeats the same commit and digest, and the same SPDX
  license list that `generate-cyclonedx` reads. `ci pins` fails when
  they differ.
- **The SBOM of the bluetoothd image.** syft finds no package in a
  static program. So the bluetoothd image includes buildroot's SBOM of
  its build, as the record that syft reads. `image-sbom -buildroot`
  keeps the target packages and leaves out the host tools.
- **A newer release than buildroot packages.** When `master` packages an
  older release than upstream, `liken/userland/versions.mk` sets the
  newer version, and `patches/<package>/<version>/<package>.hash`
  pins its tarball. buildroot's recipe has patches for the version
  that the recipe names, and a later release can already include their
  changes. The file `patches/<package>/<version>/held` lists each
  patch of the recipe that the release already includes, and
  `liken/userland/buildroot.sh` stops buildroot from applying it. A
  patch is a debt: when a release includes a patch's change, take the
  release and drop the patch.
- **The build trees in a Dockerfile.** A Dockerfile stage that runs
  buildroot deletes its build trees in the same `RUN` that builds,
  before the layer is committed, as
  `bluetooth-operator/bluetoothd/Dockerfile` does. CI writes its layer cache with
  `mode=max`, which also pushes the layers of the intermediate stages.
  In a CI run of 2026-11-03, a stage that kept buildroot's output
  pushed the cross-compiler, the build tree of every package, and the
  source tarballs into `ghcr.io/liken-sh/bluetoothd:buildcache` as one
  layer of 2.19 GB compressed. A later build that missed the cache
  downloaded that layer again. Keep only the target tree, the license
  files, and the package list that the SBOM reads.

## A new image in a component

A Dockerfile alone publishes nothing. `ci` builds and publishes the
images that the component's `package.toml` lists.

1. Add an `[[outputs.images]]` entry to the component's
   `package.toml`, with the image's `name` and a one-line
   `description`. Name the Dockerfile in `file`, relative to the
   component. When the Dockerfile has more than one stage to publish,
   name the stage in `target`. List each build context from another
   directory in `contexts`, as a path from the top of the repository.
   Add a `smoke` script when the image has one. The image builds for
   `linux/amd64` unless `platforms` names others. Copy the form from
   an entry of a sibling component, such as `display-operator`. The
   comments on `Image` in `ci/component.go` describe each field.
2. Run `make workflows` at the top of the repository. It adds the
   bake target and the jobs.
3. Add the image to the component's manual. The `manuals` skill lists
   the pages.

## The image of a kubectl plugin

An operator can ship a command that people run as
`kubectl liken <domain> <verb>`. The program is in the component's
`cli/`, and `Dockerfile.cli` builds it into its own image. Copy both
from an operator that has them, such as `display-operator`. Add the
image to `package.toml` as "A new image in a component" says.

The plugin depends on these things outside the Dockerfile:

- **The binary** is named `kubectl-liken-<domain>`, because `kubectl`
  finds a plugin by its file name. The image runs it as its
  `ENTRYPOINT`, and `liken plugins sync` extracts the file that the
  entrypoint names.
- **The label.** The operator's workload in `deploy/` has the label
  `cli.liken.sh/plugin: <domain>`. `liken plugins sync` finds the
  workload by that label and reads the image of its first container.
  So put the label on the workload whose first container runs the
  operator's image.
- **The image name.** `plugins sync` adds `-cli` to the repository of
  that first container's image and keeps its tag. So the image is
  named for the operator's image, as `display-operator-cli` is for
  `display-operator`, and it publishes at the same version.
- **The platform.** The image is for `linux/amd64` only. The build
  stage runs with `--platform=$BUILDPLATFORM` and cross-compiles with
  `GOOS` and `GOARCH`, so the build needs no emulation. The last stage
  is `distroless` with the `trust` component copied in, because the
  plugin calls HTTPS APIs.
- **The test.** `cli/` is a Go package of the component's module. The
  component's `make test-go` fails on a package that has no test
  file, because such a package adds nothing to the coverage profile.
  So the plugin needs at least one `_test.go` file in `cli/`.
- **The module.** Run `go mod tidy` in the component's directory after
  you write the plugin, so that `go.mod` and `go.sum` list what the
  plugin imports. `make test-go` runs `go mod tidy -diff`, and fails
  when they do not.

"The operator plugins" in `liken/docs/content/docs/reference/cli.md`
describes how a person installs and runs a plugin. Its list "These
domains ship a CLI" names each domain, so add the new domain to that
list in the same commit.
