---
name: bump-components
description: Bump every pin that is behind the newest upstream release, under the rule in root plan 83 - the top-level make versions report, each latest.sh --bump, the changelog research, and a final report of what changes and what to watch - including the toolchains, the pinned base images, their Debian snapshot and their revisions. Use when I say /bump-components or ask to bump, update, or refresh the component pins or the base images.
---

Bump every vendored pin that is behind, read the changelogs, and
report what is changing and what to look out for.

Additional context from me: $ARGUMENTS

If that context names specific components, or says to skip the
research or stop after a step, it wins over the defaults below.

**Core principle: the bumps are cheap working-tree edits. The value
is the research. Nothing here commits, builds, or releases.**

A pin is current when it matches the newest upstream release that is
not a pre-release. A pin built from source follows the newest
upstream release. A pin from a package repository, a registry, or a
channel server follows the newest channel that the repository offers
to install as a whole (Debian sid, Alpine `edge`, Ubuntu's
development series, k3s `latest`, the newest series a PPA publishes)
and the newest package in it. Root plan 83 gives the reasons.

Run `make versions` at the top of the repository. The commands of
steps 2 to 4 run from `liken/`, and their paths are relative to
`liken/`. Steps 5 and later work at the top of the repository.

## 1. Pull the report

Run `make versions` at the top of the repository. `versions.sh` finds
every `latest.sh` in the tree, runs them in parallel, and prints one
table. It reaches every upstream, so give it a minute or two. Collect
the rows whose state is `behind` or `wrong channel`. An indented row
is a sub-pin that hangs off the row above it; it needs its own named
bump.

Each `latest.sh` prints tab-separated rows of seven columns: name,
pinned, latest, channel, newest channel, hold, and note.

- `channel` is the suite, series, release, or channel that the pin
  reads, and `newest channel` is the newest one its repository
  offers. A pin built from source, or one that follows another pin,
  prints `-` in both.
- `hold` is `-`, or the reason the pin stays back. A hold needs a
  reason a reader can check, and it lives in the row, never in a
  comment alone.
- `latest` is `?` when the upstream did not answer.

The state is `held` when the row has a hold, `unknown` when an
upstream did not answer, `wrong channel` when the two channels
differ, `behind` when pinned and latest differ, and `current`
otherwise. A new `latest.sh` opens with a header that says what it
pins, where the bytes come from, which channel it reads, and why.

The report has these sources:

| Script | Pins |
|---|---|
| `liken/<domain>/latest.sh` | the OS domains, steps 2 to 4 |
| `liken/dev-cluster/firmware/capsule/latest.sh` | edk2, the Ubuntu image, and the Ubuntu snapshot of the lab's capsule firmware builder; it has no `--bump`, because each move changes the compiler that builds the firmware, so edit `build.sh` or the `Dockerfile` by hand and boot the lab with `FIRMWARE=capsule` |
| `toolchains/latest.sh` | each image in `toolchains/images.toml`, Go and Rust, each Go module and crate that is behind |
| `vulkan/latest.sh` | the Debian snapshot of the bases and `astrometry`, step 5 |
| `indi/latest.sh` | the snapshots of Ubuntu and the two PPAs, step 5a |
| `trust/latest.sh` | the CA bundle, step 5b |
| `ci/latest.sh` | the GitHub Actions, prek, and the hook revisions |
| `liken.sh/latest.sh` | Flatcar and the Terraform providers |

## 2. Bump

Run `<domain>/latest.sh --bump` for each behind domain, sequentially,
in one background task. These rules matter:

1. Put `systemd-boot` and `grub` last. Their source mirrors fetch
   from launchpad.net, which is slow and sometimes refuses
   connections, and each bump's tail runs `licensing/sources.sh
   --repin`, which retries launchpad every time.
2. A sub-pin needs a named bump, for example
   `nfs-utils/latest.sh --bump libtirpc` or
   `linux-firmware/latest.sh --bump wireless-regdb`. The alpine
   builder of the static builds is the `alpine` entry in
   `toolchains/images.toml`, and `../toolchains/latest.sh --bump
   alpine` moves it for `open-iscsi`, `nfs-utils`, `tzdata`,
   `wpa-supplicant`, `e2fsprogs`, and `bluetoothd` at once.
   `licensing/latest.sh` reads `musl`, `util-linux`, and `libnl`
   from the same Alpine branch, so those rows follow the builder.
3. Bump `k3s` before `xtables`. The xtables pin follows the k3s-root
   release that the pinned k3s names as `VERSION_ROOT`, so
   `xtables/latest.sh` reads `k3s/VERSION` and reports xtables as
   behind only after k3s moves.
4. Finish with one `licensing/sources.sh --repin`. The mirror cache
   in `licensing/cache/` skips files it already has, so the final
   pass is cheap and proves the source pins match their bytes.

If a bump's repin fails, the pin still moved; the final repin
repairs it. If the final repin fails, stop and report which file it
could not fetch.

Each bump prints a `next:` hint naming the make target that rebuilds
its domain. `make all` covers them, with one trap: `make storage` is
not a build. It starts the lab's storage guest under QEMU and holds
the foreground for the guest's whole life, so never chain it before
another target.

Two bumps carry extra weight. A k3s minor bump is a Kubernetes minor
bump; it can remove an API version a workload still uses, so read
the upstream release notes before taking one. Before a kernel bump,
check memory and `plans/open-problems/` for items pinned to the
current kernel, such as a workaround that the next kernel should
retest.

## 3. Read the changelogs

Dispatch parallel research agents, grouped a few components each.
Each agent reads the actual release notes on the web and reports
breaking changes, behavior changes, CVE fixes, regressions reported
against the new tag, and anything relevant to a 1GB machine. Where
the notes live:

* kernel: kernel.org's ChangeLog for the version.
* k3s: the GitHub release notes, diffing the embedded-component
  table against the old release; the upstream `CHANGELOG-1.NN.md`
  for the Kubernetes patch; the k3s issue tracker for reports
  against the new tag.
* xtables: the k3s-root release notes on GitHub.
* nfs-utils: the git shortlog between tags at git.linux-nfs.org.
  Only the mount.nfs client path matters; liken runs no NFS server.
* libtirpc: its changelog at git.linux-nfs.org or sourceforge.
* systemd-boot and grub: changelogs.ubuntu.com for the package
  revision. Only changes to sd-boot, bootctl, or the EFI stub
  matter for systemd-boot; the daemons are not shipped.
* hwdata: the GitHub compare between the two tags. Expect only
  `pci.ids` and `usb.ids` data.
* tzdata: the IANA tz-announce message for the release.
* linux-firmware: the gitlab.com/kernel-firmware compare between
  tags; git.kernel.org blocks automated fetches. Watch for removed
  files and i915/xe changes.
* wireless-regdb: the git log at
  git.kernel.org/pub/scm/linux/kernel/git/wens/wireless-regdb.git,
  announced on the linux-wireless list. A change is regulatory rules
  per country, so the research is which countries moved.
* microcode: `releasenote.md` in Intel's
  Intel-Linux-Processor-Microcode-Data-Files repo, which lists the
  INTEL-SA advisories and the updated platforms.
* flux: the flux2 release notes, plus the changelog of each
  controller the release bumped.
* e2fsprogs: the release notes at
  e2fsprogs.sourceforge.net. Only `mke2fs` ships.
* trust, hugo: no research unless the version jump looks unusual.
* alpine, storage: a new Alpine release or a move to `edge` can
  drop a static library or change a default that a build from
  source depends on, so build every domain that uses the builder.
  The lab's storage guest runs sid's daily cloud image, which
  Debian deletes after about four months.

Note each release's age. A firmware or microcode release that is
days old has no field history, and the smoke drills plus the first
metal boot are its first real test.

## 4. Verify

1. `make versions` again: every row must read `current` or `held`.
2. `git diff --stat`: the change is `VERSION` files, `fetch.sh`
   digests, `licensing/sources.sh`, and `toolchains/images.toml`,
   nothing else.
3. `make all`: changelogs do not show configure or toolchain
   breakage, and a bump that does not build is not done. The
   domains that compile from source on the musl builder break the
   most often; a new configure check or a dropped default there is
   invisible until the build runs.
4. `make smoke-uefi`: one boot to Ready proves the pieces still
   assemble into a machine.

## 5. The base images

The base images, `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`, are
pinned components at the top of the repository. `vulkan/latest.sh`
reports their snapshot date and each `[package.upstream]` entry
against the newest snapshot of sid. Each one's `package.toml` states a `version`, the date
of the snapshot.debian.org archive it installs from as `YYYYMMDD`, and
a `revision`. Each one publishes under the tag `<version>-<revision>`.

**A new snapshot.** All five move together, because a library that two
bases hold must be the same file in both. `vulkan/latest.sh --bump`
writes the date, revision 1, and the `[package.upstream]` entries
into every `package.toml` that installs from the snapshot, the sha256
of the snapshot's `InRelease` file into `vulkan/snapshot.sha256`, and
the digest of the newest `debian:sid-slim` into the `debian` entry of
`toolchains/images.toml`. `vulkan/README.md` gives the same steps by
hand. Then run `make workflows`, run `make images`, and run each
image's smoke check. The report shows the snapshot as `behind` on
every day after a bump, because sid takes uploads every day. Compare the package
versions with the old snapshot and report the ones that moved.
The suite is sid, Debian unstable, so a new date can bring a new
major release of Mesa, ffmpeg, mpv, or Weston. Read the upstream
release notes of each major release that moved, as in step 3, and
roll the dev builds to the `liken-1` testbed before any other
cluster takes them. Root plan 82 gives the reasons for sid.
`astrometry`, the plate solver, is not a base, but it installs from
the same snapshot through `vulkan/snapshot.sh`, and
`vulkan/snapshot.sha256` holds the checksums of one date. So it moves
with the five: its `version`, `revision`, digest, and
`[package.upstream]` change in the same commit.

Each base's `package.toml` also lists, under `[package.upstream]`, the
upstream releases that matter in it, such as `mesa` and `ffmpeg`. A
pinned version is a date, so these entries are how a reader of the
registry learns what a tag holds: each one becomes the image label
`sh.liken.upstream.<name>`. A new snapshot updates every entry to the
version that the snapshot installs. Read the versions from the
snapshot's `Packages` indexes, or with `dpkg-query -W` in the builder.

**A new revision.** CI hashes each base's recipe: its `package.toml`,
its `Dockerfile`, every file in its build context, the digest of each
image outside the repository that it starts from, and the tag of each
component in its `[depends]`. A change to any of them at the same
version needs the next revision. The rule cascades: a base's tag is in
the recipe of every pinned component above it, so a new revision of
`ffmpeg` changes the recipe of `mpv`, and `mpv` needs a new revision in
the same commit. Walk the graph up from each changed base and raise the
revision of every pinned component that depends on it, directly or
through another base:

* `vulkan` raises `vaapi`, `ffmpeg`, `mpv`, `weston`, and `astrometry`.
* `vaapi` raises `ffmpeg` and `mpv`.
* `ffmpeg` raises `mpv`.
* `mpv`, `weston`, and `astrometry` raise nothing.

A new snapshot sets every revision to 1, so it needs no cascade. When
CI fails with "the recipe of N pinned components changed with no new
revision", the message names each component to raise.

The files that a base's `.dockerignore` leaves out, such as its
`README.md` and its `smoke/` check, are not in the recipe, and a
change to them needs no revision.

## 5b. The trust store

`trust` at the top of the repository pins the CA bundle that every part
of `liken` trusts: the OS build reads its `VERSION` through
`trust/fetch.sh`, and every image that verifies TLS copies the bundle
from the `trust` image. `make versions` reports it as `trust`. Its `--bump` writes the new date into `VERSION`, the image's
version and `[package.upstream]` into `package.toml`, and the snapshot
and its published checksum into the `Dockerfile`. Then run
`make workflows` at the top of the repository and raise the revision of
`indi`, the one pinned component that copies the bundle. The tracked
components that copy it release because their `[depends]` names
`trust`. A snapshot removes trust as well as adding it, so read what
left the bundle before taking the bump.

## 5a. The INDI images

`indi` is a pinned component too, with the 17 images that
observatory-operator runs. It is not one of the five bases above: it
installs from Ubuntu 26.04, the INDI PPA, and the PHD2 PPA, through
dated snapshots of all three, and it moves on its own schedule. Its
`version` is the date of those snapshots, and `[package.upstream]`
states the `indi-bin`, `indi-3rdparty-drivers`, `gsc`, and `phd2`
package versions that the date installs. Its smoke checks fail when the image holds other versions.

`indi/latest.sh` reports the snapshot date, the four package
versions, and the newest Ubuntu series that both PPAs publish. When a
PPA publishes a newer series, the row reads `wrong channel`, and the
move to that series is a hand edit of `indi/snapshot.sh` and the
`ubuntu` entry of `toolchains/images.toml`. `indi/latest.sh --bump`
sets the date and revision 1 and updates `[package.upstream]` from
the snapshot's `Packages` index. Then run `make workflows`. Compare the license file of
each SDK directory in indi-3rdparty, at the commit that the PPA built
from, with `indi/sdk-licenses/`, and copy each new or changed one; the
section "The notices" in `indi/README.md` holds the steps. Build every target in
`indi/package.toml` and run each smoke check. The build fails when a
new release adds a third-party driver that no list in `indi/images/`
names; add it to an image's list or to `indi/images/unpublished`, and
say which in the report. Then run `make -C observatory-operator
drivers firmware-rules`. `drivers` writes the new tag into
`drivers/generated.go`, and `firmware-rules` copies `85-qhyccd.rules`
out of the new `indi-qhy` image and writes `deploy/firmware/qhy.yaml`
from it, one `Firmware` for each bare QHY chip, on the new tag. Report
each object it adds or removes: a new object is a new QHY camera that
`firmware-operator` loads, and the test that counts the objects needs
the new count. Then run `make -C observatory-operator
captures`, which records the captures of the standard INDI properties
and the simulator transcripts again on the new libindi; the operator's
tests fail until it runs. Then run `make -C observatory-operator
schema crosscheck`; `observatory-operator/AGENTS.md` gives the steps.
Report each new line of `observatory-operator/schema/disagreements.txt`.
CI builds the 17 images only when this tag is new.

## 5c. The toolchains

`toolchains/images.toml` names each outside image once. A Dockerfile
names an image from its `[build]` table bare, as in `FROM golang`,
and `ci/` writes the reference and its digest into the bake target.
Manifests, guides, skills, and tests name a `[run]` image at its tag.
`toolchains/latest.sh --bump <key>` writes the newest tag of an image
and, for a `[build]` image, its digest. `--bump go` and `--bump
rustc` move the toolchain image and every `go.mod` or
`rust-toolchain.toml`. `--bump go:<module>` and `--bump
crate:<name>` move every copy of one dependency. The k8s.io modules
follow the k3s release in `liken/k3s/VERSION`, so they move after
k3s does.

A new reference in `[build]` changes the recipe of each pinned
component whose Dockerfile names that key, so each one needs a new
revision, and the cascade in step 5 applies. Run `make workflows`
after every bump of `images.toml`. `cd ci && go run . pins -root ..`
fails when two copies of a Go version, a Rust version, a module, a
crate, a hook revision, or an image tag disagree; CI runs it on every
push.

`ci/latest.sh --bump` moves one GitHub Action, prek, or one hook
revision. A major version of an action can change its inputs, so read
its release notes before you take it.

## 6. Report

Give one verdict per bumped component: what changed, whether there
is anything to worry about, and what to watch in the drills. Lead
with the concerns, if there are any. The tree stays uncommitted;
the build, the smoke drills, and `/release` are separate decisions.
