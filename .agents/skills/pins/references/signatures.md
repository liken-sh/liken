# How a bump verifies a signature

When an upstream signs its checksum file or its release file,
`--bump` verifies the signature before it writes a digest. Each way
that an upstream signs has one way to verify it, below, with an
example to copy. To find every script that verifies a signature, run
`git grep -lE 'verify_signature|kernel-org-digest|archive_deb|ubuntu_deb_digest|cosign|attestation' -- '*latest.sh'`.
`trust/keys/README.md` names the callers of each shared key.

- **kernel.org's checksum autosigner** signs the `sha256sums.asc`
  file of each directory. To read a digest from one, call
  `trust/keys/kernel-org-digest.sh <URL of sha256sums.asc> <file>`.
  The script verifies the file against
  `trust/keys/kernel-org-autosigner.asc` and prints the digest. Add
  each new caller to the list in `trust/keys/README.md`. `ci pins`
  fails on a `latest.sh` that reads a `sha256sums.asc` file and does
  not call the script.
- **A PGP key of the project** signs a checksum file or each release
  tarball. Keep the key file beside the script, and verify with
  `verify_signature`. Pin the digest of the verified bytes. Examples
  are the weston tarball in `weston/latest.sh` and the tzdata tarball
  in `liken/userland/latest.sh`. Add `--signer <fingerprint>` to
  accept only one pinned key, as the `CHECKSUMS.gpg` file of the
  Kernel PPA in `liken/kernel/latest.sh` and the dbus tarball do.
- **sigstore.** Flux signs its checksums file, and Trivy signs each
  release archive. `liken/flux/latest.sh` and
  `ci/latest.sh --bump trivy` verify the bundle with cosign, from the
  `cosign` entry of `toolchains/images.toml`. They accept only the
  certificate identity of the project's release workflow at the
  release's tag, from GitHub's OIDC issuer.
- **A GitHub attestation.** prek publishes one for each archive.
  `ci/latest.sh --bump prek` verifies it with `gh attestation verify`.
- **A Debian or Ubuntu archive** signs the `InRelease` file of each
  suite. `InRelease` lists the digest of each `Packages` index, and
  each index lists the digest of each package. `archive_deb` reads a
  package's digest through that chain, and `ubuntu_deb_digest` does
  the same against `trust/keys/ubuntu-archive.asc`. Debian's keys are
  in `trust/keys/debian-archive.asc`. `ubuntu_source_files` reads the
  digests of a source package through the suite's Sources index.
  Examples are `liken/grub/latest.sh` and
  `liken/dev-cluster/firmware/latest.sh`.
- **A snapshot of a suite.** A bump that pins the `InRelease` file
  of a snapshot verifies it against the archive's keys first, as
  `vulkan/latest.sh` does for `vulkan/snapshot.sha256`. For a PPA,
  `indi/latest.sh` verifies `InRelease` against the key beside the
  script, with `--signer`. Launchpad also signs the file of a PPA with
  the older key of the PPA, so `--signer` accepts the file when the
  named key made a valid signature.
