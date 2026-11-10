# 95. The kernel, the bootloaders, and k3s build from source

Milestone 95. Proposed 2026-11-10. Nothing is built. It holds the part
of [milestone 72](completed/72-the-os-builds-from-source.md) that was
not built when that milestone closed: a `liken` build of the kernel,
GRUB, systemd-boot, `k3s-root`, and `k3s`, the data components, the
`components/` tree on the release channel, and the toolchain that
builds them. The design below is milestone 72's design for those
parts, as written on 2026-09-29, with each fact about today's build
checked again on 2026-11-10.

## What is built

The static programs of the OS build from source. Under
[root plan 88](../../plans/completed/88-one-way-to-build-each-kind-of-thing.md),
buildroot builds `mke2fs`, `mount.nfs`, `wpa_supplicant`, tzdata, and
open-iscsi as static musl programs from the BR2_EXTERNAL tree in
`userland/` (`userland/os.config`), from source tarballs that
buildroot's `.hash` files pin. "Static programs" in the root
`AGENTS.md` states that form. Each file of an OS release is signed
keyless with cosign (`releases/sign.sh`), and each release publishes a
CycloneDX SBOM (`releases/sbom/`). Milestone 72's status paragraph
names the commits.

## Open questions

- **Is stagex still the toolchain?** The design below takes its build
  tools from stagex. buildroot is now the project's form for static
  programs, and it builds its own musl toolchain and every library
  from pinned source tarballs, which removes the need for stagex's
  static libraries in those programs. `k3s-root` is a buildroot
  project too. The kernel, GRUB, systemd-boot, and `k3s` still need a
  host compiler and a Go toolchain from somewhere. This question needs
  the maintainer's decision before step 1 below.
- **How a component's build is attested.** The design below attests
  each output with `actions/attest`. The OS release's files are signed
  keyless with cosign instead, with Sigstore bundles beside each file
  and the commands in the `releases` skill. A component's build could
  use the same signer.
- **Attestations on ghcr.** The `push-to-registry` report under
  "Decisions" applies to images. The first image attestation checks
  it.
- **Does stagex's Go match the Go version that each k3s release
  needs?** Kubernetes pins its Go version closely. If stagex is late,
  the k3s bump waits, or `liken` builds that Go version as a
  component of its own.
- **Does stagex ship the static libraries** that k3s links
  (libseccomp, sqlite, zlib)? On 2026-09-25 the same question covered
  the libraries that the Alpine builds linked (libnl, libtirpc, kmod,
  libeconf), and `libnl` was not in the stagex package list. buildroot
  builds those programs now.
- **How long do the builds take on a standard runner?** The
  estimates in this plan are not measured: about an hour for the
  kernel, one to two hours for `k3s-root`, and less for k3s.
- **Does the `k3s-root` build reproduce?** buildroot marks
  `BR2_REPRODUCIBLE` as experimental in `2025.02.14`, and its help
  text says it works only with the same output directory.
- **Should `liken`'s own Go programs build with stagex's Go too?**
  That covers `init`, the operators, and the CLI. CI installs Go
  with `actions/setup-go` today. A yes brings the operator images
  inside the claim.
- **Where does the flux CLI come from?** The CLI is a build tool
  that renders the seed. Under this design, it can come from a
  stagex image, from a component, or from the flux project's release
  with its cosign signature.

## The problem

These domains of the OS ship binaries that another project built.
Each pin's `pin.toml` names the file and its digest:

| Domain | Where the bytes come from |
| --- | --- |
| `kernel` | Canonical's mainline archive: a `.deb` that Canonical built from the upstream tree, with Ubuntu's generic config (`kernel/pin.toml`) |
| `grub` | Ubuntu `.deb` packages (`grub/pin.toml`) |
| `systemd-boot` | an Ubuntu `.deb` (`systemd-boot/pin.toml`) |
| `k3s` | the k3s project's release binary (`k3s/pin.toml`) |
| `xtables` | the k3s-root project's release binary, which buildroot builds (`xtables/pin.toml`) |
| `linux-firmware` | kernel.org's release tarball |
| `microcode` | Intel's GitHub release, and the AMD files from `linux-firmware` |
| `hwdata` | the hwdata project's `pci.ids` |
| `trust` | the curl project's extract of Mozilla's CA list, in the `trust` component at the top of the repository |
| `flux` | manifests that the flux CLI renders at build time; the CLI does not ship |

Two problems follow from this:

- Nobody can rebuild the kernel, GRUB, systemd-boot, k3s, or the
  xtables binaries and compare the result with what `liken` ships. We
  trust each of those binaries because of where we downloaded it.
- Secure Boot with `liken`'s own keys needs a `liken` kernel build.
  Under lockdown, the kernel loads only modules that are signed with
  a key compiled into it. Canonical's build compiles in Canonical's
  key.

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
- the operator and driver images, until their builds use the same
  toolchain (see the open questions)

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
directly. On 2026-09-25 stagex also packaged GRUB 2.14, iptables
1.8.13, e2fsprogs 1.47.4, containerd, runc, and systemd 260.2. Most of
those packages link to shared libraries, for example `e2fsprogs`
configures with `--enable-elf-shlibs` and `iptables` with
`--enable-shared`. The `liken` image has no libc and no shared
libraries, so those binaries do not start on a `liken` machine.
`liken` builds its own static versions and uses the stagex recipes as
a reference.

Static libraries such as musl and openssl come from stagex and are
linked into the programs that `liken` ships. The claim counts them as
part of the toolchain, in the same way as `libgcc`. Their source goes
on the channel with the component that links them.

For each stagex image that a component uses, the build:

1. Pins the image by digest.
2. Verifies the maintainers' PGP signatures on that digest. The
   maintainers' public keys are committed in the repository, and the
   signatures come from stagex's signature repository.
3. Copies the image to `ghcr.io/liken-sh`, so a Docker Hub rate limit
   or a deleted image does not stop a build.
4. Records the digest and the signers in the component's build
   record.

## Components that have their own build system

Some upstream projects ship a full build system. For those, the
`Dockerfile` runs the upstream build scripts unchanged, inside a
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
  `k3s` component supplies our `k3s-root` build in its place, and its
  `[depends]` names `k3s-root`.

The `xtables` domain ships binaries from the `k3s-root` output today,
so it merges into the `k3s-root` component.

buildroot's `BR2_PRIMARY_SITE` option makes buildroot try one
download site before each package's own site. The `k3s-root` build
sets it to the component's `source/` directory on the channel.

## Where each output goes

Root plan 69's "Where each output goes"
([plan 69](../../plans/completed/69-one-repository-builds-every-component.md))
gives the host of each output and the order of a publish.

### The channel tree

Pinned components that feed the OS go to `releases.liken.sh` under
`components/`, beside the release directories:

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
- `component.yaml` is the build record. It records the name, the
  version, the revision, the recipe hash, the sha256 of each output
  file, the digest and signers of each stagex image, the commit, and
  the URL of the GitHub Actions run.
- `attestation.sigstore.json` is the signed attestation, so a person
  can verify a file without GitHub's API.

The build record of an image is the same data, written as labels on
the image. A pinned base image carries its recipe hash as a label,
and CI reads that label for the comparison in plan 69's "A pinned
component builds when its revision is new".

The old `sources/` tree stays. Published releases are immutable, and
their `LICENSES.md` gives `https://releases.liken.sh/sources/...`
URLs as the source offer for binaries that are already installed on
machines. CI stops writing to `sources/`, and a `README.md` in it
gives the new location. The index page in `releases/index.go` learns
the `components/` tree.

## Attestations

The build job attests each output with
[`actions/attest`](https://github.com/actions/attest). The action
signs a statement with the output's sha256, the commit, and the
workflow. For a public repository, it records the statement in
GitHub's attestation API and in the public Sigstore log. For a file
on the channel, the publish step copies the Sigstore bundle into the
component's directory. For an image or a deploy artifact, the
statement names the digest.

A person checks a file with one of these commands:

```sh
gh attestation verify vmlinuz --repo liken-sh/liken
gh attestation verify vmlinuz --bundle attestation.sigstore.json --repo liken-sh/liken
```

An OS release also gets an attestation for each of its artifacts.
Its `release.yaml` names the component revisions that it contains,
so a person can follow a release artifact back to each component,
then to its recipe, its sources, and its stagex images.

The second open question above asks whether this section still holds,
because each file of an OS release is now signed keyless with cosign.

## The OS build uses published components

`make all` and `make release` do not compile pinned components. For
each one, the OS build:

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

- The build job has no secrets. It produces the outputs and the
  attestation.
- A separate publish job holds the channel key and the registry
  token. It takes the build job's output and uploads it.
- Every third-party action is pinned by the commit of a release, as
  every workflow of the repository does today (`ci/actions.go`).

CI uses GitHub's standard runners, which are free for public
repositories. If a kernel or `k3s-root` build is too slow there, a
drop-in runner service such as Depot, Blacksmith, or Namespace
changes one `runs-on:` line. The attestation and the reproducible
digests make the choice of runner a detail.

## The kernel config

The kernel config is the largest risk in this milestone. A wrong
option can remove a driver from every machine. Four checks control the
risk.

1. **The first build uses the config that works today.** The first
   `liken` kernel builds the pinned version in `kernel/VERSION` with
   the config from Canonical's mainline build of the same version. The
   only changes are the module-signing key and the lines that name
   Canonical's certificate files. The smoke drills and `liken-1` then
   run a kernel whose config differs from the current one only in
   those lines, and whose module list is the same. CI proves both with
   a diff.
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
   rollout to `liken-1` stay the last check.

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

Each step follows the rule in plan 69's "The order of the work". The
numbers in parentheses are the step numbers of milestone 72.

1. **The spike** (6). Check that stagex's Go matches the Go version
   that the k3s release in `k3s/VERSION` requires. Check that stagex
   ships the static libseccomp, sqlite, and zlib that k3s links. Time
   a cold kernel build and a buildroot build on a standard runner.
2. **The channel components, with a data component** (7). Build
   `hwdata` and `trust` through the driver. These components have no
   compile step, so they prove the channel tree, the attestation, and
   the OS build's download path.
3. **The kernel** (8). Use the first config check above. After this
   step, Canonical's archive is no longer a source.
4. **The bootloaders** (10). Build GRUB for both the `pc` and `efi`
   platforms, and systemd-boot from the systemd source. After this
   step, the Ubuntu archive is no longer a source.
5. **k3s** (11). Build `k3s-root`, then `k3s` against it, and retire
   the `xtables` domain.
6. **The data components** (12). Move `linux-firmware`, `microcode`,
   and the flux seed to the driver, as components that verify and
   copy.
7. **The old tree** (13). Write the `README.md` in `sources/` and
   remove the old upload of `sources/` from `releases/publish.sh`.

## Failures and what the design does about them

| Failure | What happens |
| --- | --- |
| A bump and its first use are in the same push | The OS job `needs:` the component job. |
| An upstream download site is down | Builds try the channel's `source/` directory first. Only a new version needs the upstream site. |
| Docker Hub rate-limits the runner, or stagex deletes an image | Builds pull from the copy on `ghcr.io/liken-sh`, by digest. |
| The channel is down | The OS build uses the local and CI caches. Only a new revision needs the channel. |
| A stagex maintainer's key changes | Signature verification fails, and a person updates the committed keys in a reviewed commit. |
| `olddefconfig` drops a needed option | The `required.config` check fails the build. |
| A kernel CVE fix needs a new kernel | The bump builds the kernel, which is slower than a download. The runner choice and cached layers control the time. |

## Decisions

These are milestone 72's decisions of 2026-09-29.

- **The channel, not ghcr, holds the OS's pinned components.** An
  attestation identifies a file by its sha256, so the host does not
  change the proof. The channel already has the source offer, the
  rule that forbids a republish, and plain `curl` downloads. ghcr
  with `oras` would put the story on two hosts and add a client to
  the build. There is also a
  [report](https://github.com/sbomify/sbomify/issues/1530) that
  `push-to-registry` does nothing on ghcr. That report is not
  reproduced here. Images and deploy artifacts go to ghcr, because a
  cluster pulls them from a registry.
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
  distro. Root plan 88 uses buildroot for the static programs only,
  and keeps this decision for the rest of the OS.
- **Not a distro build service.** openSUSE's OBS, Fedora's COPR, and
  Launchpad build packages in a distro's format and on a distro's
  base.
- **Not a self-hosted runner.** A fork pull request can run any code
  on the runner, and GitHub warns against self-hosted runners for
  public repositories.

## Sources

Plan 69's "Sources" lists stagex, which this plan cites too.

Checked on 2026-09-25:

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
