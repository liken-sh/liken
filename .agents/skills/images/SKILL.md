---
name: images
description: Writes or changes a Dockerfile in the liken repository - the one form that builds a Go program, cargo auditable for a Rust binary, a closure of Debian or Ubuntu packages on scratch with its dpkg records, a static program that buildroot builds from liken/userland, how a Dockerfile names a base image of the repository and builds through docker-bake.hcl, and what the ci pins check refuses. Use before writing, changing, or reviewing a Dockerfile, a closure script, a FROM line, or a buildroot recipe or fragment under liken/userland, when adding an image to a component, and when ci pins fails on a Dockerfile.
---

# Images

A scanner lists what an image holds from records inside the image: a
Go binary's build info, the crate graph that cargo-auditable writes,
and the dpkg records of a closure. So every Dockerfile in the
repository builds each kind of program in one form, and
`cd ci && go run . pins -root ..` fails on a Dockerfile that breaks
one. prek runs that check on each commit that changes a Dockerfile.

A file that a Dockerfile downloads is a pin. The `pins` skill holds
the rules for its checksum, its `pin.toml`, and its `latest.sh`. The
`releases` skill holds the labels that a publish writes and the tags
of the pinned base images. No Dockerfile writes a `LABEL` or a
`MAINTAINER`, because `ci publish` writes every label.

## The bake file and the base images

Every image builds through `docker-bake.hcl`, on a workstation and in
CI, as the target of the same name:
`docker buildx bake media-operator-player --load` at the top of the
repository. A Dockerfile that builds on a base image of the repository
names it bare, as in `FROM mpv`, and no Dockerfile names a published
tag of a base. The bake file gives the build the base's published
image when that image is the build of the tree's recipe, and builds
the base from its own directory at the same commit when it is not.
`make bases` at the top of the repository checks the published tag of
each pinned base that an image builds on. The check passes when a
signature of the base's own workflow covers the image's digest, and
the `sh.liken.recipe` label read through that digest equals the recipe
of the tree. `make bases` writes the reference and digest of each base
that passes into `docker-bake.override.hcl`, which bake reads after
the generated file and git ignores. CI's plan job runs the same
command once for each run, and every build of the run reads its
output. So a release does not install a base's packages from
snapshot.debian.org again, and a commit that raises a base's revision
builds the new base, and its consumers on it, from the tree until the
base's tag is published. Root plan 89 holds the reasons. Run
`make workflows` at the top of the repository after you change a
`FROM` line, because `ci/` writes `docker-bake.hcl` and the workflows
from them, and CI fails when the generated files differ from what the
generator writes.

## Go programs

A Go binary records the modules it links in its build info, and syft
and Trivy read that record to list the modules of an image. Every
Dockerfile that ships a Go program builds it the same way, so the
record is in every binary, and a person, a manifest, or an operator
that writes a pod's command finds the program at the same path in
every image:

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

- The builder is the `golang` entry of `toolchains/images.toml`. A CLI
  image runs the build stage with `--platform=$BUILDPLATFORM` and
  cross-compiles with `GOOS` and `GOARCH`.
- `CGO_ENABLED=0` makes a static binary that runs on `scratch` and on
  any base, and the build needs no C toolchain. No Go program in the
  repository needs cgo. `media-capabilities` calls libva through
  purego, which needs no cgo, but its binary names the dynamic loader,
  so it runs only on the `vaapi` base that carries libva.
- `-trimpath` keeps the paths of the build machine out of the binary.
- `-s -w` drops the symbol table and the DWARF data. The build info is
  in its own section, which `-s -w` keeps, and
  `go version -m <binary>` prints it. A panic's stack trace keeps its
  function names. No build runs upx, strip, or objcopy on a Go
  program, because upx and objcopy can hide or remove the build info.
- `-X main.version=${VERSION}` is there only when the program has a
  `version` variable.
- The program is at `/<name>`, where `<name>` is the program's name,
  in every image that ships it, on any base. A second program of the
  same image, such as a pod build or a helper that a manifest runs, is
  at `/<its name>` too.
- `notices/go.sh` writes the license notices of the standard library
  and of each linked module. An image whose program makes HTTPS
  requests to hosts outside the cluster copies the `trust` component.
- When every pod that runs the image sets a non-root `runAsUser`, the
  image names the same user with a numeric `USER`, so `docker run`
  runs the program the same way. An image that a pod runs as root
  keeps root and names no `USER`.

`cd ci && go run . pins -root ..` fails on a `go build` without
`CGO_ENABLED=0`, `-trimpath`, or `-ldflags "-s -w"`, on a program
built, copied, or named in an `ENTRYPOINT` at a path other than
`/<name>`, and on upx, strip, or objcopy run on a program.

## Rust binaries

A Rust binary holds no record of the crates that it links, so syft
and Trivy list none of them from an image, and a scan matches no
advisory against them. So every Dockerfile that builds Rust runs
`cargo auditable build` in place of `cargo build`. cargo-auditable
records the crate graph of the build in the binary's `.dep-v0`
section, and both scanners read that section. The stub build of the
dependency layer runs through it too, so the dependencies compile
once under the same wrapper that the real build uses.

`toolchains/cargo-auditable/fetch.sh` pins cargo-auditable, and the
`latest.sh` beside it reports and bumps the pin. Each Rust image
takes `toolchains/` as the build context `toolchains`, copies
`fetch.sh` and `pin.sh` from it, and runs `fetch.sh`, so one pin
serves every image. `cd ci && go run . pins -root ..` fails on a
Dockerfile that runs `cargo build`.

## Closures

An image that ships files of Debian or Ubuntu packages without the
packages is a closure on scratch, and a closure holds no
`/var/lib/dpkg/status`. Without a package record, syft writes an SBOM
with almost none of the image's packages, and Trivy finds no package
to match against an advisory.

So every closure on a Debian or Ubuntu builder sources
`vulkan/closure.sh`, the closure library, through a build context, and
calls `collect` and then `notices`. No closure writes its own ldd walk
or its own copy of the notices. For every package that the tree holds
a file of, `notices` writes `/var/lib/dpkg/status.d/<package>`, the
package's stanza from the builder's dpkg status, and
`/var/lib/dpkg/status.d/<package>.md5sums`. It also copies
`/etc/os-release` and the distribution's version file, so a scanner
knows which advisories apply. syft and Trivy read this layout, which
Google's distroless images use. `indi/closure.sh` is a copy of the
library, because the build context of `indi` does not reach `vulkan/`,
and `cd ci && go run . pins -root ..` fails when the two copies
differ. A closure that unpacks a Debian package that its Dockerfile
fetches by checksum, as `library-operator/appearances/inference-closure.sh`
does, writes the same record from the package's control file, so the
package needs no `pin.toml` entry.

## Static programs

A program that the OS image or an image's `FROM scratch` stage
carries from another project must be one static binary, because a
`liken` machine and those images have no loader and no shared
libraries. buildroot builds these programs, from the BR2_EXTERNAL tree
in `liken/userland/`, because it builds the musl toolchain and every
library from source tarballs that its `.hash` files pin, and its
`legal-info` and `utils/generate-cyclonedx` write the source offer and
the CycloneDX SBOM of each build. An Alpine container with `apk add`
installs the packages of the day of the build, so no build takes that
form, and `cd ci && go run . pins -root ..` fails on an `apk add`.

- A new program is a buildroot package that a fragment of
  `liken/userland` selects: `os.config` for the OS, which
  `liken/userland/build.sh` builds and copies into `dist/`, and
  `bluetoothd.config` for the bluetoothd image, which its Dockerfile
  builds. `liken/userland/buildroot.sh` runs both builds with
  `common.config`. A change to buildroot's recipe is a hook in
  `external.mk`.
- A cold build of the OS's programs takes most of the OS build job, so
  `liken/userland/artifact.sh` keeps each finished `dist/` in
  `ghcr.io/liken-sh/userland`, tagged with a hash of the tree and the
  `buildroot` entry. `make` pulls the tag, and builds only when the
  pull fails. Only CI on `main` pushes a tag.
- When buildroot's recipe builds a program only with shared libraries,
  a recipe of the tree builds it static: `iscsi/` holds open-iscsi and
  libkmod, and `bluetooth/` holds BlueZ. A patch to a project's source
  is one that upstream can take, such as a build definition that
  respects meson's `default_library`.
- buildroot is pinned to a commit on its `master` branch in
  `liken/userland/VERSION`, and its builder is the `buildroot` entry of
  `toolchains/images.toml`. The bluetoothd Dockerfile names the same
  commit and digest, and the same SPDX license list that
  `generate-cyclonedx` reads, and `ci pins` fails when they differ.
  syft finds no package in a static program, so the bluetoothd image
  carries buildroot's SBOM of its build as its record for syft:
  `image-sbom -buildroot` keeps the target packages and leaves out the
  host tools. When master
  packages an older release than upstream,
  `liken/userland/versions.mk` sets the newer version, and
  `patches/<package>/<version>/<package>.hash` pins its tarball.
  buildroot's recipe holds patches for the version that it names, and
  a later release can already hold their changes. The file
  `patches/<package>/<version>/held` names each patch of the recipe
  that the release holds, and `liken/userland/buildroot.sh` keeps
  buildroot from applying it. A patch is a debt: when a release holds
  a patch's change, take the release and drop the patch.
