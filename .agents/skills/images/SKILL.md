---
name: images
description: Describes the required form of a Dockerfile in the liken repository - the one way to build a Go program, cargo auditable for a Rust binary, a closure of Debian or Ubuntu packages on scratch with its dpkg records, and a static program that buildroot builds from liken/userland. Also covers how a Dockerfile names a base image of the repository, how every image builds through docker-bake.hcl, and what the ci pins check rejects. Use before writing, changing, or reviewing a Dockerfile, a closure script, a FROM line, or a buildroot recipe or fragment under liken/userland, when adding an image to a component, when building a program from source in an image, when an image or its build cache is larger than expected, and when ci pins fails on a Dockerfile or CI's plan job fails because the recipe of a pinned component changed with no new revision.
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

## Closures

A closure is an image built `FROM scratch` that contains only the
files a program needs, copied out of Debian or Ubuntu packages. The
packages themselves are not installed, so the image has no
`/var/lib/dpkg/status`, the file that a scanner reads to learn which
packages an image has. Without a package record, syft writes an SBOM
that lists almost none of the image's packages, and Trivy finds no
package to match against an advisory.

The closure library, `vulkan/closure.sh`, writes those records. Every
closure on a Debian or Ubuntu builder sources the library through a
build context, and calls `collect` and then `notices`. No closure
writes its own ldd walk or its own copy of the notices.

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
