# liken third-party notices

liken's own code — the init, the operators, the log relays, the
toolkit, and everything else in [its repository](https://github.com/liken-sh/liken)
— is MIT-licensed, copyright Chris Guidry. But a liken release is an
aggregate: alongside liken's own programs it redistributes other
people's work, unmodified, under their own licenses. This document
names each of those components, its license, and where to get its
source. The full text of every license appears in the appendix.

The copyleft licenses here (GPL and LGPL) apply to the components
they cover and never to the aggregate: liken's programs invoke these
components as separate processes or ship them side by side, and the
kernel's own license explicitly exempts programs that merely make
system calls. What the copyleft licenses do require is that anyone
who receives the binaries can get their source. The release channel
serves the complete corresponding source for every such component
from the same place it serves the binaries, at
`https://releases.liken.sh/sources/<component>/<version>/`, keyed by
the component versions each release's `release.yaml` records.

## The Linux kernel (`vmlinuz`, `/lib/modules`)

License: GPL-2.0 only, with the syscall exception.
Copyright Linus Torvalds and the kernel's many contributors.

Vendored prebuilt from Canonical's mainline archive, which builds
unmodified upstream releases; the source is the upstream tarball plus
the build configuration, both mirrored under `sources/kernel/`.

## k3s (`/bin/k3s`)

License: Apache-2.0. Copyright k3s contributors and SUSE LLC.

Vendored prebuilt from the project's releases, unmodified; the source
is published from the same release page. The k3s binary embeds a
small userland (busybox, iptables, and friends, several GPL-2.0)
that it unpacks at runtime; that userland is built by the k3s-root
recipe, whose sources are mirrored under `sources/xtables/` — the
same recipe, at the same version, that builds the xtables binaries
below.

## The xtables binaries (`/sbin/iptables` and its names)

License: GPL-2.0. Copyright the Netfilter Core Team.

One static iptables multi-call binary, vendored from k3s-root's
releases. Mirrored under `sources/xtables/`: the iptables source, the
k3s-root recipe that builds it, and buildroot, the build system that
recipe drives (buildroot pins every package it builds by hash, so the
recipe identifies the exact source of everything else in k3s-root's
userland).

## mke2fs (`/sbin/mke2fs`)

License: GPL-2.0 (the program), with LGPL-2.0 (libext2fs), BSD-3
(libuuid), and MIT-style (libet/libss) libraries built in. Copyright
Theodore Ts'o and others. The static link also carries glibc,
LGPL-2.1, copyright the Free Software Foundation.

Vendored from gokrazy's reproducible build of the upstream e2fsprogs
release (the e2fsprogs domain's fetch script names the exact recipe
and commit). Mirrored under `sources/e2fsprogs/`: the e2fsprogs
tarball and glibc 2.31, the version in the recipe's debian:bullseye
toolchain. One fidelity note: the recipe links Debian's glibc
package, which carries Debian patches atop the GNU release mirrored
here; the recipe itself names the toolchain image for anyone
rebuilding the exact bytes.

## The iSCSI initiator (`/sbin/iscsid`, `/sbin/iscsiadm`)

License: GPL-2.0 or later. Copyright the open-iscsi contributors
(including Cisco Systems, Dicon Zhang, Red Hat, and others).

Built from pinned source by this repository's own recipe
(open-iscsi/fetch.sh), statically linking: libkmod (LGPL-2.1 or
later, copyright the kmod contributors), libblkid and libmount from
util-linux (LGPL-2.1 or later, copyright Karel Zak and others),
libeconf (MIT, copyright SUSE LLC), OpenSSL (Apache-2.0, copyright
the OpenSSL Project), and musl (MIT, copyright Rich Felker and
contributors). Sources are mirrored under `sources/open-iscsi/` and,
for the alpine-packaged static libraries, `sources/toolchain/`; the
build container is pinned by digest in the recipe. Alpine's musl and
util-linux packages carry small distribution patches atop the
upstream tarballs mirrored here.

## The NFS client (`/sbin/mount.nfs`)

License: GPL-2.0. Copyright the Linux NFS maintainers and
contributors.

Built from pinned source by this repository's own recipe
(nfs-utils/fetch.sh), statically linking libtirpc (BSD-3, copyright
Sun Microsystems, Inc.) and the same util-linux and musl libraries as
the iSCSI initiator. Sources are mirrored under `sources/nfs-utils/`
and `sources/toolchain/`.

## systemd-boot (`systemd-bootx64.efi`)

License: LGPL-2.1 or later. Copyright the systemd contributors.

Vendored prebuilt from Ubuntu's archive, unmodified. The Ubuntu
source package — upstream tarball, packaging, and .dsc — is mirrored
under `sources/systemd-boot/`.

## GRUB (`grub-boot.img`, `grub-core.img`)

License: GPL-3.0 or later. Copyright the Free Software Foundation.

The BIOS boot stages, produced from Ubuntu's grub-pc-bin package by
grub-mkimage with no source modification. The Ubuntu source package
is mirrored under `sources/grub/`.

## The CA trust store (`/etc/ssl/certs/ca-certificates.crt`)

License: MPL-2.0 (the bundle; it derives from Mozilla's certdata.txt).

The Mozilla CA program's root certificates, as extracted and
published by the curl project. The PEM bundle is its own source form
and is mirrored under `sources/trust/`.

## The PCI naming database (`/usr/share/hwdata/pci.ids`)

License: BSD-3-Clause (dual-licensed with GPL-2.0-or-later; liken
redistributes it under the BSD option). Copyright Martin Mares and
Albert Pool.

The pci.ids compilation, as snapshotted by the hwdata project: the
database that names PCI vendors and devices in the Machine's
unclaimed-hardware report. The file is its own source form and is
mirrored under `sources/hwdata/`.

## liken's programs and their Go dependencies

liken's binaries (`/liken`, the operators, the log relays, and the
`liken` toolkit) are MIT-licensed and statically compile in the Go
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

The MIT and BSD texts these modules are offered under are reproduced
in the appendix once each; the copyright lines above complete them
for each holder.
