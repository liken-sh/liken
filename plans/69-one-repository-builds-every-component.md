# 69. One repository builds every component

Milestone 69. Proposed 2026-09-25. Revised 2026-09-28: the revision
moves the operators, the drivers, `brand`, and `.agents` into this
repository, and adds one release tag for all of them.

Today `liken` downloads most of the programs in its image as binaries
that other projects built, and its operators and drivers live in
eleven other repositories that copy each other's code. This milestone
does two things with one model:

- It moves every component that the project ships into this
  repository. A component is a directory with a `package.toml`, and
  CI reads those files into one dependency graph.
- It makes `liken` build every program in its release artifacts from
  source. The build tools come from stagex, and each build is pinned
  and signed.

The work runs in two stages. The first stage moves the repositories
together and builds the component graph. The second stage moves the
OS's vendored domains to stagex builds, and it starts after the first
stage is done.

A component takes its version in one of two ways. A **pinned**
component, such as the kernel or `mpv`, declares an upstream version
and a revision, and it builds only when that revision is not
published yet. A **tracked** component, such as an operator or the
OS itself, takes the repository's CalVer release tag, and it builds
only when its files or a dependency changed since its last published
version. Everything else is shared: the directory, the file, the
graph, the publish steps, and the attestations.

## The problem

### The vendored binaries

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

### The split repositories

On 2026-09-28 the project shipped from twelve repositories: `liken`,
nine operator and driver repositories, `brand`, and `.agents`. A
review of the three days before that date found that most of the
work went into keeping copies in agreement:

- Six operators each carry their own `objectcache.go`, and every
  operator carries its own `apiclient.go`, `watch.go`, `cdi.go`, and
  a `ci.yaml` of about 300 lines. `liken` has a seventh cache in
  `kubernetes/informer`. On 2026-09-27 one fix to the cache landed
  as four separate commits with four separate tests.
- `media-operator` and `library-operator` carry the same `mqtt.go`,
  with a difference of one comment, and near copies of `leader.go`.
  `equipment-operator` carries a third copy of each.
- Twelve repositories pin `brand` through a git submodule. The pin
  in `liken/go.mod` was one month older than the pin in
  `liken/docs`.
- `display-operator`'s release builds the `vulkan`, `vaapi`,
  `ffmpeg`, and `mpv` base images under its own version. The
  consumers pin those images by hand, and the pins differed:
  `library-operator` used `ffmpeg:2026.09.11-001` and
  `media-operator` used `ffmpeg:2026.09.16-001`.
- A cluster that follows a development build must pin the manifests
  to the full 40-character commit sha, because `git fetch` by sha
  needs all forty characters, and the image to the build's version.

The git history of the ten repositories that release gives the size
of the cost. The counts below cover `liken`, the seven operators, and
the two CSI drivers, and they were taken on 2026-09-28. A release
session is a group of release tags with less than two hours between
one tag and the next.

| Window | Release tags | Sessions | Sessions that tag more than one repository | Tags in those sessions |
| --- | --- | --- | --- | --- |
| 2026-07-11 to 2026-09-28 | 445 | 114 | 38 (33%) | 65% |
| 2026-08-29 to 2026-09-28 | 255 | 59 | 27 (45%) | 73% |
| 2026-09-21 to 2026-09-28 | 65 | 11 | 7 (63%) | 93% |

- In the 30 days to 2026-09-28, four sessions tagged `liken` and all
  nine other repositories.
- In the same 30 days, 11 sessions tagged `liken`, and 9 of them also
  tagged operators or drivers.
- `media-operator` and `library-operator` released in the same
  session 22 times in those 30 days.

The grouping by time counts more sessions than the dependencies
require. One person often works across several repositories in one
sitting, so some of the tags are together by chance. The tags alone
cannot separate the two cases.

The commits show the duplicated work more directly. In the same 30
days, 163 of 999 commits (16%) have a subject line that is identical
to a commit in another repository. A fix whose subject is worded
differently in each repository does not match, so 16% is a lower
bound. The first operator repositories started on 2026-08-16, in
week 33. Before then only `liken` existed, and the rate was 0%. Since
then it has been between 10% and 25% in every full week:

| ISO week | Commits | Subject repeated in another repository |
| --- | --- | --- |
| 2026-W33 | 55 | 6 (10%) |
| 2026-W34 | 183 | 21 (11%) |
| 2026-W35 | 171 | 41 (23%) |
| 2026-W36 | 291 | 45 (15%) |
| 2026-W37 | 188 | 33 (17%) |
| 2026-W38 | 202 | 51 (25%) |
| 2026-W39 | 239 | 29 (12%) |

Most repeated subjects come from changes that apply to every
repository: the voice rules, the agent instructions, the guides as
Agent Skills, Prometheus metrics, and the CI workflow. The project
expects more changes of that kind. In one repository, each one is a
single commit.

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
- the operator and driver images, until their builds use the stagex
  toolchain (see the open questions)

Those images can move to the same system later. The claim must name
its scope in every place it is published.

Firmware and microcode are binary files from their vendors. Nobody
can build them, and the claim does not say that anyone did. Their
components verify and copy the vendor's files, and their attestations
record which upstream release they came from.

## One repository

The repository is `liken-sh/liken`. Its top level lists the things
that ship, and each one keeps its own domains inside it:

```
liken/                     the OS: init/, image/, kernel/, k3s/, machine-operator/, ...
kubernetes/                the client, the watch, and the cache that every operator imports
brand/                     the theme, voice.md, crdref, and linkcheck
vulkan/  vaapi/  ffmpeg/  mpv/  weston/
audio-operator/  bluetooth-operator/  display-operator/  equipment-operator/
media-operator/  library-operator/  people-operator/
git-csi-driver/  per-node-csi-driver/
plans/
AGENTS.md  skills/
```

- `liken/` holds today's tree. Inside it, the paths do not change:
  this plan's `kernel/`, `image/`, and `releases/` are
  `liken/kernel/`, `liken/image/`, and `liken/releases/`.
- `kubernetes/` leaves `liken/` because the operators import it too.
  It replaces the seven caches, and `bus/` or a similar directory
  replaces the copies of `mqtt.go` and `leader.go` once a second
  user needs them.
- `plans/` moves to the top, because the plans now cover every
  component.
- `brand/` comes in. The sites read it from the tree, so no
  submodule pin can lag.
- `.agents` comes in. Its `AGENTS.md` becomes the root `AGENTS.md`,
  and its skills go to `skills/`.

Four repositories stay out:

- `corrosion` is a fork of superfly/corrosion, with its own upstream
  and its own branches.
- `plugins` stays the skill catalog.
- `log` is the devlog site.
- `liken-dev-cluster` stays its own repository.

### The history moves with the code

`liken`'s own tree moves into `liken/` with `git mv` in one commit.
Its history is not rewritten, so the commit SHAs that published
releases and the channel name stay valid.

Each other repository comes in with its full history. `git
filter-repo` rewrites the repository so that every path is under the
component's directory, and prefixes every tag with the component's
name. Then a merge with `--allow-unrelated-histories` joins it to
`main`:

```sh
git filter-repo --to-subdirectory-filter display-operator --tag-rename '':'display-operator/'
git merge --allow-unrelated-histories display-operator/main
```

The tag prefix is necessary, because every operator repository has
tags such as `2026.09.28-001`, and the names collide without it.
The imported commits get new SHAs. The old SHAs still resolve in the
archived repositories, so a development version that names an old
SHA still points at a real commit. On 2026-09-28 the imports total
about 1,280 commits: the nine operator and driver repositories,
`brand`, and `.agents`.

### One docs site

The project publishes one site, `liken.sh`. Each component's manual
is a section at `liken.sh/<component>/`, for example
`liken.sh/bluetooth/`, built from the component's own `docs/`
directory together with the rest of the site.

The names of the old sites stay in use. Each one is also a name that
a cluster shows: `bluetooth.liken.sh`, `display.liken.sh`, and the
other operator sites are API groups, and `git.liken.sh` and
`per-node.liken.sh` are CSI driver names. A person who sees one of
those names in a cluster and opens it in a browser must arrive at
that component's manual. A small HTTP service answers every
`*.liken.sh` name that has no record of its own with a 301 to
`liken.sh/<name>/`. `www`, `releases`, and `log` keep their own
records. One wildcard rule covers every name, including
the API groups of components that do not exist yet. GitHub Pages
cannot do this, because it serves one custom domain for each
repository and does not redirect for another domain.

The old repositories are archived after the move, each with a
`README.md` that names its new directory. The project has no users
outside its maintainers yet, so no install path has to keep working
across the move.

`git-csi-driver`, `per-node-csi-driver`, and `people-operator` do not
need `liken` to run. They stay in the repository, and each one must
install on any Kubernetes cluster. A component moves out when a user
wants only that component and the repository is in the way. The CSI
driver names `git.liken.sh` and `per-node.liken.sh` can change
cheaply until a cluster outside the project holds volumes that name
them.

## A component

A component is a directory with a `package.toml`. CI finds every
component with `**/package.toml`, so a component can sit at the top
level, such as `mpv/`, or inside the OS, such as `liken/kernel/`. The
directory name is the component name, the image name, and the name
on the channel.

A pinned component declares its component in three files:

- `package.toml`: the upstream version, the `liken` revision, the
  license, the dependencies, the outputs, and each source file with
  its sha256 and its mirrors. The format follows stagex's
  `package.toml`, with two added tables, `[depends]` and
  `[outputs]`.
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

[outputs]
channel = true
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

A tracked component has no `version` and no `revision`. Its version
is the repository tag, and its source is its own directory:

```toml
[package]
name = "media-operator"
license = "Apache-2.0"

[depends]
components = ["kubernetes", "mpv", "vulkan"]

[outputs]
images = ["media-operator", "media-operator-api", "media-operator-player"]
deploy = "deploy/"
```

`[depends]` names other components. `[outputs]` names where the build
goes: `channel` for files on `releases.liken.sh`, `images` for
images on `ghcr.io/liken-sh`, and `deploy` for a directory of
manifests that the build pushes as an OCI artifact. A component can
have more than one kind of output.

One driver builds any component from these files. It reads the
graph, fetches and verifies the sources, runs the builds with
BuildKit, writes the build record, and publishes. The `licensing`
domain reads the same `package.toml` files for the notices and the
source offer, so the list of sources is written in one place only.

The `latest.sh` scripts stay one per domain. Each upstream announces
its releases in a different way, so each domain keeps its own script
to find the newest version.
The `bump-components` skill and `make versions` keep working on top
of them. A bump now also writes a new revision and, where the build
reproduces, the new expected digests.

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
4. Records the digest and the signers in the component's build
   record.

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
  `k3s` component supplies our `k3s-root` build in its place, and its
  `[depends]` names `k3s-root`.

The `xtables` domain ships binaries from the `k3s-root` output today,
so it merges into the `k3s-root` component.

buildroot's `BR2_PRIMARY_SITE` option makes buildroot try one
download site before each package's own site. The `k3s-root` build
sets it to the component's `source/` directory on the channel.

## The base images are pinned components

`vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston` are upstream software
that the project builds from source into container images. Each one
becomes a pinned component with its own directory, its own upstream
version, and its own revision. Its output is an image, not files on
the channel.

The operators build `FROM` the base in the tree, not from a published
tag. `docker buildx bake` gives each consumer a named build context
that points at the base's target:

```hcl
target "mpv" {
  context = "mpv"
}

target "media-operator-player" {
  context  = "media-operator"
  dockerfile = "Dockerfile.player"
  contexts = { mpv = "target:mpv" }
}
```

`FROM mpv` in `Dockerfile.player` then resolves to the `mpv` target's
output at the same commit. A change to a base reaches every consumer
at the next build, with no pin to move by hand.

## When a component builds

### A pinned component builds when its revision is new

The `liken` revision is the declaration. The first build of an
upstream version is revision 1. A change that alters the output but
keeps the upstream version increments the revision, for example a
kernel config change or a new stagex image. The pin in the domain
changes from `7.2.6-1` to `7.2.6-2`, and that one line is the bump.

CI enforces the rule with a recipe hash. The hash covers
`package.toml`, the `Containerfile`, every file that the
`Containerfile` adds, the digests of the stagex images, and the
published revisions of the components in `[depends]`. For each
pinned component, CI compares the hash with the one in the published
build record for the pinned revision:

- If a published revision exists and its hash matches, CI builds
  nothing and downloads the published output.
- If a published revision exists and its hash differs, CI fails. The
  recipe changed without a bump.
- If no published revision exists, CI builds the component.

Because the hash covers the dependencies, a bump cascades. When
`ffmpeg` goes from revision 3 to 4, `mpv`'s hash changes, and CI fails
until `mpv` bumps too. The `bump-components` skill writes the cascade
in the same commit: it walks the graph from the bumped component and
increments the revision of each pinned dependent. The commit touches
several `package.toml` files, and each new revision is a line that a
person reviews.

When a component's build reproduces, the domain also commits the
expected sha256 of each output file, as stagex does in its `digests/`
directory. A bump then changes two lines: the version and the
expected digests. A rebuild by anyone must produce the same digests.
Components that do not reproduce yet skip this check, and their
`README.md` says so.

### A tracked component builds when its paths changed

A tracked component's paths are its own directory plus the
directories of every component it depends on, followed through the
whole graph. On a release tag, CI does this for each tracked
component:

1. Reads the component's newest published release version from
   ghcr, or from the channel for the OS.
2. Diffs the component's paths from the git tag of that version to
   the commit of the new tag.
3. Builds and publishes the component if the diff is not empty, or
   if no git tag exists for that version.

The git tag of a version is the bare version for a release made in
this repository, and `<component>/<version>` for a release that the
component made before the move. So the first release after the move
diffs from each component's imported tag, and it does not rebuild a
component that did not change.

The diff starts from the component's own newest published version,
not from the previous repository tag. If a release fails halfway,
the component that did not publish keeps its older version, and the
next release diffs from there and builds it. The rule for a missing
git tag covers a new component and the first release after the move,
when the old tags are in the archived repositories.

A tracked component that did not change gets no new version. Its
cluster keeps the version it has, so its pods do not restart.

The dependencies are declared by hand in `[depends]`, and a missing
entry fails silently: the component does not rebuild, and it keeps
running an old base. CI checks the declaration against what the
tools report: `go list -deps` for Go imports inside the repository,
`cargo metadata` for path crates, and the `FROM` lines and the bake
contexts for images. A dependency that the tools find and
`[depends]` omits fails the build.

## Versions and tags

The repository has one tag format, CalVer `yyyy.mm.dd-nnn`, and it
applies to the whole repository. `nnn` is a three-digit serial within
the day, starting at `001`. The tag is the bare version, with no `v`
prefix, and it is lightweight. The tag is the release act. Everything
after it is verification.

A tag releases every tracked component that changed, each under the
tag's version, and every pinned component whose revision is not
published yet, each under its own version. After a release on
2026-10-02, the registry could show:

```
display-operator   2026.10.02-001   changed
media-operator     2026.10.02-001   changed, because mpv changed
mpv                0.41.0-2         pinned, new revision
audio-operator     2026.09.20-003   unchanged, keeps its version
```

CI also publishes a release record for each tag: every component and
its version at that tag. At release X, a tracked component's version
is its newest published version at or before X.

A push to `main` is a development build. It builds each tracked
component that changed in the push and each component that depends
on one. The version comes from `git describe` of the newest
repository tag, with a suffix: `2026.10.02-001-dev-017-abcdef01` is
17 commits past `2026.10.02-001`, at commit `abcdef01`. The count is
for the whole repository, not for the component, so it is not
contiguous for one component. It sorts after its release and before
the next one, and that is its only job. A development build never
moves `:latest`.

A lab release of the OS keeps its `-9xx` serial, and serial `000`
stays the working-tree channel, as the `release` skill describes.

## The OS is a tracked component

`liken/` is a tracked component. Its dependencies are the pinned
components it pulls into the image, `kubernetes/`, and its own
domains, such as `init/`, `machine-operator/`, and `image/`. Its
output is a release on the channel, with the smoke drills and the
publish that the `release` skill describes.

A tag releases the OS when any of those inputs changed since the last
OS release. So a tag that is meant to release one operator also
releases the OS if an OS change is waiting on `main`. An OS release
reboots every machine in a fleet that follows releases. The project
accepts this. Over time the OS changes the least, and the operators
change the most, so most tags do not include an OS release. When an
operator really needs a new OS, the same rule ships both together.
The goal is that no operator needs a specific OS release, but it is
not a rule that CI enforces.

## Where each output goes

| Output | Host | Tag or path |
| --- | --- | --- |
| Files of a pinned OS component | `releases.liken.sh/components/<name>/<version>/<revision>/` | the revision |
| The OS release | `releases.liken.sh/<version>/` | the repository tag |
| An image | `ghcr.io/liken-sh/<image>` | the component's version |
| A `deploy/` directory | `ghcr.io/liken-sh/<component>-deploy` | the component's version |

A publish uploads the outputs first and the record that makes them
visible last:

- On the channel, `component.yaml` goes last, so a component with no
  `component.yaml` does not exist yet.
- On ghcr, the deploy artifact goes last, after every image of the
  component. A cluster follows the deploy artifact, so it never
  applies manifests that name an image that is not pushed yet.

CI never overwrites a published revision or a published version.

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
and CI reads that label for the comparison above.

The old `sources/` tree stays. Published releases are immutable, and
their `LICENSES.md` gives `https://releases.liken.sh/sources/...`
URLs as the source offer for binaries that are already installed on
machines. CI stops writing to `sources/`, and a `README.md` in it
gives the new location. The index page in `releases/index.go` learns
the `components/` tree.

## How a cluster follows a component

A cluster follows each component through one `OCIRepository` and one
`ImagePolicy`. Flux's `OCIRepository` is GA at
`source.toolkit.fluxcd.io/v1` in Flux 2.9. It pulls a tarball of
manifests from a registry by tag, semver range, or digest, and a
`Kustomization` applies it the same way it applies a git checkout.

```yaml
apiVersion: image.toolkit.fluxcd.io/v1
kind: ImageRepository
metadata:
  name: display-operator
  namespace: flux-system
spec:
  image: ghcr.io/liken-sh/display-operator-deploy
  interval: 1h
---
apiVersion: image.toolkit.fluxcd.io/v1
kind: ImagePolicy
metadata:
  name: display-operator
  namespace: flux-system
spec:
  imageRepositoryRef:
    name: display-operator
  filterTags:
    pattern: '^(?P<v>\d{4}\.\d{2}\.\d{2}-\d{3})$'
    extract: "$v"
  policy:
    alphabetical:
      order: asc
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: display-operator
spec:
  interval: 1h
  url: oci://ghcr.io/liken-sh/display-operator-deploy
  ref:
    tag: 2026.10.02-001 # {"$imagepolicy": "flux-system:display-operator:tag"}
```

The same marker also sets the tag of each image in the
`Kustomization`, so the manifests and the images move in one commit.
The policy watches the deploy artifact, not an image, because the
deploy artifact is published last.

The pattern chooses the track:

- A cluster that follows releases uses the pattern above.
- A test cluster uses a pattern that admits development builds:
  `'^(?P<v>\d{4}\.\d{2}\.\d{2}-\d{3}(-dev-\d{3}-[0-9a-f]{8})?)$'`.
- A cluster that follows releases can follow one component's
  development builds, to test hardware that only that cluster has.
  The change is that one component's pattern, in one commit.

A development build pushes its deploy artifact under its development
version, so no cluster pins a commit sha.

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
- Every third-party action is pinned by commit SHA. The open problem
  [Pin CI executable inputs](open-problems/ci-executables-need-immutable-pins.md)
  covers the actions that the other workflows use.

CI uses GitHub's standard runners, which are free for public
repositories. If a kernel or `k3s-root` build is too slow there, a
drop-in runner service such as Depot, Blacksmith, or Namespace
changes one `runs-on:` line. The attestation and the reproducible
digests make the choice of runner a detail.

## The kernel config

The kernel config is the largest risk in the pinned half of this
milestone. A wrong option can remove a driver from every machine.
Four checks control the risk.

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

The work has two stages. Each step ends with CI green and a release
rolled to liken-1. A step that changes the OS also ends with the
smoke drills green.

### Stage 1: one repository

This stage moves the repositories together and builds the graph. It
changes how every component releases, and it removes the copies that
the numbers in "The split repositories" measure. It does not change
how any vendored domain gets its bytes.

1. **The move.** Move `liken`'s tree into `liken/`, and bring in the
   operators, the drivers, `brand`, and `.agents`, on a branch. Each
   component keeps its own `Makefile` targets. GitHub runs workflows
   only from `.github/workflows/` at the repository root, so the
   imported workflow files stop running. One root workflow replaces
   them for this step. It has one job for each component, a path
   filter on each job, and `needs:` from each consumer to the base
   images it builds on. It runs each component's `make` targets and
   publishes nothing. The branch merges when that workflow is green
   for every component.
2. **The graph and the tag.** Add a `package.toml` to each tracked
   component. Build the driver's graph, the path diff, the release
   record, and the development builds. Replace the hand-written root
   workflow with one that a generator writes from the `package.toml`
   files, and commit the generated file. CI fails if the committed
   workflow differs from what the generator writes.

   The publish job has a dry-run mode, and it runs in that mode on
   every push to the branch. It prints each component, whether it
   changed and why, the version it would get, and each image and
   artifact it would push. It also checks that the workflow can
   write to each `ghcr.io/liken-sh` package. Each package is linked
   to its old repository, and it accepts a push from this
   repository's workflows only after its settings grant this
   repository write access. The first real publish comes after the
   dry run is correct and every package grants access.
3. **The deploy artifacts and the fleets.** Push each component's
   `deploy/` as an OCI artifact. The `liken` side is finished and
   published before any fleet moves. Move liken-1 to
   `OCIRepository` and the new policies first, as the proof. Then
   move each fleet that follows releases.

   A fleet that follows releases through the old `GitRepository`
   pins must suspend flux for the `liken` components before the
   first real publish from this repository. The suspension covers
   the `Kustomization`s and the image automation. The new releases
   push new tags to the same images, and the image automation would
   write each new tag into a `GitRepository`'s `ref.tag`. The old
   repositories never get those git tags, so the `Kustomization`
   would fail when it resumes. The running pods do not change while
   flux is suspended. The fleet resumes each component when its pins
   move to the new `OCIRepository`.
4. **The shared module.** Replace the copies of the client, the
   watch, and the cache with `kubernetes/`, one operator at a time.
5. **The base images.** Make `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and
   `weston` pinned components, and make their consumers build
   `FROM` the tree.

### Stage 2: the OS builds from source

This stage starts after stage 1 is done. It moves the OS's vendored
domains to stagex builds, one component at a time, through the driver
and the graph that stage 1 built. Secure Boot needs this stage, and
the release numbers do not.

6. **The spike.** Check that stagex's Go matches the Go version that
   k3s `v1.36.4+k3s1` requires. Check that stagex ships the static
   libseccomp, sqlite, and zlib that k3s links. Time a cold kernel
   build and a buildroot build on a standard runner.
7. **The channel components, with a data component.** Build `hwdata`
   and `trust` through the driver. These components have no compile
   step, so they prove the channel tree, the attestation, and the OS
   build's download path.
8. **The kernel.** Use the first config check above. After this
   step, Canonical's archive is no longer a source.
9. **The static userland.** Build `e2fsprogs`, `nfs-utils`,
   `open-iscsi`, `wpa-supplicant`, and `tzdata` in stagex images.
   After this step, Alpine, Debian, and gokrazy are no longer
   sources.
10. **The bootloaders.** Build GRUB for both the `pc` and `efi`
    platforms, and systemd-boot from the systemd source. After this
    step, the Ubuntu archive is no longer a source.
11. **k3s.** Build `k3s-root`, then `k3s` against it, and retire the
    `xtables` domain.
12. **The data components.** Move `linux-firmware`, `microcode`, and
    the flux seed to the driver, as components that verify and copy.
13. **The old tree.** Write the `README.md` in `sources/` and remove
    the old upload step from the release workflow.

## Failures and what the design does about them

| Failure | What happens |
| --- | --- |
| A fork pull request bumps a component | The driver builds it in the run. Nothing is published. |
| A bump and its first use are in the same push | The OS job `needs:` the component job. |
| A pinned dependency bumps and its dependents do not | The dependents' recipe hashes differ from their published revisions, and CI fails until they bump. |
| `[depends]` omits a dependency | The check against `go list`, `cargo metadata`, and the `FROM` lines fails the build. |
| A release fails halfway | Each component that did not publish keeps its older version. The next tag diffs from that version and builds it. |
| The first release after the move | Each component diffs from its imported `<component>/<version>` tag. A component with no tag for its newest version builds. |
| A fleet on the old `GitRepository` pins sees the first release from this repository | Its image automation writes a git tag that the old repository does not have. Suspending the image automation and the `Kustomization`s before the first real publish prevents it. |
| The first real publish cannot push to a `ghcr.io/liken-sh` package | The dry run checks write access to every package before the first real publish. |
| A cluster sees a new tag before the images exist | It cannot. The deploy artifact is published after the images, and the policy follows the deploy artifact. |
| A tag meant for one operator also carries an OS change | The OS releases too. The project accepts this; see "The OS is a tracked component". |
| An upstream download site is down | Builds try the channel's `source/` directory first. Only a new version needs the upstream site. |
| Docker Hub rate-limits the runner, or stagex deletes an image | Builds pull from the copy on `ghcr.io/liken-sh`, by digest. |
| The channel is down | The OS build uses the local and CI caches. Only a new revision needs the channel. |
| A stagex maintainer's key changes | Signature verification fails, and a person updates the committed keys in a reviewed commit. |
| `olddefconfig` drops a needed option | The `required.config` check fails the build. |
| A kernel CVE fix needs a new kernel | The bump builds the kernel, which is slower than a download. The runner choice and cached layers control the time. |

## Decisions

- **The repository first, stagex second.** The numbers in "The split
  repositories" are a cost that the project pays every week. Stage 2
  pays for itself through Secure Boot and the claim, and it can wait
  until those are the priority. Stage 2 also uses the driver and the
  graph that stage 1 builds.
- **One repository, not twelve and not four.** The shared code
  crosses every line that a smaller split could draw: the cache is
  in the OS and in every operator, and the base images feed display,
  media, and library. Four domain repositories would still need a
  published module and pins between them. A smaller fix was also
  possible: one shared Go module in its own repository, reusable
  GitHub workflows, and a script that tags several repositories.
  That fix removes the copies of the plumbing. It does not make a
  change that applies to every repository into one commit, it does
  not remove the base-image pins, and a session still pushes up to
  ten tags.
- **A generated static workflow, not a dynamic matrix.** A job for
  each component, with `needs:` that follow the graph, makes GitHub
  draw the dependency graph on every run page, and a skipped job
  shows which component did not change. A matrix that a script
  computes shows a flat list and keeps the graph inside the script.
  A generator writes the static file from `package.toml`, so the
  graph has one source.
- **Freeze release tags during the move, and keep CI.** The old
  repositories keep their workflows until each one is archived, so
  an urgent fix can still release from the old repository. No
  release tag is pushed in any repository from the start of step 1
  until the branch merges.
- **The full history, with prefixed tags.** `git log` and `git
  blame` inside a component directory show the component's whole
  history, and the old tags give the first release a place to diff
  from. Only `liken`'s history keeps its SHAs, because published
  releases name them.
- **One site, and the old names as redirects.** A separate site for
  each component split the manual by repository, not by what a
  reader looks for. The redirect keeps the one property of the old
  sites that matters: the name in the cluster opens the manual.
- **The OS directory is `liken/`.** The directory name is the
  component name, and the OS component and its artifacts are named
  `liken`. The Go import paths become
  `github.com/liken-sh/liken/liken/...`.
- **Two ways to version, one model.** A pinned component's version
  comes from upstream, and its revision is a declaration that a
  person reviews. A tracked component has no upstream, so the
  repository tag is its version. The directory, the file, the graph,
  the publish steps, and the attestations are the same for both.
- **One tag format for the whole repository, not one per
  component.** A tag such as `display-operator/2026.10.02-001` would
  let each component release alone. The path diff gives the same
  result: a component that did not change gets no new version, and
  its pods do not restart. So the person who tags decides when to
  release, and the graph decides what.
- **A path diff for tracked components, not an input hash.** The
  paths come from the graph, which changes slowly. The diff starts
  from each component's own newest published version, so a failed
  release heals on the next tag.
- **A recipe hash for pinned components, and a cascade by tooling.**
  The hash includes the published revisions of the dependencies, so
  a dependency's bump cannot reach a dependent without a reviewed
  revision. The `bump-components` skill writes the cascade.
- **Consumers build `FROM` the tree.** A change to a base reaches its
  consumers at the next build. Hand-moved pins drifted between
  consumers, and the tree cannot drift.
- **Manifests ship as OCI artifacts, not as git refs.** A cluster
  reads one version from one registry for both the manifests and the
  images. The git tag format then stays internal to the repository,
  and a development build needs no commit sha.
- **The OS releases when its inputs changed, with the rest.** The OS
  is a tracked component like the others, and the project accepts
  that a tag can release it.
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
  distro.
- **Not a distro build service.** openSUSE's OBS, Fedora's COPR, and
  Launchpad build packages in a distro's format and on a distro's
  base.
- **Not a self-hosted runner.** A fork pull request can run any code
  on the runner, and GitHub warns against self-hosted runners for
  public repositories.

## Open questions

- **Where the redirect service runs.** It needs a host that answers
  `*.liken.sh` over HTTPS with a wildcard certificate. Linode DNS
  has no redirect feature, and a Linode community answer says that
  Object Storage does not serve redirects.
- **The order inside step 1.** Which component moves first, and
  whether the old repositories keep building until the last one
  moves.
- **Absent `version` means tracked.** A tracked component has no
  `version` field. A misspelled field then also reads as tracked.
  An explicit field, such as `track = "tag"`, would make the choice
  visible.
- **Attestations on ghcr.** The `push-to-registry` report above
  applies to images. The first image attestation checks it.
- **Does stagex's Go match the Go version that each k3s release
  needs?** Kubernetes pins its Go version closely. If stagex is late,
  the k3s bump waits, or `liken` builds that Go version as a
  component of its own.
- **Does stagex ship the static libraries** that k3s links
  (libseccomp, sqlite, zlib) and that the current Alpine builds link
  (libnl, libtirpc, kmod, libeconf)? `libnl` was not in the stagex
  package list on 2026-09-25.
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

## Sources

Checked on 2026-09-25:

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

Checked on 2026-09-28:

- Flux `OCIRepository`, `source.toolkit.fluxcd.io/v1`, with
  `ref.tag`, `ref.semver`, and `ref.digest`, and cosign and Notation
  verification: <https://fluxcd.io/flux/components/source/ocirepositories/>
  (the page for Flux 2.9).
- Flux image automation markers, which are inline comments in the
  target YAML: <https://fluxcd.io/flux/guides/image-update/>.
- Docker Bake, a target as a named build context with
  `contexts = { name = "target:<target>" }`:
  <https://docs.docker.com/build/bake/contexts/>.
- `git filter-repo`, `--to-subdirectory-filter` and `--tag-rename`:
  <https://github.com/newren/git-filter-repo>.
- A Linode community answer that Object Storage does not serve
  redirects, with no date on the answer:
  <https://www.linode.com/community/questions/19668/how-can-i-add-a-redirection-rule-to-my-bucket>.
- GitHub Pages custom domains, one for each repository:
  <https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/about-custom-domains-and-github-pages>.
