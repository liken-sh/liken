---
name: bump-components
description: Bump the vendored component pins — the make versions report, each domain's latest.sh --bump, the changelog research, and a final report of what changes and what to watch. Use when I say /bump-components or ask to bump, update, or refresh the component pins.
---

Bump every vendored pin that is behind, read the changelogs, and
report what is changing and what to look out for.

Additional context from me: $ARGUMENTS

If that context names specific components, or says to skip the
research or stop after a step, it wins over the defaults below.

**Core principle: the bumps are cheap working-tree edits. The value
is the research. Nothing here commits, builds, or releases.**

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

## 5. Report

Give one verdict per bumped component: what changed, whether there
is anything to worry about, and what to watch in the drills. Lead
with the concerns, if there are any. The tree stays uncommitted;
the build, the smoke drills, and `/release` are separate decisions.
