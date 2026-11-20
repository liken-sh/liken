# Where the changelogs live

Each entry says where the release notes of one component are, and
which part of them matters to `liken`.

- **kernel:** kernel.org's ChangeLog for the version.
- **k3s:** the GitHub release notes, with the table of embedded
  components compared against the old release's table. Then the
  upstream `CHANGELOG-1.NN.md` for the Kubernetes patch, and the k3s
  issue tracker for reports against the new tag.
- **xtables:** the k3s-root release notes on GitHub.
- **buildroot:** the git log of `master` between the two commits, for
  the packages that `userland/os.config` and
  `userland/bluetoothd.config` select, the libraries they link, and
  the toolchain. `make -C userland` then prints each version in
  `userland/dist/legal-info/manifest.csv`. Compare it with the old
  build's.
- **nfs-utils:** the git shortlog between tags at git.linux-nfs.org.
  Only the `mount.nfs` client path matters, because `liken` runs no
  NFS server.
- **systemd-boot and grub:** changelogs.ubuntu.com for the package
  revision. Only changes to sd-boot, `bootctl`, or the EFI stub matter
  for systemd-boot, because the daemons are not shipped.
- **hwdata:** the GitHub compare between the two tags. Expect only
  `pci.ids` and `usb.ids` data.
- **tzdata:** the IANA tz-announce message for the release.
- **linux-firmware:** the gitlab.com/kernel-firmware compare between
  tags, because git.kernel.org blocks automated fetches. Watch for
  removed files and i915/xe changes.
- **wireless-regdb:** the git log at
  git.kernel.org/pub/scm/linux/kernel/git/wens/wireless-regdb.git,
  announced on the linux-wireless list. A change is regulatory rules
  per country, so the research is which countries moved.
- **microcode:** `releasenote.md` in Intel's
  Intel-Linux-Processor-Microcode-Data-Files repo, which lists the
  INTEL-SA advisories and the updated platforms.
- **flux:** the flux2 release notes, plus the changelog of each
  controller the release bumped.
- **e2fsprogs:** the release notes at e2fsprogs.sourceforge.net. Only
  `mke2fs` ships.
- **trust, hugo:** no research unless the version jump looks unusual.
- **storage:** the lab's storage guest runs sid's daily cloud image,
  which Debian deletes after about four months.
