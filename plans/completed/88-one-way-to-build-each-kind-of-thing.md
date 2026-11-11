# 88, One way to build each kind of thing

Proposed on 2026-11-01. Built on 2026-11-01, except two parts:

- The Go images do not build with ko. bake already writes the SBOM
  and the provenance of every image and cosign signs each one, syft
  reads the module list from each Go binary's build info, and
  `AGENTS.md` states one Dockerfile form for a Go program, which
  `ci/goimages.go` checks. ko would add a second build path for one
  kind of image. This is open for a decision by the maintainer.
- No apk lock exists, because no build installs from Alpine now.
  buildroot builds every static program, including open-iscsi and the
  daemons of the `bluetoothd` image, and `ci pins` fails on an
  `apk add`.

`AGENTS.md` at the top of the repository states each build form and
the SBOM of each kind of artifact, and the `bump-components` and
`releases` skills hold the procedures. Each image that holds software
syft cannot find, such as a program built from a tarball or by
buildroot, carries a CycloneDX record that `toolchains/manifest`
writes from its `pin.toml` or from buildroot's SBOM.

The drills ran on the `lab` fleet on 2026-11-01: a release built from
these changes booted, an upgrade to it reached `Ready`, and its SBOM
passed the schema check. These have not run: the first CI publish,
which proves the cosign signer identities and the job limits; the
`bluetoothd` image with its renamed `dbus` user on a machine with a
radio; and the audio and Bluetooth programs at their new paths on
real hardware.

The components of `liken` started in separate repositories, and each
one wrote its own build scripts. In this repository, components that
use the same build strategy now do the same steps in different ways. A
survey on 2026-11-01 found four copies of the closure functions, three
places where a pin's digest lives, 26 `latest.sh` scripts with four
interfaces, and builds that install unpinned Alpine packages. Each
difference is a place where a fix lands in one copy and not in the
others, and several defects came from that. The same survey found that
no image or release carries an SBOM a scanner can read: syft finds 6
of the 219 Debian packages in the `mpv` image, because the closures
write a text list and no package database.

This plan gives each build strategy one form. `AGENTS.md` at the top of
the repository states each form, shared tooling implements it, and a
check in `ci/` fails when a component departs from it. The plan also
publishes an SBOM for every release and every image, and signs both.

## What stays

The images stay `FROM scratch` with file-level closures, and the
packages still come from Debian sid, Ubuntu, and Alpine. On 2026-11-01
four reviews tested a move to Nix and rejected it. Nix copies whole
store paths into an image, so the `mpv` base would grow from 206 MB to
at least 488 MB compressed. Fixes to widely shared libraries reach
nixpkgs-unstable through a staging cycle of three to four weeks, and
no scanner matches vulnerabilities in a Nix SBOM well.
[Plan 72](../../liken/plans/completed/72-the-os-builds-from-source.md)
in `liken/plans/` rejects Nix for the OS because a reader must learn the
framework to understand the build.

## The shared core

- **One manifest for each pin.** Each pin's directory holds one file
  that names each file the pin downloads: its version, URL, sha256,
  purl, and license. `latest.sh --bump` writes it, and the fetch, the
  source mirror in `liken/licensing/sources.sh`, the `components:` of a
  release, and the SBOM read it. No build reads a digest from the
  network.
- **One fetch helper.** It downloads to a partial file, checks the
  digest from the manifest, and moves the file into
  `cache/<version>/`.
- **buildroot builds the static programs.** e2fsprogs, nfs-utils,
  open-iscsi, wpa_supplicant, and tzdata build today in an Alpine
  container from packages that `apk add` installs unpinned. buildroot
  builds them, their musl toolchain, and the libraries they link from
  source tarballs that its `.hash` files pin, and it writes their
  CycloneDX SBOM and their source offer. A `BR2_EXTERNAL` tree in this
  repository holds the configuration, and it follows buildroot's
  `master` branch so the versions stay current. k3s-root, which
  `liken/xtables` vendors, is a buildroot project too. Plan 72 rejects
  buildroot for the whole OS; this use is limited to the static
  programs. `bluetoothd` moves too if its build fits.
- **One apk lock for each build that stays on Alpine.** It lists every
  package that the build installs, with its version and sha256. Alpine
  deletes a superseded package, so a build fails when a locked version
  is gone, and the failure is the signal to bump. The `.apk` files are
  not mirrored.
- **One closure library.** `vulkan/closure.sh` is the implementation.
  Each closure that runs on a Debian or Ubuntu builder uses it, and a
  check in `ci/` keeps the copy that `indi/` needs identical to it.
- **One `latest.sh` library**, for the report row, the network calls,
  and the bump.

## SBOMs, provenance, and signatures

- Each closure writes a `/var/lib/dpkg/status.d/<package>` entry for
  every package it takes files from. syft and Trivy read that path, so
  the SBOM and the vulnerability scan of an image see its packages.
- Each Rust binary records its crates, so the SBOM sees them.
- `ci/` publishes each image with buildx's SBOM and provenance
  attestations and signs it with cosign. The SBOM and the signature are
  in the registry beside the image.
- The OS release publishes a CycloneDX SBOM on `releases.liken.sh`,
  written by syft and merged with the pin manifests and buildroot's
  SBOM, and signs the release's files.
- The Go images build with ko.

Each image puts its program at `/<name>`.

## Later

Renovate replaces Dependabot only if finishing Dependabot's pull
requests by hand keeps costing too much
([plan 87](../open-problems/what-a-vulnerability-finding-does-to-a-build.md)).
