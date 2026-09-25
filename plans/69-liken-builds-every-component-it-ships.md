# 69. `liken` builds every component it ships

Milestone 69. Proposed 2026-09-25.

Today `liken` downloads most of the programs in its image as binaries
that other projects built. This milestone makes `liken` build every
program in its release artifacts from source. The build tools come
from stagex, and each build is pinned and signed. A component builds
only when its pin changes. Each build is published once to
`releases.liken.sh`, together with its source, its recipe, and a
signed attestation, so anyone can check where each file came from.

## The problem

The image has 15 vendored domains. Each one has its own `fetch.sh`,
and the sources differ a lot:

| Domain | Pin on 2026-09-25 | Where the bytes come from |
| --- | --- | --- |
| `kernel` | `7.2.6` | Canonical's mainline archive: a `.deb` that Canonical built from the upstream tree, with Ubuntu's generic config |
| `grub` | `2.12-1ubuntu7.3` | an Ubuntu `.deb` |
| `systemd-boot` | `259.5-0ubuntu3.4` | an Ubuntu `.deb` |
| `k3s` | `v1.36.4+k3s1` | the k3s project's release binary |
| `xtables` | `v0.15.2` | the k3s-root project's release binary, which buildroot builds |
| `e2fsprogs` | `1.47.1` | gokrazy's prebuilt `mke2fs`, linked against glibc from a `debian:bullseye` container |
| `open-iscsi` | `2.1.13` | our build from source, in a pinned `alpine:3.22` container |
| `nfs-utils` | `3.1.1` | our build from source, in the same Alpine container |
| `wpa-supplicant` | `2.12` | our build from source, in the same Alpine container |
| `tzdata` | `2026d` | our build from IANA's source, in the same Alpine container |
| `linux-firmware` | `20260916` | kernel.org's release tarball |
| `microcode` | `20260812` | Intel's GitHub release, and the AMD files from `linux-firmware` |
| `hwdata` | `v0.411` | the hwdata project's `pci.ids` |
| `trust` | `2026-09-25` | the curl project's extract of Mozilla's CA list |
| `flux` | `v2.9.5` | manifests that the flux CLI renders at build time; the CLI does not ship |

Three problems follow from this:

- Nobody can rebuild the kernel, GRUB, systemd-boot, k3s, the
  xtables binaries, or `mke2fs` and compare the result with what
  `liken` ships. We trust each of those binaries because of where we
  downloaded it.
- Secure Boot with `liken`'s own keys needs a `liken` kernel build.
  Under lockdown, the kernel loads only modules that are signed with
  a key compiled into it. Canonical's build compiles in Canonical's
  key.
- The build uses three distros: Ubuntu for the kernel and the
  bootloaders, Alpine for the compiler and the static libraries, and
  Debian inside gokrazy's `mke2fs` build. Each `fetch.sh` handles its
  source in a different way.

## The claim

When this milestone is done, the project can make this claim, and
anyone can check it:

> Every program in a `liken` release was built from source by
> `liken`'s own build, with pinned tools that anyone can trace to a
> signed, reproducible build. Each build has a public attestation.

The claim covers the files in the release artifacts: the kernel, the
initramfs, the root filesystem, the bootloaders, and everything
inside them. It does not cover:

- the container images that k3s pulls at run time, such as the pause
  image and CoreDNS
- the Flux controller images that the flux seed names
- the operator images in the other `liken-sh` repositories

Those images can move to the same system later. The claim must name
its scope in every place it is published.

Firmware and microcode are binary files from their vendors. Nobody
can build them, and the claim does not say that anyone did. Their
components verify and copy the vendor's files, and their attestations
record which upstream release they came from.

## The build tools come from stagex

[stagex](https://stagex.tools/) is a set of OCI images. Each image
holds one package, and the project builds each package from source,
starting from a 190-byte assembly seed. Its builds reproduce bit for
bit, and at least four maintainers sign each package with keys on
hardware tokens. The packages use musl. On 2026-09-25, stagex
published gcc 15.2.0, clang 22.1.5, go 1.26.4, rust 1.96.0, binutils
2.46.0, musl 1.2.6, meson 1.11.1, perl 5.42.2, and python 3.14.5.

`liken` uses stagex images only for the tools and libraries that run
during a build. `liken` does not ship any file from a stagex image
directly. stagex also packages GRUB 2.14, iptables 1.8.13, e2fsprogs
1.47.4, containerd, runc, and systemd 260.2. Most of those packages
link to shared libraries, for example `e2fsprogs` configures with
`--enable-elf-shlibs` and `iptables` with `--enable-shared`. The
`liken` image has no libc and no shared libraries, so those binaries
do not start on a `liken` machine. `liken` builds its own static
versions and uses the stagex recipes as a reference.

Static libraries such as musl and openssl come from stagex and are
linked into the programs that `liken` ships. The claim counts them as
part of the toolchain, in the same way as `libgcc`. Their source goes
on the channel with the component that links them, as the Alpine
libraries do today.

For each stagex image that a component uses, the build:

1. Pins the image by digest.
2. Verifies the maintainers' PGP signatures on that digest. The
   maintainers' public keys are committed in the repository, and the
   signatures come from stagex's signature repository.
3. Copies the image to `ghcr.io/liken-sh`, so a Docker Hub rate limit
   or a deleted image does not stop a build.
4. Records the digest and the signers in the component's
   `component.yaml`.

## A component

Each vendored domain stays a top-level directory in the repository,
for example `kernel/`, `k3s/`, and `e2fsprogs/`. A domain declares
its component in three files:

- `package.toml`: the upstream version, the `liken` revision, the
  license, and each source file with its sha256 and its mirrors. The
  format follows stagex's `package.toml`.
- `Containerfile`: the build. It starts `FROM` a stagex image pinned
  by digest, adds the verified source files, and runs the build with
  `--network=none`. Its last stage is `FROM scratch` and holds only
  the files that ship.
- `README.md`: why `liken` needs the component, and the choices in
  its build. This text replaces the long header comment in each
  `fetch.sh` today.

An example for `mke2fs`:

```toml
[package]
name = "e2fsprogs"
version = "1.47.4"
revision = 1
license = "GPL-2.0-only"

[sources.e2fsprogs]
hash = "<sha256>"
file = "e2fsprogs-{version}.tar.gz"
mirrors = [
  "https://releases.liken.sh/components/e2fsprogs/{version}/source/",
  "https://mirrors.edge.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/v{version}/",
]
```

```dockerfile
FROM ghcr.io/liken-sh/stagex/pallet-gcc-gnu-busybox@sha256:<digest> AS build
ADD e2fsprogs-1.47.4.tar.gz /
WORKDIR /e2fsprogs-1.47.4
RUN --network=none ./configure --disable-nls LDFLAGS=-static \
 && make -C misc mke2fs

FROM scratch
COPY --from=build /e2fsprogs-1.47.4/misc/mke2fs /sbin/mke2fs
```

One driver builds any component from these files. The driver is the
only new directory in the repository. It fetches and verifies the
sources, runs the build with BuildKit, writes `component.yaml`, and
publishes. The `licensing` domain reads the same `package.toml` files
for the notices and the source offer, so the list of sources is
written in one place only.

The `latest.sh` scripts stay one per domain. Each upstream announces
its releases in a different way, so each domain keeps its own script
to find the newest version.
The `bump-components` skill and `make versions` keep working on top
of them. A bump now also writes a new revision and, where the build
reproduces, the new expected digests.

## Components that have their own build system

Some upstream projects ship a full build system. For those, the
`Containerfile` runs the upstream build scripts unchanged, inside a
stagex image, with our pins. We do not rewrite their builds.

k3s is two components in a chain:

- **`k3s-root`** is a buildroot project (buildroot `2025.02.14`). It
  builds busybox, coreutils, findutils, iptables and nftables,
  ipset, conntrack-tools, ebtables, ethtool, iproute2, util-linux,
  fuse-overlayfs, slirp4netns, and pigz, all as static musl
  binaries. Upstream builds it in SUSE's `bci-base` image. Buildroot
  compiles its own musl cross-compiler from source tarballs. It
  needs a host compiler only to start, and stagex's gcc is that
  compiler. `make source` downloads every tarball first and checks
  each one against buildroot's `.hash` files. Then the build runs
  with no network.
- **`k3s`** is Go with cgo, linked statically, with runc, containerd,
  the CNI plugins, and sqlite. Its `scripts/download` downloads a
  prebuilt `k3s-root` tarball from GitHub and checks its sha256. The
  `k3s` component supplies our `k3s-root` build in its place.

The `xtables` domain ships binaries from the `k3s-root` output today,
so it merges into the `k3s-root` component.

buildroot's `BR2_PRIMARY_SITE` option makes buildroot try one
download site before each package's own site. The `k3s-root` build
sets it to the component's `source/` directory on the channel.

## A component builds only when its pin changes

The `liken` revision is the declaration. The first build of an
upstream version is revision 1. A change that alters the output but
keeps the upstream version increments the revision, for example a
kernel config change or a new stagex image. The pin in the domain
changes from `7.2.6-1` to `7.2.6-2`, and that one line is the bump.

CI enforces the rule with a recipe hash. The hash covers
`package.toml`, the `Containerfile`, every file that the
`Containerfile` adds, and the digests of the stagex images. For each
component, CI compares the hash with the one in the published
`component.yaml` for the pinned revision:

- If a published revision exists and its hash matches, CI builds
  nothing and downloads the published files.
- If a published revision exists and its hash differs, CI fails. The
  recipe changed without a bump.
- If no published revision exists, CI builds the component.

When a component's build reproduces, the domain also commits the
expected sha256 of each output file, as stagex does in its `digests/`
directory. A bump then changes two lines: the version and the
expected digests. A rebuild by anyone must produce the same digests.
Components that do not reproduce yet skip this check, and their
`README.md` says so.

## The channel tree

Components go to `releases.liken.sh` under `components/`, beside the
release directories:

```
components/
  kernel/
    7.2.6/
      source/
        linux-7.2.6.tar.xz
        sha256sums.asc
      1/
        recipe.tar.zst
        vmlinuz
        modules.tar.zst
        config
        component.yaml
        attestation.sigstore.json
      2/
        ...
  k3s-root/
  k3s/
  ...
```

- `source/` holds the upstream source files once for each upstream
  version. It is the GPL source offer, and it is the first mirror
  for every build of that version.
- `<revision>/` holds one build. `recipe.tar.zst` is the component's
  directory from the repository at the commit that built it. The GPL
  counts the scripts that control a build as part of the source, so
  the recipe completes the offer.
- `component.yaml` records the name, the version, the revision, the
  recipe hash, the sha256 of each output file, the digest and
  signers of each stagex image, the commit, and the URL of the
  GitHub Actions run.
- `attestation.sigstore.json` is the signed attestation, so a person
  can verify a file without GitHub's API.

The rules for releases also apply to components. CI never overwrites
a published revision. A publish uploads the files first and
`component.yaml` last, so a component with no `component.yaml` does
not exist yet.

The old `sources/` tree stays. Published releases are immutable, and
their `LICENSES.md` gives `https://releases.liken.sh/sources/...`
URLs as the source offer for binaries that are already installed on
machines. CI stops writing to `sources/`, and a `README.md` in it
gives the new location. The index page in `releases/index.go` learns
the `components/` tree.

## Attestations

The build job attests each output file with
[`actions/attest`](https://github.com/actions/attest). The action
signs a statement with the file's sha256, the commit, and the
workflow. For a public repository, it records the statement in
GitHub's attestation API and in the public Sigstore log. The
publish step copies the Sigstore bundle into the component's
directory.

A person checks a file with one of these commands:

```sh
gh attestation verify vmlinuz --repo liken-sh/liken
gh attestation verify vmlinuz --bundle attestation.sigstore.json --repo liken-sh/liken
```

An OS release also gets an attestation for each of its artifacts.
Its `release.yaml` names the component revisions that it contains,
so a person can follow a release artifact back to each component,
then to its recipe, its sources, and its stagex images.

## The OS build uses published components

`make all` and `make release` do not compile components. For each
component, the OS build:

1. Looks for the pinned revision in the local cache, keyed by the
   recipe hash.
2. If the cache does not have it, downloads it from the channel and
   verifies the attestation and the digests in `component.yaml`.
3. Writes the files to `<domain>/dist/<version>/`, where the rest of
   the build reads them today.

Step 3 keeps the contract between the domains and the image build.
So the migration can move one component at a time, and the image
build does not change.

A pull request can bump a component. Fork pull requests get no
secrets, so they cannot publish. When the channel does not have the
pinned revision, the driver builds the component in the pull
request's run and passes it to the OS build and the smoke drills. A
push to `main` builds and publishes it. In the same workflow run,
the component job is a `needs:` of the OS job, so the OS build never
starts before the component is published.

The jobs keep the secrets small:

- The build job has no secrets. It produces the files and the
  attestation.
- A separate publish job holds the channel key. It takes the build
  job's output and uploads it.
- Every third-party action is pinned by commit SHA. The open problem
  [Pin CI executable inputs](open-problems/ci-executables-need-immutable-pins.md)
  covers the actions that the other workflows use.

CI uses GitHub's standard runners, which are free for public
repositories. If a kernel or `k3s-root` build is too slow there, a
drop-in runner service such as Depot, Blacksmith, or Namespace
changes one `runs-on:` line. The attestation and the reproducible
digests make the choice of runner a detail.

## The kernel config

The kernel config is the largest risk in this milestone. A wrong
option can remove a driver from every machine. Four checks control
the risk.

1. **The first build uses the config that works today.** The first
   `liken` kernel builds `7.2.6` with the config from Canonical's
   `7.2.6` mainline build. The only changes are the module-signing
   key and the lines that name Canonical's certificate files. The
   smoke drills and liken-1 then run a kernel whose config differs
   from the current one only in those lines, and whose module list
   is the same. CI proves both with a diff.
2. **A committed list of required options.** `kernel/required.config`
   lists each option that `liken` depends on, with `=y` or `=m`. The
   list comes from k3s's
   [`check-config.sh`](https://github.com/k3s-io/k3s/blob/main/contrib/util/check-config.sh),
   from `image/boot-modules.conf` and the feature `modules.conf`
   files, and from options that the code names, such as
   `CONFIG_SQUASHFS=y` in `image/build.sh`. The build fails if the
   built config does not contain an option from the list.
   `make olddefconfig` silently drops an option that upstream
   renamed, and this check catches that.
3. **Each bump shows its config diff.** The bump pull request
   includes a report: the options that the new version added and
   their defaults, the options that disappeared, and the options
   whose value changed. A person reviews it before merge.
4. **The drills.** `make smoke-uefi`, `make smoke-bios`, and the
   rollout to liken-1 stay the last check.

Module signing affects reproducibility. The kernel's
[reproducible builds guide](https://docs.kernel.org/kbuild/reproducible-builds.html)
says that `CONFIG_MODULE_SIG_ALL` makes a new temporary key for each
build, and the modules then do not reproduce. The guide gives a
method with a persistent key: build with `CONFIG_MODULE_SIG_KEY`
empty and `CONFIG_MODULE_SIG_ALL` off, sign the modules in a
separate step, publish the detached signatures as sources, and
attach them in a second build. The build also sets
`KBUILD_BUILD_TIMESTAMP`, `KBUILD_BUILD_USER`, and
`KBUILD_BUILD_HOST`, and maps the source path out of the debug
information.

Revision 1 can use an unsigned build or a temporary key. It must not
enforce signatures, because the image has no Secure Boot yet. The
persistent key, where it is kept, and who can use it belong to the
Secure Boot milestone.

## The order of the work

Each step ends with the smoke drills green and a release rolled to
liken-1.

1. **The spike.** Check that stagex's Go matches the Go version that
   k3s `v1.36.4+k3s1` requires. Check that stagex ships the static
   libseccomp, sqlite, and zlib that k3s links. Time a cold kernel
   build and a buildroot build on a standard runner.
2. **The driver, with a data component.** Build `hwdata` and `trust`
   through the driver. These components have no compile step, so
   they prove the driver, the channel tree, the attestation, and the
   OS build's download path.
3. **The kernel.** Use the first config check above. After this
   step, Canonical's archive is no longer a source.
4. **The static userland.** Build `e2fsprogs`, `nfs-utils`,
   `open-iscsi`, `wpa-supplicant`, and `tzdata` in stagex images.
   After this step, Alpine, Debian, and gokrazy are no longer
   sources.
5. **The bootloaders.** Build GRUB for both the `pc` and `efi`
   platforms, and systemd-boot from the systemd source. After this
   step, the Ubuntu archive is no longer a source.
6. **k3s.** Build `k3s-root`, then `k3s` against it, and retire the
   `xtables` domain.
7. **The data components.** Move `linux-firmware`, `microcode`, and
   the flux seed to the driver, as components that verify and copy.
8. **The old tree.** Write the `README.md` in `sources/` and remove
   the old upload step from the release workflow.

## Failures and what the design does about them

| Failure | What happens |
| --- | --- |
| A fork pull request bumps a component | The driver builds it in the run. Nothing is published. |
| A bump and its first use are in the same push | The OS job `needs:` the component job. |
| An upstream download site is down | Builds try the channel's `source/` directory first. Only a new version needs the upstream site. |
| Docker Hub rate-limits the runner, or stagex deletes an image | Builds pull from the copy on `ghcr.io/liken-sh`, by digest. |
| The channel is down | The OS build uses the local and CI caches. Only a new revision needs the channel. |
| A stagex maintainer's key changes | Signature verification fails, and a person updates the committed keys in a reviewed commit. |
| `olddefconfig` drops a needed option | The `required.config` check fails the build. |
| A kernel CVE fix needs a new kernel | The bump builds the kernel, which is slower than a download. The runner choice and cached layers control the time. |

## Decisions

- **The channel, not ghcr, holds the components.** An attestation
  identifies a file by its sha256, so the host does not change the
  proof. The channel already has the source offer, the rule that
  forbids a republish, and plain `curl` downloads. ghcr with `oras`
  would put the story on two hosts and add a client to the build.
  There is also a
  [report](https://github.com/sbomify/sbomify/issues/1530) that
  `push-to-registry` does nothing on ghcr. That report is not
  reproduced here.
- **`components/` on the channel, and no such directory in the
  repository.** The channel root holds releases, and
  `releases/index.go` reads its keys. A prefix keeps components apart
  from releases. In the repository, each component stays in its own
  domain directory.
- **A revision number, not a hash, in the path.** A revision is a
  declaration that a person can read and review. The recipe hash
  checks the declaration.
- **stagex for the build tools, not for the shipped files.** The
  stagex binaries link to shared libraries. GRUB's modules are the
  exception because they are freestanding code, but `liken` builds
  GRUB too, so that the claim has no exceptions.
- **A published toolchain, not our own bootstrap.** Running the
  bootstrap from a seed would take many hours for each build, and it
  would add a chain to maintain. stagex already publishes that chain
  with receipts. The toolchain is one pinned digest, so the design
  can replace it later without a change to any component recipe.
- **Upstream build systems run unchanged.** Rewriting buildroot's
  output as about 15 recipes of our own would move us away from the
  userland that k3s tests with.
- **Not Nix, not a buildroot for the whole OS, and not Yocto.** Each
  one solves the problem, but a reader must learn the framework to
  understand the build. Nix also depends on nixpkgs, which is another
  distro.
- **Not a distro build service.** openSUSE's OBS, Fedora's COPR, and
  Launchpad build packages in a distro's format and on a distro's
  base.
- **Not a self-hosted runner.** A fork pull request can run any code
  on the runner, and GitHub warns against self-hosted runners for
  public repositories.

## Open questions

- Does stagex's Go match the Go version that each k3s release needs?
  Kubernetes pins its Go version closely. If stagex is late, the k3s
  bump waits, or `liken` builds that Go version as a component of its
  own.
- Does stagex ship the static libraries that k3s links (libseccomp,
  sqlite, zlib) and that the current Alpine builds link (libnl,
  libtirpc, kmod, libeconf)? `libnl` was not in the stagex package
  list on 2026-09-25.
- How long do the builds take on a standard runner? The estimates in
  this plan are not measured: about an hour for the kernel, one to
  two hours for `k3s-root`, and less for k3s.
- Does the `k3s-root` build reproduce? buildroot marks
  `BR2_REPRODUCIBLE` as experimental in `2025.02.14`, and its help
  text says it works only with the same output directory.
- Should `liken`'s own Go programs (`init`, the operators, the CLI)
  build with stagex's Go too? CI installs Go with `actions/setup-go`
  today.
- The flux CLI is a build tool that renders the seed. Where does it
  come from under this design: a stagex image, a component, or the
  flux project's release with its cosign signature?

## Sources

All checked on 2026-09-25.

- stagex: <https://stagex.tools/>, the package list at
  <https://stagex.tools/packages/>, the documentation at
  <https://docs.stagex.tools/overview/>, and the repository at
  <https://codeberg.org/stagex/stagex> (the `e2fsprogs`, `iptables`,
  and `grub` recipes under `packages/user/`, and the `Makefile`'s
  `verify` and `digests` targets).
- GitHub's attestation action: <https://github.com/actions/attest>
  (`subject-path`, at most 1024 subjects, storage in GitHub's API and
  in public Sigstore for public repositories).
- `gh attestation verify`:
  <https://cli.github.com/manual/gh_attestation_verify>.
- The report about `push-to-registry` on ghcr:
  <https://github.com/sbomify/sbomify/issues/1530>.
- k3s's build: `scripts/download`, `scripts/build`, and
  `contrib/util/check-config.sh` in <https://github.com/k3s-io/k3s>.
- k3s-root: `Dockerfile`, `scripts/download`, and `buildroot/config`
  in <https://github.com/k3s-io/k3s-root>.
- buildroot `2025.02.14`, `Config.in`: `BR2_REPRODUCIBLE`,
  `BR2_PRIMARY_SITE`, and `BR2_PRIMARY_SITE_ONLY`.
- The kernel's reproducible builds guide:
  <https://docs.kernel.org/kbuild/reproducible-builds.html>.
