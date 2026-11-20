---
name: pins
description: Adds or changes a pin in the liken repository - a VERSION file, a fetch.sh, a latest.sh, a pin.toml manifest, a digest, a purl, a CPE, an ADD --checksum in a Dockerfile, an image of toolchains/images.toml, or the commit of a GitHub Action. Covers the rule that every pin follows the newest upstream release, the rules of a download, the library toolchains/pin.sh, the manifest, the signature that a bump verifies, and each pattern that the ci pins check rejects. Use before writing or changing any of those files, when a component starts to download a new file, when a build applies an upstream patch to a pinned release, when a scanner or the GitHub security tab must know the exact version that liken ships, and when ci pins or the pins hook of prek fails. To move existing pins to newer releases, use the bump-components skill.
---

# Pins

A pin records exactly which bytes a build downloads: a version, and
the digest of each file. This skill has the rules that every pin
follows, and the library that every pin script uses. The procedure
that moves pins to newer releases is in the `bump-components` skill.
The form of a Dockerfile is in the `images` skill.

## Every pin stays current

`liken` uses the newest release of everything that it builds with
and ships. A pin that stays on an old release line keeps the faults
that upstream has already fixed. For example, the bases stayed on
Debian stable, and Weston fell two major versions behind and drew the
wrong colors. So a pin is current when it matches the newest upstream
release that is not a pre-release, with no soak time.

- **A pin that `liken` builds from source** follows the newest
  release of the upstream project.
- **A pin from a package repository, a registry, or a channel
  server** follows the newest channel that the repository offers to
  install as a whole, and the newest package in that channel. These
  channels are Debian sid, Alpine `edge`, Ubuntu's development
  series, and k3s's `latest` channel. A PPA counts as its own
  repository, and its newest channel is the newest series that it
  publishes.
- **A hold** is a pin that must stay back. Its row in the report
  gives the reason. A comment alone does not count as a hold.

Every pin has a `latest.sh` beside it. `make versions`, at the top of
the repository, runs all of them and prints one table.

A pin that several components share has one owner. For example,
`toolchains/images.toml` lists each image from outside the repository
once. Checks in `ci/` fail when two copies of a Go version, a Rust
version, a Go module, a crate, or an image tag disagree.

The format of a report row and the procedure of a bump are in the
`bump-components` skill under `.agents/skills`. The reasons are in
root plan 83.

## Pins and downloads

A build uses only the bytes that a commit named. So a person who
reviews the commit has reviewed what every later build downloads.

Every pin in the repository follows the rules below.
`cd ci && go run . pins -root ..` fails CI on each break of a rule
that a pattern can find, and prek runs `ci pins` on each commit that
changes a pin. Every `fetch.sh` and `latest.sh` sources
`toolchains/pin.sh`, which has one implementation of each step of a
pin. The last section of this skill describes the library, the
patterns, and the comment that excuses one line. The procedure of a
bump is in the `bump-components` skill.

- **The digest is in the repository.** The digest of every file that
  a pin downloads is in the repository, and `latest.sh --bump` writes
  it. No `fetch.sh`, Dockerfile, or other build step reads a digest
  from the network. A checksum file that a build reads at build time
  accepts whatever the upstream serves that day.

- **A pin whose files a release ships has a manifest.** The manifest
  is a file named `pin.toml`, beside the pin's `fetch.sh`. The SBOM of
  a release must list each of those files, and only the pin records
  where each file came from.

  The manifest has one table for each file that the fetch downloads.
  Each table has the file's `version`, `url`, `sha256`, `purl`, and
  `license`, which is an SPDX expression. A table can also set the
  CycloneDX `scope` to `excluded`, for a file that only the build
  reads.

  When the build applies upstream commits to the release, the entry
  lists them in `backports`. Each item is the URL of a GitHub commit
  with its full 40-digit id. The SBOM records the commits as the
  component's pedigree, so the SBOM does not present the component as
  the stock release.

  `liken/licensing/sources.sh` mirrors the corresponding source of
  some files onto the release channel. The manifest also has a table
  for each file of that source, with the scope `excluded`. So the bump
  that moves a binary moves its source too.

  The manifest is the only copy of the URL and the digest.
  `latest.sh --bump` writes them with `set_pin`. `fetch.sh` and the
  source mirror download with `fetch_pinned`. Every other reader gets
  a field with `pin_value`.

  A purl follows the purl specification for the kind of file:

  - `pkg:deb/ubuntu/...?arch=amd64` for a package of Ubuntu's
    archive, and `?arch=source` for a file of its source package.
  - `pkg:github/<owner>/<name>@<tag>` for a GitHub release or tag.
  - `pkg:generic/<name>@<version>`, with the `download_url` and the
    `checksum`, for any other file.

  A person reads the upstream's license and writes the expression.

  Some programs need no manifest, because another record already
  lists them. buildroot's CycloneDX SBOM lists the programs that
  buildroot builds. The `status.d` records list the packages of a
  closure image. The build info lists the modules of a Go binary, and
  cargo-auditable records the crates of a Rust binary. `unmanifested`
  in `ci/manifests.go` lists each other `fetch.sh` that needs no
  manifest, with the reason.

- **A pin whose files an image ships has a manifest too.** The
  `pin.toml` is beside the Dockerfile that fetches the files, because
  syft finds no package in a file that a Dockerfile fetches by its
  checksum.

  An entry that an image ships also has a CycloneDX `type` that syft
  keeps, such as `library`, `application`, or
  `machine-learning-model`. When NVD lists the upstream, the entry
  also has the `cpe` that NVD lists it under.

  The Dockerfile has a second copy of the URL and the digest in its
  `ADD --checksum`, because BuildKit reads the checksum from the
  text of the Dockerfile. The pin's `latest.sh --bump` writes both
  copies.

  The image build runs `image-sbom` from `toolchains/manifest`,
  through the build context `manifest`. `image-sbom` writes a record,
  and the image includes it at `/usr/share/doc/liken/<image>.cdx.json`.
  syft copies the components of each such file into the image's SBOM.
  The SBOM of a release uses the same reader, `toolchains/manifest`,
  so a field means the same in both.

  `ci pins` fails on an `ADD --checksum` that no manifest beside the
  Dockerfile lists at that digest. It also fails on a Dockerfile that
  fetches a file of a manifest and runs no `image-sbom`.

  A Debian package needs no entry, because the closure that unpacks
  it writes its dpkg record. `unmanifestedDownloads` in
  `ci/manifests.go` lists each Dockerfile whose downloads only its
  build reads.

- **A bump verifies the upstream's signature.** When the upstream
  signs the checksum file that a bump copies a digest from, the bump
  verifies the signature first. Without that check, a mirror that
  changes the file changes the pin. The keys are in the repository. A
  key that more than one component reads is in `trust/keys/`.

- **A download goes to a `.partial` file.** The file gets its real
  name in the cache only after its digest matches. A cached file that
  does not match is downloaded again. So a download that broke off or
  changed never gets the name of a good one.

- **Each outside image has one entry.** Every `FROM` and
  `COPY --from` names an image of `toolchains/images.toml`, or an
  image of the repository by its bare name. A shell script that runs
  an image from outside the repository reads its reference with
  `image_of`. So one entry pins each outside image.

- **Every pin has a `latest.sh`.** Every `VERSION`, every `fetch.sh`,
  and every Dockerfile that downloads has a `latest.sh` that
  `make versions` runs. So no pin can fall behind without the report
  showing it.

- **A check for a static binary accepts a static PIE.** A check that
  a binary is static accepts both "statically linked" and "static-pie
  linked", because gcc on Alpine builds a static PIE for `-static`.

- **A GitHub Action is pinned to a commit.** Every `uses:` of a
  GitHub Action names the commit of a release, as in
  `actions/checkout@<commit> # <release>`. The owner of an action can
  move a tag to other code. The publish jobs run their actions with a
  token that can write to the registry and a token that can sign
  images. `actionPins` in `ci/actions.go` lists each repository of
  actions once, and the generated workflows and the composite actions
  use the same commits. `ci/latest.sh --bump <repository>` writes the
  commit of the tag of the newest release.

A file under `toolchains/`, such as `pin.sh` or `manifest/`, can be
in the build context of a pinned component. So an edit of that file,
even of a comment, can require a new revision. "The bake file and the
base images" in the `images` skill has the rule, and the check to run
before a push.

## A new or changed `latest.sh` or `fetch.sh`

The rules above give the reasons. This section describes the library
that every pin script uses, and the check in `ci/` that makes the
scripts use it.

**The library.** Every `fetch.sh` and `latest.sh` sources
`toolchains/pin.sh` by its path from the script's directory, after
`set -euo pipefail`:

```bash
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"
```

The `shellcheck` directive lets the component's shellcheck hook,
which runs with `-x`, read the library. The library has one
implementation of each step. Before you write a script, read
[`references/pin-library.md`](references/pin-library.md), which
lists each function and what it does. The comment above each
function in `pin.sh` gives its arguments and the reason for its
behavior.

When a new script needs a step that the library does not have, add
the step to `pin.sh` with a comment. Do not write it in the script.

**A test of a new script.** A `--bump` that has nothing to move
proves nothing, and a `--bump` in the tree writes the pin. So test
the bump on a copy in the session's scratch directory:

1. Run the script with no argument, and check its report row.
2. Copy `toolchains/pin.sh` and the component's directory into the
   scratch directory, in the same relative layout, so that the
   script's `source` line finds the library.
3. Set the pin in the copy back to an older version, with a digest
   of zeros. Run `--bump` on the copy. It must write the newest
   version, the real digest, and every copy of the URL.
4. Make the copy fail too, for example with one of the conditions
   under which the script says that it stops. Check that it stops
   with its message.
5. Run `shellcheck -x` on the script, `prek run --files` on the new
   files, and `cd ci && go run . pins -root ..`.

**The digest.** `latest.sh --bump` reads the upstream's checksum
once, when the pin moves, and writes it into the repository. It
writes the digest to one of these places:

- the `pin.toml` beside `fetch.sh`, for a pin whose files a release
  ships
- a `declare -A` table in `fetch.sh`, for a pin that no release
  ships
- a buildroot `.hash` file under `liken/userland/patches/`
- an `ARG` of a Dockerfile, or the `--checksum` of an `ADD`
- a constant in `ci/`

The fetch and the build read only that copy. Each `--bump` prints one
line that says where the digest came from, such as "the digest came
from sha256sums.asc, signed by kernel.org's checksum autosigner".

**The manifest.** "Pins and downloads" above says which pins have a
`pin.toml`, and what each field means. A new pin that a release ships
gets one table for each file that its `fetch.sh` downloads:

```toml
[k3s]
version = "v1.37.1+k3s1"
url = "https://github.com/k3s-io/k3s/releases/download/v1.37.1%2Bk3s1/k3s"
sha256 = "92b94582eb7b34a8cf532e6006842b9ed215a6783cf56a3dd6271fd35243b2c0"
purl = "pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1"
license = "Apache-2.0"
```

The pin's `latest.sh --bump` computes the URL and the purl of the new
version, and writes the version, the URL, the digest, and the purl
with `set_pin`. Its `fetch.sh` calls `fetch_pinned`.

Write the license from the upstream's own files: its `COPYING` or
`LICENSE`, its SPDX headers, or the package's `copyright` file. Use a
`LicenseRef-` for terms that the SPDX list does not include, and say
in the comment above the table where the terms are. A bump does not
change the license, so check it yourself when a release changes the
upstream's license.

The Dockerfile of the trust image fetches the CA bundle with a
`--checksum` of its own, because BuildKit reads the checksum from the
text of the Dockerfile. `trust/latest.sh --bump` writes both copies.

Some licenses require that the source is available. The manifest of
such a pin has an entry for each file of that source, with the scope
`excluded`, because `liken/licensing/sources.sh` mirrors the source
onto the release channel and no build reads it. These are the entries
of that kind:

- the kernel's tarball in `liken/kernel/pin.toml`
- k3s-root's recipe, with the iptables and buildroot releases that it
  builds from, in `liken/xtables/pin.toml`
- the three files of each Ubuntu source package, in
  `liken/systemd-boot/pin.toml` and `liken/grub/pin.toml`

The same `--bump` that moves the binary writes them. `sources.sh`
downloads each one with `fetch_pinned`, and passes the manifest as
the last argument.

A pin whose files an image ships has a `pin.toml` beside the
Dockerfile that fetches them. Each entry has a `type` that syft
keeps, and the `cpe` that NVD lists. The bump writes the entry with
`set_pin`, with the CPE as the last argument. It also writes the same
URL and digest into the `ADD --checksum` of the Dockerfile. The image
build runs `image-sbom` on the manifest, and "Pins and downloads"
above has the rule. These manifests are `weston/pin.toml`,
`library-operator/pin.toml`, and
`library-operator/appearances/models/pin.toml`. No bump moves the
models in the last one.

Some entries of the OS also have a `cpe`, and a `type` that a scanner
keeps, because NVD lists their upstream: the kernel, k3s, GRUB,
systemd-boot, and the Intel microcode. The scan of each release's
SBOM matches those files by the CPE alone. The scan is described in
"Vulnerability scans" in the `sbom-and-scans` skill.

NVD lists an upstream at the upstream's own version. So the bump
writes the CPE with `cpe_version`, which prints `1.37.2` for
`v1.37.2+k3s1` and `2.14` for `2.14-2ubuntu3`. `ci pins` accepts a
CPE at the entry's version, or at that upstream part of it. A new pin
of the OS gets the same two fields when NVD lists its upstream.

To list the purl of every pin that a release ships:

```sh
tomlq -r '.[] | .purl' liken/*/pin.toml trust/pin.toml
```

**The signature.** When the upstream signs its checksum file or its
release file, `--bump` verifies the signature before it writes a
digest. It stops when the signature is missing, does not verify, or
is from another signer. Before you write the bump of an upstream that
signs, read [`references/signatures.md`](references/signatures.md),
which lists what each signing upstream signs and how its `latest.sh`
verifies it.

A key that one script reads stays beside that script. When a second
component reads the key, move it to `trust/keys/`, and add a section
to `trust/keys/README.md` that says where the key comes from. When
the upstream signs nothing, the script's comment and the line that it
prints both say so. Then the commit says what the digest is based on.

**A static binary.** gcc on Alpine builds a static PIE for
`-static`, so a check with file(1) accepts both phrases:
`grep -qE "statically linked|static-pie linked"`.

**A static program from buildroot.** buildroot downloads inside its
builder, so the check below cannot read its downloads. The pins are
these files:

- `liken/userland/VERSION` and the `buildroot_sha256` table of
  `liken/userland/fetch.sh`, for buildroot itself
- buildroot's own `.hash` files, for every package at that commit
- the `.hash` file beside each recipe under `liken/userland/iscsi/`
  and `liken/userland/bluetooth/`
- the `.hash` file under `liken/userland/patches/` for each version
  that `versions.mk` sets

`BR2_DOWNLOAD_FORCE_CHECK_HASHES` in `liken/userland/common.config`
makes buildroot stop on a download that no `.hash` file lists.
`fetch.sh` pins the SPDX schema that `utils/generate-cyclonedx` would
otherwise download. The bluetoothd Dockerfile downloads buildroot
itself with `ADD --checksum`, and `ci pins` fails when its commit or
digest differs from the pin of `liken/userland`.

**The check.** `cd ci && go run . pins -root ..` runs `ci/fetches.go`
and `ci/manifests.go`, which match the scripts and the manifests
against patterns. The check fails in each of these cases:

- A `fetch.sh` or `latest.sh` does not source `toolchains/pin.sh` by
  its path from the script's directory, or it runs curl or wget as a
  command of its own.
- A `fetch.sh` names a checksum file, such as `SHA256SUMS`,
  `sha256sums.asc`, `checksums.txt`, or `<file>.sha256`, or the `ADD`
  or `RUN` of a Dockerfile downloads one.
- A curl or wget in a `fetch.sh` or in `toolchains/pin.sh` writes a
  file whose name does not end in `.partial`.
- A `VERSION` or `fetch.sh` has no `latest.sh` in its directory. Or a
  Dockerfile that downloads has no `latest.sh` in its directory or
  above it. Or a `latest.sh` is not executable, or is under a
  directory that `versions.sh` skips.
- A `latest.sh` reads a `sha256sums.asc` file and does not call
  `trust/keys/kernel-org-digest.sh`.
- A `fetch.sh` has no `pin.toml` beside it, unless `unmanifested` in
  `ci/manifests.go` lists its directory.
- An entry of a `pin.toml` is missing a field, has an unknown field,
  or has a purl at another version. For `pkg:generic`, the check also
  fails on a purl with another `download_url` or `checksum`. The check
  also rejects an entry at another version than the `VERSION` beside
  it, unless `nestedPins` in `ci/manifests.go` lists it as a pin that
  follows another one. Examples are `wireless-regdb` in
  `liken/linux-firmware`, and the iptables and buildroot sources in
  `liken/xtables`.
- An entry has a `type` that CycloneDX does not define, a `cpe` at
  another version, or a `backports` item that is not the URL of a
  GitHub commit.
- The `fetch.sh` has a digest of its own, or does not call
  `fetch_pinned`.
- A Dockerfile `ADD`s a URL with a checksum, and no `pin.toml` beside
  it lists that URL at that digest, after the `ARG` defaults are
  substituted. The exceptions are a Debian package, and a Dockerfile
  that `unmanifestedDownloads` in `ci/manifests.go` lists.
- A Dockerfile fetches a file of a manifest and runs no `image-sbom`.
  Or a sums file beside a `pin.toml`, such as `models.sha256`, lists
  an entry's file with another digest.
- The bluetoothd Dockerfile names an SPDX schema version and digest
  that the `spdx_schema_sha256` table of `liken/userland/fetch.sh`
  does not pin.
- A shell script names an image by a digest of its own, such as
  `ghcr.io/oras-project/oras:<tag>@sha256:<digest>`, and does not read
  the reference from `toolchains/images.toml` with `image_of`.
- `liken/licensing/sources.sh` has a digest in it. Each source that it
  mirrors is an entry of the `pin.toml` of the pin that the source
  belongs to, so the digest belongs in that entry.
- A grep for "statically linked" does not accept "static-pie linked".
- A `uses:` in a workflow or a composite action under `.github/`
  names the action of another repository by anything other than the
  commit and the release that `actionPins` in `ci/actions.go` lists.
  Or it names a `docker://` image by anything other than a digest.

The pins hook of the root `.pre-commit-config.yaml` runs the check on
each commit that changes a pin script, a Dockerfile, a `VERSION`, a
`pin.toml`, `toolchains/images.toml`, `versions.sh`, the library,
`trust/keys/`, or a file under `.github/`.

The check finds only the patterns above. It cannot tell when an
upstream other than kernel.org publishes a signature and a bump does
not verify it. When a line breaks a pattern for a reason that the
check cannot see, put a comment line directly above it with
`fetch-check: allow` and the reason.
