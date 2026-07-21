# liken third-party notices

liken's own code — the init, the operators, the log relays, the
toolkit, and everything else in [its repository](https://github.com/liken-sh/liken)
— is MIT-licensed, copyright Chris Guidry. A liken release is an
aggregate. Alongside liken's own programs, it redistributes other
people's work, unmodified, under their own licenses. This document
names each of those components, its license, and where to get its
source. The full text of every license appears in the appendix.

The copyleft licenses here, GPL and LGPL, apply to the components
they cover, and never to the aggregate. liken's programs invoke these
components as separate processes, or ship them side by side, and the
kernel's own license explicitly exempts programs that only make
system calls. The copyleft licenses do require one thing: anyone who
receives the binaries must be able to get their source. The release
channel serves the complete corresponding source for every such
component from the same place it serves the binaries, at
`https://releases.liken.sh/sources/<component>/<version>/`. This
location is keyed by the component versions that each release's
`release.yaml` records.

## The Linux kernel (`vmlinuz`, `/lib/modules`)

License: GPL-2.0 only, with the syscall exception.
Copyright Linus Torvalds and the kernel's many contributors.

This binary is vendored, prebuilt from Canonical's mainline archive,
which builds unmodified upstream releases. The source is the upstream
tarball plus the build configuration. Both are mirrored under
`sources/kernel/`.

## k3s (`/bin/k3s`)

License: Apache-2.0. Copyright k3s contributors and SUSE LLC.

This binary is vendored, prebuilt from the project's releases,
unmodified. The source is published from the same release page. The
k3s binary embeds a small userland — busybox, iptables, and similar
programs, several under GPL-2.0 — that it unpacks at runtime. The
k3s-root recipe builds that userland, and its sources are mirrored
under `sources/xtables/`. This is the same recipe, at the same
version, that builds the xtables binaries described below.

## The xtables binaries (`/sbin/iptables` and its names)

License: GPL-2.0. Copyright the Netfilter Core Team.

This is one static iptables multi-call binary, vendored from
k3s-root's releases. `sources/xtables/` mirrors three things: the
iptables source, the k3s-root recipe that builds it, and buildroot,
the build system that the recipe drives. Buildroot pins every package
it builds by hash, so the recipe identifies the exact source of
everything else in k3s-root's userland.

## mke2fs (`/sbin/mke2fs`)

License: GPL-2.0 (the program), with LGPL-2.0 (libext2fs), BSD-3
(libuuid), and MIT-style (libet/libss) libraries built in. Copyright
Theodore Ts'o and others. The static link also carries glibc,
LGPL-2.1, copyright the Free Software Foundation.

This binary is vendored from gokrazy's reproducible build of the
upstream e2fsprogs release. The e2fsprogs domain's fetch script names
the exact recipe and commit used. `sources/e2fsprogs/` mirrors the
e2fsprogs tarball and glibc 2.31, the version in the recipe's
debian:bullseye toolchain. One fidelity note: the recipe links
Debian's glibc package, which carries Debian patches on top of the
GNU release mirrored here. The recipe itself names the toolchain
image for anyone who wants to rebuild the exact bytes.

## The iSCSI initiator (`/sbin/iscsid`, `/sbin/iscsiadm`)

License: GPL-2.0 or later. Copyright the open-iscsi contributors
(including Cisco Systems, Dicon Zhang, Red Hat, and others).

This repository's own recipe (open-iscsi/fetch.sh) builds this binary
from pinned source. It statically links libkmod (LGPL-2.1 or later,
copyright the kmod contributors), libblkid and libmount from
util-linux (LGPL-2.1 or later, copyright Karel Zak and others),
libeconf (MIT, copyright SUSE LLC), OpenSSL (Apache-2.0, copyright
the OpenSSL Project), and musl (MIT, copyright Rich Felker and
contributors). Sources are mirrored under `sources/open-iscsi/`, and
for the alpine-packaged static libraries, under `sources/toolchain/`.
The recipe pins the build container by digest. Alpine's musl and
util-linux packages carry small distribution patches on top of the
upstream tarballs mirrored here.

## The NFS client (`/sbin/mount.nfs`)

License: GPL-2.0. Copyright the Linux NFS maintainers and
contributors.

This repository's own recipe (nfs-utils/fetch.sh) builds this binary
from pinned source. It statically links libtirpc (BSD-3, copyright
Sun Microsystems, Inc.) and the same util-linux and musl libraries as
the iSCSI initiator. Sources are mirrored under `sources/nfs-utils/`
and `sources/toolchain/`.

## systemd-boot (`systemd-bootx64.efi`)

License: LGPL-2.1 or later. Copyright the systemd contributors.

This binary is vendored, prebuilt from Ubuntu's archive, unmodified.
The Ubuntu source package — the upstream tarball, the packaging, and
the .dsc file — is mirrored under `sources/systemd-boot/`.

## GRUB (`grub-boot.img`, `grub-core.img`)

License: GPL-3.0 or later. Copyright the Free Software Foundation.

These are the BIOS boot stages, produced from Ubuntu's grub-pc-bin
package by grub-mkimage with no source modification. The Ubuntu
source package is mirrored under `sources/grub/`.

## The CA trust store (`/etc/ssl/certs/ca-certificates.crt`)

License: MPL-2.0 (the bundle; it derives from Mozilla's certdata.txt).

These are the Mozilla CA program's root certificates, extracted and
published by the curl project. The PEM bundle is its own source form,
and it is mirrored under `sources/trust/`.

## The PCI naming database (`/usr/share/hwdata/pci.ids`)

License: BSD-3-Clause (dual-licensed with GPL-2.0-or-later; liken
redistributes it under the BSD option). Copyright Martin Mares and
Albert Pool.

This is the pci.ids compilation, snapshotted by the hwdata project.
It is the database that names PCI vendors and devices in the
Machine's unclaimed-hardware report. The file is its own source form,
and it is mirrored under `sources/hwdata/`.

## Driver firmware (`/lib/firmware`)

License: per family, as the WHENCE manifest records. Most families
are redistributable binaries under vendor terms; some files carry
the GPL or a dual license.

These are the device firmware blobs from the linux-firmware project,
cut down to the set that the shipped kernel's modules can request
(the linux-firmware domain documents the derivation). The image
carries the WHENCE manifest and every license text beside the blobs,
at `/lib/firmware/WHENCE` and `/lib/firmware/LICENSES/`. WHENCE is
the authoritative per-file record of copyright and terms. The
upstream release tarball is mirrored under `sources/linux-firmware/`.
For most blobs the binary is its own source form; for the GPL blobs
whose source exists, upstream keeps that source in the same tree, so
the one mirror satisfies the source offer for every file the image
carries.

## liken's programs and their Go dependencies

liken's binaries — `/liken`, the operators, the log relays, and the
`liken` toolkit — are MIT-licensed. They statically compile in the Go
standard library and runtime (BSD-3, copyright 2009 The Go Authors)
and these modules:

Under the MIT license:

* github.com/josharian/native — copyright 2020 Josh Bleecher Snyder
* github.com/mdlayher/packet — copyright 2022 Matt Layher
* github.com/mdlayher/socket — copyright 2021 Matt Layher
* sigs.k8s.io/yaml — copyright 2014 Sam Ghods; portions copyright
  2012 The Go Authors (BSD-3)

Under the BSD 2-Clause license:

* github.com/beevik/ntp — copyright 2015–2023 Brett Vickers

Under the BSD 3-Clause license:

* github.com/insomniacslk/dhcp — copyright 2018 Andrea Barberio
* github.com/pierrec/lz4/v4 — copyright 2015 Pierre Curto
* github.com/u-root/uio — copyright 2012–2021 u-root Authors
* golang.org/x/net, golang.org/x/sync, golang.org/x/sys — copyright
  2009 The Go Authors

Under the Apache License 2.0:

* github.com/vishvananda/netlink — copyright 2014 Vishvananda Ishaya
* github.com/vishvananda/netns — copyright 2014 Vishvananda Ishaya
* go.yaml.in/yaml/v2 — copyright 2011–2016 Canonical Ltd.

The appendix reproduces the MIT and BSD license texts that these
modules are offered under, once each. The copyright lines above
complete each text for its holder.
