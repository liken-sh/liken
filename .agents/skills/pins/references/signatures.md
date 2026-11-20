# The upstreams that sign their releases

When an upstream signs its checksum file or its release file,
`--bump` verifies the signature before it writes a digest. This list
gives, for each upstream that signs, what it signs and how its
`latest.sh` verifies it.

- **kernel.org** signs the `sha256sums.asc` file of each directory
  with its checksum autosigner. To read a digest from one, call
  `trust/keys/kernel-org-digest.sh <URL of sha256sums.asc> <file>`.
  The script verifies the file against
  `trust/keys/kernel-org-autosigner.asc` and prints the digest. Add
  each new caller to the list in `trust/keys/README.md`.
- **The Kernel PPA** signs the `CHECKSUMS` file of each build.
  `liken/kernel/latest.sh` verifies `CHECKSUMS.gpg` against
  `liken/kernel/kernel-ppa.asc`, and accepts one pinned fingerprint.
- **dbus** signs each release tarball.
  `liken/userland/latest.sh --bump dbus` verifies the tarball against
  `liken/userland/dbus.asc`, accepts one pinned fingerprint, and pins
  the digest of the verified bytes.
- **IANA** signs each tzdata and tzcode tarball, and **the release
  managers of weston** sign each weston tarball.
  `liken/userland/latest.sh --bump tzdata` and `weston/latest.sh`
  verify the tarball against the key file beside the script before
  they pin its digest.
- **Flux** signs its checksums file, and **Trivy** signs each release
  archive, with sigstore. `liken/flux/latest.sh` and
  `ci/latest.sh --bump trivy` verify the bundle with cosign, from the
  `cosign` entry of `toolchains/images.toml`. They accept only the
  certificate identity of the project's release workflow at the
  release's tag, from GitHub's OIDC issuer.
- **prek** publishes a GitHub attestation of each archive.
  `ci/latest.sh --bump prek` verifies it with `gh attestation verify`.
- **Debian** signs the `InRelease` file of each suite. `InRelease`
  lists the digest of each `Packages` index, and each index lists the
  digest of each package. `liken/dev-cluster/firmware/latest.sh` gets
  the digest of `ovmf-generic` through that chain with `archive_deb`,
  checked against Debian's archive keys in
  `trust/keys/debian-archive.asc`. `vulkan/latest.sh` verifies the
  `InRelease` file of the snapshot against the same keys before it
  pins its digest in `vulkan/snapshot.sha256`.
- **Ubuntu** signs its archive the same way.
  `liken/grub/latest.sh` and `liken/systemd-boot/latest.sh` get the
  digests of their packages through the chain with
  `ubuntu_deb_digest`, checked against `trust/keys/ubuntu-archive.asc`.
  They get the digests of their source packages through the suite's
  Sources index with `ubuntu_source_files`. `indi/latest.sh` verifies
  the `InRelease` file of each Ubuntu suite of its snapshot against the
  same key. It verifies the `InRelease` file of each PPA against the
  key beside the script, with `--signer`, and then pins the digests in
  `indi/snapshot.sha256`. Launchpad also signs the file of a PPA with
  the older key of the PPA, so `--signer` accepts the file when the
  named key made a valid signature.
