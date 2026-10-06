---
name: bump-components
description: Bump the vendored component pins — the make versions report, each domain's latest.sh --bump, the changelog research, and a final report of what changes and what to watch — and the pinned base images, their Debian snapshot and their revisions. Use when I say /bump-components or ask to bump, update, or refresh the component pins or the base images.
---

Bump every vendored pin that is behind, read the changelogs, and
report what is changing and what to look out for.

Additional context from me: $ARGUMENTS

If that context names specific components, or says to skip the
research or stop after a step, it wins over the defaults below.

**Core principle: the bumps are cheap working-tree edits. The value
is the research. Nothing here commits, builds, or releases.**

Run the commands of steps 1 to 4 from `liken/`, where the domains and
`make versions` are. The paths in those steps are relative to
`liken/`. Step 5 works at the top of the repository.

## 1. Pull the report

Run `make versions`. It reaches every upstream, so give it a couple
of minutes. Collect the rows whose state is `behind`. An indented row
is a sub-pin that hangs off the domain above it; it needs its own
named bump in step 2.

## 2. Bump

Run `<domain>/latest.sh --bump` for each behind domain, sequentially,
in one background task. Two orderings and one repeat matter:

1. Put `systemd-boot` and `grub` last. Their source mirrors fetch
   from launchpad.net, which is slow and sometimes refuses
   connections, and each bump's tail runs `licensing/sources.sh
   --repin`, which retries launchpad every time.
2. A sub-pin needs a named bump, for example
   `nfs-utils/latest.sh --bump libtirpc` or
   `linux-firmware/latest.sh --bump wireless-regdb`. The alpine builder is one
   image pinned by three domains; when it moves, run `--bump alpine`
   in `open-iscsi`, `nfs-utils`, and `tzdata`.
3. Finish with one `licensing/sources.sh --repin`. The mirror cache
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
* trust, alpine, hugo, storage: routine refreshes; no research
  unless the version jump looks unusual.

Note each release's age. A firmware or microcode release that is
days old has no field history, and the smoke drills plus the first
metal boot are its first real test.

## 4. Verify

1. `make versions` again: every row must read `current`.
2. `git diff --stat`: the change is `VERSION` files, `fetch.sh`
   digests, and `licensing/sources.sh`, nothing else.
3. `make all`: changelogs do not show configure or toolchain
   breakage, and a bump that does not build is not done. The
   domains that compile from source on the musl builder break the
   most often; a new configure check or a dropped default there is
   invisible until the build runs.
4. `make smoke-uefi`: one boot to Ready proves the pieces still
   assemble into a machine.

## 5. The base images

The base images, `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`, are
pinned components at the top of the repository. `make versions` does
not report them. Each one's `package.toml` states a `version`, the date
of the snapshot.debian.org archive it installs from as `YYYYMMDD`, and
a `revision`. Each one publishes under the tag `<version>-<revision>`.

**A new snapshot.** All five move together, because a library that two
bases hold must be the same file in both. The steps are in
`vulkan/README.md`: check that the snapshot exists, write the sha256
of its three `InRelease` files into `vulkan/snapshot.sha256`, read the
digest of the newest `debian:trixie-slim`, set `version` to the date
and `revision` to 1 in all five `package.toml` files, set the digest
in every `FROM debian:trixie-slim` line, run `make workflows`, run
`make images`, and run each image's smoke check. Compare the package
versions with the old snapshot and report the ones that moved.

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

* `vulkan` raises `vaapi`, `ffmpeg`, `mpv`, and `weston`.
* `vaapi` raises `ffmpeg` and `mpv`.
* `ffmpeg` raises `mpv`.
* `mpv` and `weston` raise nothing.

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
from the `trust` image. `make versions` reports it as the domain
`../trust`. Its `--bump` writes the new date into `VERSION`, the image's
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

Bump it only when asked, or for a security fix in `indiserver`. Set the
date and revision 1, update `[package.upstream]` from the snapshot's
`Packages` index, and run `make workflows`. Compare the license file of
each SDK directory in indi-3rdparty, at the commit that the PPA built
from, with `indi/sdk-licenses/`, and copy each new or changed one; the
section "The notices" in `indi/README.md` holds the steps. Build every target in
`indi/package.toml` and run each smoke check. The build fails when a
new release adds a third-party driver that no list in `indi/images/`
names; add it to an image's list or to `indi/images/unpublished`, and
say which in the report. CI builds the 17 images only when this tag is
new.

## 6. Report

Give one verdict per bumped component: what changed, whether there
is anything to worry about, and what to watch in the drills. Lead
with the concerns, if there are any. The tree stays uncommitted;
the build, the smoke drills, and `/release` are separate decisions.
