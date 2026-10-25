# 83, Every pin stays current

Proposed on 2026-10-25. Steps 1 to 5 of [Order](#order) built on
2026-10-25. Step 6 is the first run of the `bump-components` skill
under the rule, and it has not run.

`liken` takes the newest release of everything it builds with and
ships. The bases sat on Debian trixie, the stable release, until
[plan 82](82-debian-sid.md) moved them to sid, and so
Weston was two major versions behind and drew the wrong colors. No
written rule said what a pin follows. Each `latest.sh` chose a line
on its own, such as a stable channel, an Ubuntu series, or an Alpine
release, and reported `current` when the line had nothing newer. Many
pins have no report at all.

This plan writes the rule down, gives every pin a report that checks
it, and gives each pin that several components share one owner. The
bumps that the new report finds are a separate step, after this plan.

## The rule

A pin is current when it matches the newest upstream release that is
not a pre-release. There is no soak time.

- A pin that `liken` builds from source follows the newest release of
  the upstream project.
- A pin that `liken` takes from a package repository, a registry, or
  a channel server follows the newest channel that the repository
  offers to install as a whole, and the newest package in that
  channel. For Debian that is sid, not `experimental`, because
  `experimental` adds packages on top of sid. For Alpine it is
  `edge`. For Ubuntu it is the development series. For k3s it is the
  `latest` channel.
- A third-party repository such as a PPA counts as its own repository.
  Its newest channel is the newest series that it publishes.
- A pin that must stay back is a hold. The hold states its reason in
  its report row, and the report shows the pin as `held`. A reason in
  a comment alone does not count, because the report cannot show it.
- A pin that follows another pin, such as `xtables` after the
  k3s-root release that k3s names, is current when it matches the pin
  it follows. Its row names that pin.

A repository can lag its upstream. sid had Weston 16.0.0 on
2026-10-24 when upstream had 16.0.1. The rule accepts that lag,
because `liken` takes Debian's build and not Weston's source.

## The report

`make versions` moves to the top of the repository. It finds every
`latest.sh` in the tree and prints one table.

Each `latest.sh` prints tab-separated rows with seven columns:

```
name  pinned  latest  channel  newest-channel  hold  note
```

- `channel` is the suite, series, release, or channel that the pin
  reads, and `newest-channel` is the newest one that the repository
  offers. A pin built from source prints `-` in both.
- `hold` is empty, or the reason the pin stays back.
- A sub-pin keeps the indented name it has today.

The report sets the state of each row:

| State | When |
|---|---|
| `held` | `hold` is not empty |
| `wrong channel` | `channel` differs from `newest-channel` |
| `behind` | `pinned` differs from `latest` |
| `unknown` | `latest` is `?`, because an upstream did not answer |
| `current` | none of the above |

Each `latest.sh` opens with the same header: what it pins, where the
bytes come from, which channel it reads, and why. `--bump` keeps its
current behavior: a bare `--bump` moves the main pin, and
`--bump <name>` moves one sub-pin. A bump to a new channel is a bump
like any other.

## One owner for each shared pin

The survey on 2026-10-25 found these pins repeated across components.
Each copy can drift on its own, and some already have:

| Pin | Copies |
|---|---|
| `golang:1.27.3-bookworm` | 28 `FROM` lines. One of them has a digest, and that digest is stale |
| `rust:1.98.1-bookworm` and the Rust toolchain | 3 `FROM` lines and 5 `rust-toolchain.toml` files |
| `debian:sid-slim` | 16 `FROM` lines, all with the same digest |
| The alpine builder | 4 `fetch.sh` files in `liken/`, the `toolchain/alpine-3.22` path in `licensing/sources.sh`, and `alpine:3.22` in `bluetoothd` |
| `go 1.27.3` | 28 `go.mod` files |
| Go modules | `prometheus/client_golang` at v1.23.2 in 3 modules and v1.24.1 in 8; `golang.org/x/sys` at two versions |
| Rust crates | each crate repeated in up to 4 `Cargo.toml` files, at the same version today |
| flux | `liken/flux/VERSION` at v2.9.6, and the CI action and its CLI at v2.9.5 |
| prek | `prek-version` in 2 CI templates, and the hook revisions in 26 `.pre-commit-config.yaml` files |
| Images in guides and manifests | `curlimages/curl` at 3 versions, Debian as `12-slim` and `trixie-slim`, `busybox` at 2 |

### The `toolchains` component

A new top-level component, `toolchains`, owns what the code builds
with: the outside images that the Dockerfiles start from, the Go and
Rust toolchains, and the Go modules and Rust crates.

`distroless` and `oras` are not toolchains, but a `FROM` line names
them, so they live in `images.toml` too.

- **`toolchains/images.toml`** maps a short name to one image
  reference with its tag and its digest:
  `golang = "golang:1.27.3-trixie@sha256:…"`. It holds `golang`,
  `rust`, `alpine`, `debian`, `ubuntu`, `distroless`, and `oras`.
- **Every Dockerfile names the image bare**, as in `FROM golang AS
  build`. That is how a Dockerfile names a base of the repository
  today. `ci/` writes the reference into the bake target's
  `contexts` as `docker-image://…`, next to the `target:` contexts it
  writes now.
- **The four OS `fetch.sh` files** read the alpine reference from
  `images.toml` and keep no copy.
- **`toolchains/latest.sh`** prints one row for each image, one for
  the Go toolchain, one for the Rust toolchain, and one for each Go
  module and Rust crate that is behind. Its `--bump` writes every
  copy that a check below requires to match.

`debian` stays tied to the snapshot date in `vulkan/`, because the
image and the snapshot must come from the same day. `images.toml`
holds the digest, and the snapshot bump writes the date and the
digest in one commit.

### The checks in CI

A check in `ci/` fails when two copies of one pin disagree:

- every `go.mod` names the same `go` version as the Go entry in
  `toolchains`;
- every `rust-toolchain.toml` names the Rust version of the `rust`
  image;
- each Go module has one version across every `go.mod` that requires
  it, and each crate has one version across every `Cargo.toml`;
- every image that a manifest, a guide, or a skill names is in
  `images.toml` at the same tag.

These checks stop drift at the commit. The report finds what is
behind upstream.

### What `ci/` changes

- `ci/bake.go` writes a `docker-image://` context for each bare name
  that `images.toml` resolves.
- `ci/recipe.go` hashes the resolved reference of a bare name. The
  hash already covers each target's contexts, so a new digest in
  `images.toml` changes the recipe of every pinned component that
  uses it. CI then asks for a new revision of each one, as it does
  now.
- The selection that decides what a push builds treats
  `images.toml` as an input of every component that reads it.
  Without that, a bump of `images.toml` releases nothing.
- The flux action and its CLI version come from `liken/flux/VERSION`,
  and `prek-version` comes from one value in the generator.

## The pins by domain

The survey ran on 2026-10-25. The table gives each pin that changes
under this plan. The pins not listed already follow the rule.

### The OS, in `liken/`

| Pin | Today | Under the rule |
|---|---|---|
| `k3s` | the `stable` channel: v1.36.5+k3s1 | the `latest` channel: v1.37.1+k3s1 |
| `xtables`, `iptables`, `buildroot` | follow the k3s-root release that k3s names | unchanged; each row names the pin it follows |
| The alpine builder | the `3.22` tag | `edge`, from `images.toml` |
| `musl`, `util-linux`, `libnl` | read the `v3.22` index | follow the builder's channel |
| `systemd-boot` | the pin's Ubuntu series: 259.5 | the development series, `tenacious`: 261.3 |
| `grub` | the pin's Ubuntu series: 2.12 | `tenacious`: 2.14. Its `grub-mkimage` needs a newer `libdevmapper` than the build has, and `grub-common` became `grub2-common` |
| `e2fsprogs`, `glibc` | gokrazy's prebuilt `mke2fs`, from a recipe on `debian:bullseye`: 1.47.1 and 2.31 | `mke2fs` builds from the e2fsprogs release on kernel.org, static against musl on the alpine builder: 1.47.4. The `glibc` row and the `gokrazy` sub-pin go away |
| `kernel` | Canonical's mainline builds: 7.2.6 | current in that repository. kernel.org has 7.3 and 7.2.12 |
| `storage` | the Debian trixie cloud image | sid's daily cloud image |

`e2fsprogs` builds the way `open-iscsi` and `nfs-utils` build now:
its `fetch.sh` downloads the release tarball, checks it against
kernel.org's sums file, and builds a static `mke2fs` in the alpine
builder. gokrazy's recipe stayed on `debian:bullseye` and on
e2fsprogs 1.47.1, and no rule of `liken`'s can move it, so `liken`
stops reading it. This step changes how the domain builds and keeps
the pinned version at 1.47.1. The bump to 1.47.4 is in step 6, like
the other bumps.

`liken/plans/72-the-os-builds-from-source.md` moves the kernel,
`grub`, `systemd-boot`, and `e2fsprogs` to stagex builds. Under this
rule each of them then follows its upstream release. Until then the
kernel follows Canonical's mainline builds, and the rule accepts
their lag behind kernel.org.

sid keeps its daily cloud images for about four months. So the
`storage` pin expires unless it moves, and `fetch.sh` needs the
`daily/` path and the image's new name.

### The bases and `indi`

- **`vulkan/latest.sh`** reports the snapshot date against the newest
  snapshot of sid. `toolchains/latest.sh` reports the `debian`
  digest, so the digest has one row. It prints one row for
  each `[package.upstream]` entry of the six images that install from
  the snapshot, with the version in the newest snapshot as `latest`.
  A bump of the date shows what it brings before it builds.
- **`indi/latest.sh`** reports the snapshot date, and the newest
  Ubuntu series that `mutlaqja/ppa` and `pch/phd2` publish as
  `newest-channel`. Both publish only `resolute` today, so `indi`
  stays on Ubuntu 26.04 and reports `current`. When a PPA adds a
  newer series, the report shows `wrong channel`.

### CI, the site, and the guides

- **`ci/latest.sh`** reports each GitHub Action in `ci/templates`,
  prek, and the hook revisions. Four actions are a major version
  behind: `setup-go`, `login-action`, `setup-buildx-action`, and
  `prek-action`. The templates keep major-version tags such as
  `@v7`, because a major tag takes each fix within the major
  version. The report checks the major version.
- **`liken.sh/latest.sh`** reports the Flatcar release and the
  Terraform providers. Flatcar's newest channel is `alpha`.
- **The guides and manifests** name their images through the
  `images.toml` check: `eclipse-mosquitto`, the CSI sidecars,
  `curlimages/curl`, `busybox`, and `debian`.

## Documents

- **The root `AGENTS.md`** states the rule in a short section next to
  "Releases and development builds".
- **The `bump-components` skill** gives the `latest.sh` contract,
  runs the top-level `make versions`, and covers every domain this
  plan adds. It loses the steps that bump the alpine builder in each
  domain, and the words "routine refresh" and "only when asked".
- **`vulkan/README.md`** names `images.toml` as the home of the
  `debian` digest.
- **Each manual** whose guides name an image changes to the tag in
  `images.toml`.

## Order

1. The rule, in `AGENTS.md` and the skill.
2. The report: the top-level `make versions` and the seven-column
   rows in every existing `latest.sh`, with the channel changes for
   `k3s`, the alpine builder, `systemd-boot`, `grub`, and `storage`,
   and `e2fsprogs` built from source at its pinned version. The pins
   do not move in this step. The report shows them as `wrong channel`
   or `behind`.
3. `toolchains`: `images.toml`, the changes in `ci/`, the bare `FROM`
   lines, the alpine builder in the four `fetch.sh` files, and
   `toolchains/latest.sh`.
4. The CI checks for one version of each shared pin. Where copies
   disagree today, each takes the newest of its copies.
5. `vulkan/latest.sh`, `indi/latest.sh`, `ci/latest.sh`, and
   `liken.sh/latest.sh`.
6. The first full bump under the rule, with `bump-components`, as its
   own commits. It includes k3s 1.37, whose embedded etcd moves from
   3.6 to 3.7. Read etcd's downgrade rules before it, and drill it on
   `liken-1` before any other cluster, because the `rollback` skill
   depends on an earlier release rejoining the cluster.

## Open question

- **The alpine builder on `edge`.** Nothing has built the static
  binaries of `open-iscsi`, `nfs-utils`, `tzdata`, `wpa-supplicant`,
  or `bluetoothd` on `edge` yet. The bump in step 6 is the first
  test.
