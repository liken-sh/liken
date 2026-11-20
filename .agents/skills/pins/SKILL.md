---
name: pins
description: Adds or changes a pin in the liken repository - a VERSION file, a fetch.sh, a latest.sh, a pin.toml manifest, a digest, a purl, a CPE, an ADD --checksum in a Dockerfile, an image of toolchains/images.toml, or the commit of a GitHub Action. Covers the rule that every pin follows the newest upstream release, the rules of a download, the library toolchains/pin.sh, the manifest, the signature that a bump verifies, and each pattern that the ci pins check refuses. Use before writing or changing any of those files, when a component starts to download a new file, and when ci pins or the pins hook of prek fails. To move existing pins to newer releases, use the bump-components skill.
---

# Pins

A pin names the bytes that a build downloads: a version, and the
digest of each file. This skill holds the rules that every pin
follows and the library that every pin script uses. The
`bump-components` skill holds the procedure that moves the pins to
newer releases, and the `images` skill holds the form of a
Dockerfile.

## Every pin stays current

`liken` takes the newest release of everything it builds with and
ships, because a pin that stays on an old line collects faults that
upstream already fixed. The bases stayed on Debian stable, and Weston
fell two major versions behind and drew the wrong colors. So a pin is
current when it matches the newest upstream release that is not a
pre-release, with no soak time:

- A pin that `liken` builds from source follows the newest release of
  the upstream project.
- A pin that `liken` takes from a package repository, a registry, or
  a channel server follows the newest channel that the repository
  offers to install as a whole, and the newest package in that
  channel: Debian sid, Alpine `edge`, Ubuntu's development series,
  and k3s's `latest` channel. A PPA counts as its own repository,
  and its newest channel is the newest series it publishes.
- A pin that must stay back is a hold, and its report row states the
  reason. A comment alone does not count.

Every pin has a `latest.sh` beside it, and `make versions` at the top
of the repository runs all of them and prints one table. A pin that
several components share has one owner: `toolchains/images.toml`
names each outside image once, and checks in `ci/` fail when two
copies of a Go version, a Rust version, a Go module, a crate, or an
image tag disagree. The `bump-components` skill under
`.agents/skills` holds the row format and the procedure, and root
plan 83 holds the reasons.

## Pins and downloads

A build uses only the bytes that a commit named, so a person who
reviews the commit reviews what every later build downloads. Every
pin in the repository follows these rules, and
`cd ci && go run . pins -root ..` fails CI on each break that a
pattern can find. Every `fetch.sh` and `latest.sh` sources
`toolchains/pin.sh`, which holds one implementation of each step of a
pin, and prek runs `ci pins` on each commit that changes a pin. The
last section of this skill holds the library, the patterns, and the
comment that excuses one line, and the `bump-components` skill holds
the procedure of a bump.

- The digest of every file that a pin downloads is in the
  repository, and `latest.sh --bump` writes it. No `fetch.sh`,
  Dockerfile, or other build step reads a digest from the network,
  because a checksum file read at build time accepts whatever the
  upstream serves that day.
- A pin whose files a release ships holds a manifest, `pin.toml`,
  beside its `fetch.sh`, because the SBOM of a release must name each
  of those files, and only the pin knows where it came from. The
  manifest has one table for each file that the fetch downloads, with
  its `version`, `url`, `sha256`, `purl`, and the SPDX expression of
  its `license`, and an optional CycloneDX `scope` of `excluded` for a
  file that only the build reads. It also has a table, with the scope
  `excluded`, for each file of the corresponding source that
  `liken/licensing/sources.sh` mirrors onto the release channel, so
  the bump that moves a binary moves its source too. The manifest is
  the one copy of the URL and the digest: `latest.sh --bump` writes
  them with `set_pin`, `fetch.sh` and the source mirror download with
  `fetch_pinned`, and every other reader takes a field with
  `pin_value`. A purl follows the purl specification for the file's
  kind: `pkg:deb/ubuntu/...?arch=amd64` for a package of Ubuntu's
  archive, `?arch=source` for a file of its source package,
  `pkg:github/<owner>/<name>@<tag>`
  for a GitHub release or tag, and `pkg:generic/<name>@<version>` with
  the `download_url` and the `checksum` for any other file. A person
  reads the upstream's license and writes the expression. The
  programs that buildroot builds, the closure images, and the Go and
  Rust binaries need no manifest, because buildroot's CycloneDX SBOM,
  the `status.d` records, the build info, and cargo-auditable already
  name them. `unmanifested` in `ci/manifests.go` names each other
  `fetch.sh` that needs no manifest, with the reason.
- A pin whose files an image ships holds a `pin.toml` too, beside the
  Dockerfile that fetches them, because syft finds no package in a
  file that a Dockerfile fetches by its checksum. An entry that an
  image ships also names its CycloneDX `type`, one that syft keeps,
  such as `library`, `application`, or `machine-learning-model`, and
  the `cpe` that NVD lists the upstream under, when NVD has one. The
  Dockerfile keeps a copy of the URL and the digest in its
  `ADD --checksum`, because BuildKit reads the checksum from the
  Dockerfile's text, and the pin's `latest.sh --bump` writes both
  copies. The image build runs `image-sbom` from
  `toolchains/manifest`, through the build context `manifest`, and the
  image carries the record that it writes at
  `/usr/share/doc/liken/<image>.cdx.json`. syft copies the components
  of each such file into the image's SBOM. `toolchains/manifest` is
  also the reader that the SBOM of a release uses, so a field means
  the same in both. `ci pins` fails on an `ADD --checksum` that no
  manifest beside the Dockerfile names at that digest, and on a
  Dockerfile that fetches a manifest's file and runs no `image-sbom`.
  A Debian package needs no entry, because the closure that unpacks
  it writes its dpkg record, and `unmanifestedDownloads` in
  `ci/manifests.go` names each Dockerfile whose downloads only its
  build reads.
- When the upstream signs the checksum file that a bump copies a
  digest from, the bump verifies the signature first. Without that
  check, a mirror that changes the file changes the pin. The keys are
  in the repository, and a key that more than one component reads is
  in `trust/keys/`.
- A download goes to a `.partial` file, which takes its name in the
  cache only after its digest matches. A cached file that does not
  match is downloaded again. So a torn or changed download never
  takes the name of a good one.
- Every `FROM` and `COPY --from` names an image of
  `toolchains/images.toml`, or an image of the repository by its bare
  name, and a shell script that runs an outside image reads its
  reference with `image_of`, so that one entry pins each outside
  image.
- Every `VERSION`, every `fetch.sh`, and every Dockerfile that
  downloads has a `latest.sh` that `make versions` runs, so no pin
  falls behind where the report cannot show it.
- A check that a binary is static accepts both "statically linked"
  and "static-pie linked", because gcc on Alpine builds a static PIE
  for `-static`.
- Every `uses:` of a GitHub Action names the commit of a release, as
  in `actions/checkout@<commit> # <release>`, because the owner of an
  action can move a tag to other code, and the publish jobs run their
  actions with a token that writes the registry and one that signs
  images. `actionPins` in `ci/actions.go` names each repository of
  actions once, the generated workflows and the composite actions
  name the same commits, and `ci/latest.sh --bump <repository>`
  writes the commit of the newest release's tag.

## A new or changed `latest.sh` or `fetch.sh`

The rules above give the reasons. This section gives the library that
every pin script uses, and the check in `ci/` that holds the scripts
to it.

**The library.** Every `fetch.sh` and `latest.sh` sources
`toolchains/pin.sh` by its path from the script's directory, after
`set -euo pipefail`:

```bash
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"
```

The directive lets the component's shellcheck hook, which runs with
`-x`, read the library. The library holds one implementation of each
step, and the comment above each function in `pin.sh` gives its
arguments and the reason for its behavior:

- `require_tools` stops on a missing tool, and `fail` stops with a
  message that names the script.
- `fetch_text`, `url_exists`, `newest_tag`, `tag_commit`,
  `github_api`, and `registry_digest` read an upstream, with the retries and time limits
  that every read takes. `github_api` sends `GITHUB_TOKEN` or the gh
  CLI's token when one is at hand.
- `fetch_verified [--resume] <url> <digest> <path>` puts a pinned file
  in the cache: it keeps a cached file that matches, downloads anything
  else into a `.partial` file, deletes a `.partial` that does not match
  and stops, and moves a match into place. `--resume` is for a large
  file. The digest is the one the script pins, and an empty digest
  stops the fetch.
- `fetch_pinned [--resume] <entry> <version> <path>` fetches the file
  that an entry of the `pin.toml` beside the script names, through
  `fetch_verified`, and stops when the entry is at another version
  than the one the fetch builds. `pin_value <entry> <field>
  [<manifest>]` prints one field of a manifest, and `set_pin <entry>
  <version> <url> <sha256> <purl>` writes a bump into one entry.
  `generic_purl`, `github_purl`, and `deb_purl` print the purl of each
  kind.
- `fetch_file` downloads a file whose digest the repository does not
  hold yet, such as a checksum file or a signature, and
  `digest_of_url` prints the sha256 of a download for an upstream that
  publishes no checksum.
- `verify_signature [--signer <fingerprint>] <key.asc> <signature>
  [<file>]` checks a detached signature, or a clearsigned file and
  prints its signed text.
- `archive_deb <key.asc> <suite URL> <index> <package> [<version>]`
  prints the version and the sha256 of one package of a Debian or
  Ubuntu archive, through the suite's signed `InRelease` file and the
  `Packages` index that it lists.
- `launchpad_published`, `ubuntu_development_series`, and
  `ubuntu_newest_in` read Launchpad's API for an Ubuntu source
  package, and `ubuntu_deb_digest` takes the digest of one of its
  packages through `archive_deb`, against
  `trust/keys/ubuntu-archive.asc`. `ubuntu_source_files` takes the
  digests of the files of the source package through `archive_source`
  and the suite's Sources index, and `set_ubuntu_source_pins` writes
  them into the manifest at their Launchpad URLs.
- `container_runtime` and `image_of <key>` give a build its docker or
  podman and its image from `toolchains/images.toml`.
- `row` prints one report row of seven columns, with `?` for an empty
  latest.
- `bump_needed`, `bumped`, and `next_step` print the lines of a bump.
  `set_line`, `set_var`, `set_toml`, `set_entry`, and `add_entry` write
  a bump into the file that holds the pin, and each one stops when its
  pattern matches no line or more than one, so a bump never reports a
  move that it did not write.

When a new script needs a step that the library lacks, add the step
to `pin.sh` with a comment, and do not write it in the script.

**The digest.** `latest.sh --bump` reads the upstream's checksum once,
when the pin moves, and writes it into the repository: the
`pin.toml` beside `fetch.sh` for a pin whose files a release ships, a
`declare -A` table in `fetch.sh` for a pin that no release ships, a
buildroot `.hash` file under `liken/userland/patches/`, an `ARG` of a
Dockerfile, the `--checksum` of an `ADD`, or a constant in `ci/`. The
fetch and the build read only that copy. Each `--bump` prints one line
that says where the digest came from, such as "the digest came from
sha256sums.asc, signed by kernel.org's checksum autosigner".

**The manifest.** "Pins and downloads" above says
which pins hold a `pin.toml` and what each field means. A new pin that
a release ships gets one table for each file that its `fetch.sh`
downloads:

```toml
[k3s]
version = "v1.37.1+k3s1"
url = "https://github.com/k3s-io/k3s/releases/download/v1.37.1%2Bk3s1/k3s"
sha256 = "92b94582eb7b34a8cf532e6006842b9ed215a6783cf56a3dd6271fd35243b2c0"
purl = "pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1"
license = "Apache-2.0"
```

Its `latest.sh --bump` computes the URL and the purl of the new
version and writes all four with `set_pin`, and its `fetch.sh` calls
`fetch_pinned`. Write the license from the upstream's own files: its
`COPYING` or `LICENSE`, its SPDX headers, or the package's
`copyright` file. Use a `LicenseRef-` for terms that the SPDX list
does not name, and say in the comment above the table where the
terms are. A bump keeps the license, so check it when a release
changes the upstream's license. The trust image's Dockerfile fetches
the CA bundle by a `--checksum` of its own, because BuildKit reads the
checksum from the Dockerfile's text, and `trust/latest.sh --bump`
writes both copies.

The manifest of a pin whose license asks for the source holds an
entry for each file of that source too, with the scope `excluded`,
because `liken/licensing/sources.sh` mirrors it onto the release
channel and no build reads it: the kernel's tarball in
`liken/kernel/pin.toml`, k3s-root's recipe with the iptables and
buildroot releases it builds from in `liken/xtables/pin.toml`, and
the three files of each Ubuntu source package in
`liken/systemd-boot/pin.toml` and `liken/grub/pin.toml`. The same
`--bump` that moves the binary writes them. `sources.sh` downloads
each one with `fetch_pinned`, and names the manifest as its last
argument.

A pin whose files an image ships holds a `pin.toml` beside the
Dockerfile that fetches them, with a `type` that syft keeps and the
`cpe` that NVD lists, and its bump writes the entry with `set_pin`,
the CPE as the last argument, and the same URL and digest into the
Dockerfile's `ADD --checksum`. The image build runs `image-sbom` on
the manifest, and "Pins and downloads" above gives the rule: `weston/pin.toml`, `library-operator/pin.toml`, and
`library-operator/appearances/models/pin.toml`, whose models no bump
moves.

An entry of the OS that NVD lists states a `cpe` and a `type` that a
scanner keeps too: the kernel, k3s, GRUB, systemd-boot, and the Intel
microcode. The scan of each release's SBOM matches those files by the
CPE alone, and "Vulnerability scans" in the `sbom-and-scans` skill gives the
scan. NVD lists an upstream at its own version, so the bump writes the
CPE with `cpe_version`, which prints `1.37.2` for `v1.37.2+k3s1` and
`2.14` for `2.14-2ubuntu3`, and `ci pins` accepts a CPE at the entry's
version or at that part of it. A new pin of the OS gets the same two
fields when NVD lists its upstream.

To list the purl of every pin that a release ships:

```sh
tomlq -r '.[] | .purl' liken/*/pin.toml trust/pin.toml
```

**The signature.** When the upstream signs its checksum file or its
release file, `--bump` verifies the signature before it writes a
digest, and stops when the signature is missing, does not verify, or
names another signer:

- kernel.org signs the `sha256sums.asc` file of each directory with
  its checksum autosigner. Call
  `trust/keys/kernel-org-digest.sh <URL of sha256sums.asc> <file>`,
  which verifies the file against `trust/keys/kernel-org-autosigner.asc`
  and prints the digest. Add the caller to the list in
  `trust/keys/README.md`.
- The Kernel PPA signs each build's `CHECKSUMS` file.
  `liken/kernel/latest.sh` verifies `CHECKSUMS.gpg` against
  `liken/kernel/kernel-ppa.asc` and accepts one pinned fingerprint.
- dbus signs each release tarball. `liken/userland/latest.sh --bump dbus`
  verifies the tarball against `liken/userland/dbus.asc`, accepts one
  pinned fingerprint, and pins the digest of the verified bytes.
- IANA signs each tzdata and tzcode tarball, and weston's release
  managers sign each weston tarball. `liken/userland/latest.sh --bump
  tzdata` and `weston/latest.sh` verify the tarball against the key
  file beside the script before they pin its digest.
- Flux signs its checksums file, and Trivy signs each release archive,
  with sigstore. `liken/flux/latest.sh` and `ci/latest.sh --bump trivy`
  verify the bundle with cosign, from the `cosign` entry of
  `toolchains/images.toml`, and accept only the certificate identity
  of the project's release workflow at the release's tag, from
  GitHub's OIDC issuer.
- prek publishes a GitHub attestation of each archive.
  `ci/latest.sh --bump prek` verifies it with `gh attestation verify`.
- Debian signs each suite's `InRelease` file, which lists the digest
  of each `Packages` index, and the index lists the digest of each
  package. `liken/dev-cluster/firmware/latest.sh` takes the digest of
  `ovmf-generic` through that chain with `archive_deb`, against
  Debian's archive keys in `trust/keys/debian-archive.asc`. `vulkan/latest.sh`
  verifies the snapshot's `InRelease` against the same keys before it
  pins its digest in `vulkan/snapshot.sha256`.
- Ubuntu signs its archive the same way. `liken/grub/latest.sh` and
  `liken/systemd-boot/latest.sh` take their digests through the chain
  with `ubuntu_deb_digest`, against `trust/keys/ubuntu-archive.asc`,
  and the digests of their source packages through the suite's
  Sources index with `ubuntu_source_files`.
  `indi/latest.sh` verifies the `InRelease` file of each Ubuntu suite
  of its snapshot against the same key, and each PPA's against the
  key beside it with `--signer`, before it pins the digests in
  `indi/snapshot.sha256`. Launchpad signs a PPA's file with the PPA's
  older key as well, and `--signer` accepts the file on the valid
  signature of the named key.

A key that one script reads stays beside that script. A key that a
second component reads moves to `trust/keys/`, with a section in its
`README.md` that says where the key comes from. When the upstream
signs nothing, the script's comment and the printed line say so, so
the commit says what stands behind the digest.

**A static binary.** gcc on Alpine builds a static PIE for `-static`,
so a check with file(1) accepts both words:
`grep -qE "statically linked|static-pie linked"`.

**A static program from buildroot.** buildroot downloads inside its
builder, so the check below cannot read its downloads. The pins are
`liken/userland/VERSION` and the `buildroot_sha256` table of
`liken/userland/fetch.sh` for buildroot itself, buildroot's own `.hash`
files for every package at that commit, the `.hash` file beside each
recipe under `liken/userland/iscsi/` and `liken/userland/bluetooth/`,
and the `.hash` file under `liken/userland/patches/` for each version
that `versions.mk` sets. `BR2_DOWNLOAD_FORCE_CHECK_HASHES` in
`liken/userland/common.config` makes buildroot stop on a download that
no `.hash` file lists, and `fetch.sh` pins the SPDX schema that
`utils/generate-cyclonedx` would otherwise download. The bluetoothd
Dockerfile downloads buildroot itself with `ADD --checksum`, and
`ci pins` fails when its commit or digest differs from the pin of
`liken/userland`.

**The check.** `cd ci && go run . pins -root ..` runs
`ci/fetches.go`, which reads the scripts by pattern and fails when:

- a `fetch.sh` or `latest.sh` does not source `toolchains/pin.sh` by
  its path from the script's directory, or runs curl or wget as a
  command of its own;
- a `fetch.sh` names a checksum file, such as `SHA256SUMS`,
  `sha256sums.asc`, `checksums.txt`, or `<file>.sha256`, or a
  Dockerfile's `ADD` or `RUN` downloads one;
- a curl or wget in a `fetch.sh` or in `toolchains/pin.sh` writes a
  file whose name does not end in `.partial`;
- a `VERSION` or `fetch.sh` has no `latest.sh` in its directory, a
  Dockerfile that downloads has none in its directory or above it, or
  a `latest.sh` is not executable or is under a directory that
  `versions.sh` skips;
- a `latest.sh` reads a `sha256sums.asc` file and does not call
  `trust/keys/kernel-org-digest.sh`;
- a `fetch.sh` has no `pin.toml` beside it, unless `unmanifested` in
  `ci/manifests.go` names its directory; an entry of a `pin.toml`
  lacks a field, holds an unknown one, is at another version than the
  `VERSION` beside it, or has a purl at another version or, for
  `pkg:generic`, with another `download_url` or `checksum`; the
  `fetch.sh` holds a digest of its own or does not call
  `fetch_pinned`; an entry has a `type` that CycloneDX does not name,
  or a `cpe` at another version;
- a Dockerfile `ADD`s a URL by a checksum, after its `ARG` defaults
  are put in, that no `pin.toml` beside it names at that digest,
  unless the file is a Debian package or `unmanifestedDownloads` in
  `ci/manifests.go` names the Dockerfile; a Dockerfile fetches a file
  of a manifest and runs no `image-sbom`; or a sums file beside a
  `pin.toml`, such as `models.sha256`, names an entry's file by
  another digest;
- the bluetoothd Dockerfile names an SPDX schema version and digest
  that the `spdx_schema_sha256` table of `liken/userland/fetch.sh`
  does not pin;
- a shell script names an image by a digest of its own, such as
  `ghcr.io/oras-project/oras:<tag>@sha256:<digest>`, and does not read
  the reference from `toolchains/images.toml` with `image_of`;
- `liken/licensing/sources.sh` holds a digest, because each source
  that it mirrors is an entry of the `pin.toml` of the pin that the
  source belongs to;
- a grep for "statically linked" does not accept "static-pie linked";
- a `uses:` in a workflow or a composite action under `.github/`
  names another repository's action by anything but the commit and
  the release that `actionPins` in `ci/actions.go` names, or a
  `docker://` image by anything but a digest.

The pins hook of the root `.pre-commit-config.yaml` runs the check on
each commit that changes a pin script, a Dockerfile, a `VERSION`, a
`pin.toml`, `toolchains/images.toml`, `versions.sh`, the library,
`trust/keys/`, or a file under `.github/`. The check sees only these forms. It does not see a
signature that an upstream other than kernel.org publishes and a bump
skips. A line that breaks a pattern for a reason the check cannot see
takes a comment line directly above it with `fetch-check: allow` and
the reason.
