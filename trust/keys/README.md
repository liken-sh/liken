# Signing keys

The public keys that the `latest.sh --bump` scripts check upstream
signatures against before they copy a digest into a pin. A key here
has more than one reader in the repository. A key that one script
reads stays beside that script.

## kernel.org's checksum autosigner

`kernel-org-autosigner.asc` is the key of `Kernel.org checksum
autosigner <autosigner@kernel.org>`, fingerprint
`B886 8C80 BA62 A1FF FAF5 FDA9 632D 3A06 589D A6B1`.

kernel.org's autosigner is a system off the network that hashes every
upload to kernel.org's central storage and clearsigns the result as
the `sha256sums.asc` file of each directory, with a key that exists
only there. <https://www.kernel.org/signature.html> describes it. The
signature proves that a file is the one on kernel.org's central
storage. It does not replace a maintainer's own signature of a
tarball, which nothing in the repository checks.

The key comes from kernel.org's pgpkeys repository,
`keys/632D3A06589DA6B1.asc` in
<https://git.kernel.org/pub/scm/docs/kernel/pgpkeys.git>, and
kernel.org's web key directory serves the same key for
`autosigner@kernel.org`. A new key is a change that a person makes by
hand, after checking both sources, and it changes the fingerprint
pinned as `signer` in `kernel-org-digest.sh` too.

`kernel-org-digest.sh` fetches a `sha256sums.asc` file, verifies it
against this key, and prints the digest it lists for one file. These
bumps call it:

- `liken/kernel/latest.sh`, for the kernel's source tarball, which
  `liken/licensing/sources.sh` mirrors
- `liken/linux-firmware/latest.sh`, for the linux-firmware and
  wireless-regdb tarballs
- `liken/userland/latest.sh`, for the e2fsprogs, nfs-utils, kmod, and
  BlueZ tarballs

## Ubuntu's archive signing key

`ubuntu-archive.asc` is the key of `Ubuntu Archive Automatic Signing
Key (2018) <ftpmaster@ubuntu.com>`, fingerprint
`F6EC B376 2474 EDA9 D21B 7022 8719 20D1 991B C93C`.

Ubuntu signs no package. Its archive signs the `InRelease` file of
each suite, such as `tenacious` or `resolute-updates`. The file lists
the digest of each `Packages` index, and each index lists the digest
of each package. `ubuntu_deb_digest` in `toolchains/pin.sh` verifies
that chain against this key before a bump pins a digest. gpgv fails
on a signature whose key it does not have, so the file must hold
every key that signs the suites that a bump reads.

The key is the one that the `ubuntu-keyring` package installs in
`/usr/share/keyrings/ubuntu-archive-keyring.gpg`, exported in armored
form with `gpg --export --armor`. keyserver.ubuntu.com serves the same
fingerprint. A new key is a change that a person makes by hand, after
checking both sources.

These bumps call `ubuntu_deb_digest`:

- `liken/grub/latest.sh`, for the `grub-pc-bin` and `grub2-common`
  packages
- `liken/systemd-boot/latest.sh`, for the `systemd-boot-efi` package

`indi/latest.sh` verifies the `InRelease` file of each Ubuntu suite
that the INDI images install from against the key, before it pins
the file's digest in `indi/snapshot.sha256`.

## Debian's archive signing keys

`debian-archive.asc` holds the keys of `Debian Archive Automatic
Signing Key (12/bookworm) <ftpmaster@debian.org>`, fingerprint
`B8B8 0B5B 623E AB6A D877 5C45 B7C5 D7D6 3509 47F8`, and `Debian
Archive Automatic Signing Key (13/trixie) <ftpmaster@debian.org>`,
fingerprint `04B5 4C3C DCA7 9751 B16B C6B5 2256 29DF 75B1 88BD`.

Debian signs the `InRelease` file of each suite, and signs no
package. Debian signs each suite with the archive keys of more than
one release, and gpgv fails on a signature whose key it does not
have, so the file holds the archive signing key of each release that
signs sid. <https://ftp-master.debian.org/keys.html> lists those
keys. A new key is a change that a person makes by hand, after
comparing it with the keys that the `debian-archive-keyring` package
installs.

These bumps verify a signature against the file:

- `liken/dev-cluster/firmware/latest.sh`, through `archive_deb`, for
  the `ovmf-generic` package of sid
- `vulkan/latest.sh`, for the `InRelease` file of the snapshot.debian.org
  date that the bases and `astrometry` install from, whose digest
  `vulkan/snapshot.sha256` pins

No image build reads this directory, so a change here needs no new
revision of `trust`.
