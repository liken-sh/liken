---
name: pins
description: Adds or changes a pin in the liken repository - a VERSION file, a fetch.sh, a latest.sh, a pin.toml manifest, a digest, a purl, a CPE, an ADD --checksum in a Dockerfile, an image of toolchains/images.toml, or the commit of a GitHub Action. Covers the rule that every pin follows the newest upstream release, the rules of a download, the library toolchains/pin.sh, the manifest, the signature that a bump verifies, and the ci pins check. Use before writing or changing any of those files, when a component starts to download a new file, when a build applies an upstream patch to a pinned release, when a scanner or the GitHub security tab must know the exact version that liken ships, and when ci pins or the pins hook of prek fails. To move existing pins to newer releases, use the bump-components skill.
---

# Pins

A pin records exactly which bytes a build downloads: a version, and
the digest of each file. This skill has the rules that every pin
follows, and the library that every pin script uses. The procedure
that moves pins to newer releases is in the `bump-components` skill.
The form of a Dockerfile is in the `images` skill. The reasons for
both are in root plan 83.

## Which section to read

* **Every pin, before you add or change one:** "Every pin stays
  current" and "The rules of a download".
* **Add a pin:** "Where a pin goes" first.
* **Write a `latest.sh` or a `fetch.sh`:** "Write a `latest.sh` or a
  `fetch.sh`", and `references/pin-library.md`.
* **Fetch a file into an image:** "The manifest", up to the paragraph
  "A CPE for a file of the OS". The form of the Dockerfile is in the
  `images` skill.
* **A file that an OS release ships, and its CPE:** "The manifest",
  then "A CPE for a file of the OS".
* **An upstream that signs its files:** "The signature", and
  `references/signatures.md`.
* **A program that buildroot builds:** "A static program from
  buildroot".
* **A failure of `ci pins`:** "The check".

## Every pin stays current

A pin is current when it matches the newest upstream release that is
not a pre-release, with no soak time. The root `AGENTS.md` gives the
reason.

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
the repository, runs all of them and prints one table. The format of
a row is in the `bump-components` skill.

A pin that several components share has one owner. For example,
`toolchains/images.toml` lists each image from outside the repository
once. `ci pins` fails when two copies of a Go version, a Rust
version, a Go module, a crate, or an image tag disagree.

## The rules of a download

A build uses only the bytes that a commit named. So a person who
reviews the commit has reviewed what every later build downloads.

- **The digest is in the repository.** `latest.sh --bump` reads the
  upstream's checksum once, when the pin moves, and writes it into
  the repository. No `fetch.sh`, Dockerfile, or other build step
  reads a digest from the network, because a checksum file that a
  build reads accepts whatever the upstream serves that day.
- **A download goes to a `.partial` file.** The file gets its real
  name in the cache only after its digest matches. A cached file that
  does not match is downloaded again. So a download that broke off or
  changed never gets the name of a good one.
- **A bump verifies the upstream's signature.** When the upstream
  signs the checksum file or the release file that a bump copies a
  digest from, the bump verifies the signature first. Without that
  check, a mirror that changes the file changes the pin.
- **Each outside image has one entry.** Every `FROM` and
  `COPY --from` names an image of `toolchains/images.toml`, or an
  image of the repository by its bare name. A shell script that runs
  an outside image reads its reference with `image_of`, and names no
  digest of its own.
- **Every pin has a `latest.sh`.** Every `VERSION`, every `fetch.sh`,
  and every Dockerfile that downloads has a `latest.sh` in its
  directory or above it. The `latest.sh` is executable. So no pin
  can fall behind without the report showing it.
- **A GitHub Action is pinned to a commit.** Every `uses:` names the
  commit of a release, as in `actions/checkout@<commit> # <release>`,
  because the owner of an action can move a tag to other code. The
  publish jobs run their actions with a token that can write to the
  registry and a token that can sign images. `actionPins` in
  `ci/actions.go` lists each repository of actions once.
  `ci/latest.sh --bump <repository>` writes the commit of the tag of
  the newest release.
- **A check for a static binary accepts a static PIE.** gcc on Alpine
  builds a static PIE for `-static`, so a check with file(1) accepts
  both phrases: `grep -qE "statically linked|static-pie linked"`.

A file under `toolchains/`, such as `pin.sh` or `manifest/`, can be
in the build context of a pinned component. So an edit of that file,
even of a comment, can require a new revision. "The bake file and the
base images" in the `images` skill has the rule, and the check to run
before a push.

## Where a pin goes

Before you write a pin, decide which form it has. The form
depends on what downloads the file.

- **A Dockerfile downloads the file with `ADD --checksum`.** Put the
  `pin.toml` and the `latest.sh` in the directory of that Dockerfile.
  The pin has no `fetch.sh` and no `VERSION`, because the Dockerfile
  downloads the file. `weston/` is an example.
- **The OS build downloads the file.** Make a directory under
  `liken/` for the pin, with a `VERSION`, a `fetch.sh`, a `latest.sh`,
  and a `pin.toml`. `liken/hwdata/` is an example. Other files of
  `liken/` also name the pin, such as the `hwdata` target and the
  `HWDATA_VERSION` variable in `liken/Makefile`. To find each place
  that your pin must be added to, search for the name of a similar
  pin outside its own directory:
  `git grep -n hwdata -- liken ':!liken/hwdata'`.

## Write a `latest.sh` or a `fetch.sh`

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
implementation of each step, and a script runs no curl or wget of
its own. Before you write a script, read
[`references/pin-library.md`](references/pin-library.md), which
lists each function and what it does. The comment above each
function in `pin.sh` gives its arguments and the reason for its
behavior.

When a new script needs a step that the library does not have, add
the step to `pin.sh` with a comment. Do not write it in the script.

**The header.** A new `latest.sh` starts with a comment that says
what the script pins, where the bytes come from, which channel it
reads, and why.

**The digest.** `latest.sh --bump` writes the digest to one of these
places, and the fetch and the build read only that copy:

- the `pin.toml` beside `fetch.sh`, for a pin whose files a release
  ships
- a `declare -A` table in `fetch.sh`, for a pin that no release
  ships
- a buildroot `.hash` file under `liken/userland/patches/`
- an `ARG` of a Dockerfile, or the `--checksum` of an `ADD`
- a constant in `ci/`

A `fetch.sh` never names a checksum file such as `SHA256SUMS` or
`<file>.sha256`. Each `--bump` prints one line that says where the
digest came from, such as "the digest came from sha256sums.asc,
signed by kernel.org's checksum autosigner".

When the upstream publishes no checksum, such as for an asset of a
GitHub release, the bump downloads the file and computes the digest
with `digest_of_url <url>`. `liken/hwdata/latest.sh` is an example.
The script's header comment and its printed line must then say that
the upstream signs nothing, and that the digest comes from the bump's
own download.

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
5. Run `prek run --files <script> <other new files>` from the
   component's directory. The component's shellcheck hook runs
   `shellcheck -x` from a prebuilt binary, so this works on a
   workstation that has no `shellcheck` on its `PATH`. Then run
   `cd ci && go run . pins -root ..` at the top of the repository.

## The manifest

A manifest is a file named `pin.toml`. It is the only copy of the URL
and the digest of each file that it lists. `latest.sh --bump` writes
an entry with `set_pin`. `fetch.sh` and the source mirror download
with `fetch_pinned`. Every other reader gets a field with
`pin_value`. A `fetch.sh` that has a digest of its own, or does not
call `fetch_pinned`, fails `ci pins`.

**Which pins have one.** A pin whose files a release ships has a
`pin.toml` beside its `fetch.sh`, because the SBOM of a release must
list each of those files, and only the pin records where each file
came from. A pin whose files an image ships has a `pin.toml` beside
the Dockerfile that fetches them, because syft finds no package in a
file that a Dockerfile fetches by its checksum.

Some programs need no manifest, because another record already lists
them. buildroot's CycloneDX SBOM lists the programs that buildroot
builds. The `status.d` records list the packages of a closure image.
The build info lists the modules of a Go binary, and cargo-auditable
records the crates of a Rust binary. A Debian package that a closure
unpacks needs no entry, because the closure writes its dpkg record.
`unmanifested` in `ci/manifests.go` lists each other `fetch.sh` that
needs no manifest, and `unmanifestedDownloads` lists each Dockerfile
whose downloads only its build reads, each with the reason.

**The fields.** A manifest has one table for each file that the
fetch downloads, whatever the upstream of the file. For example,
`liken/linux-firmware/pin.toml` has a table for `linux-firmware` and
a table for `wireless-regdb`, which come from different projects. So
a Dockerfile that fetches a second file adds a table to the
`pin.toml` beside it:

```toml
[k3s]
version = "v1.37.1+k3s1"
url = "https://github.com/k3s-io/k3s/releases/download/v1.37.1%2Bk3s1/k3s"
sha256 = "92b94582eb7b34a8cf532e6006842b9ed215a6783cf56a3dd6271fd35243b2c0"
purl = "pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1"
license = "Apache-2.0"
```

- **`version`** is the version that the `VERSION` beside the
  manifest pins. An entry that follows another pin, such as
  `wireless-regdb` in `liken/linux-firmware`, is listed in
  `nestedPins` in `ci/manifests.go`, and the check skips this
  comparison for it.
- **`purl`** follows the purl specification for the kind of file, at
  the entry's version:
  - `pkg:deb/ubuntu/...?arch=amd64` for a package of Ubuntu's
    archive, and `?arch=source` for a file of its source package.
  - `pkg:github/<owner>/<name>@<tag>` for a GitHub release or tag.
  - `pkg:generic/<name>@<version>`, with the `download_url` and the
    `checksum` of the entry, for any other file.
- **`license`** is an SPDX expression. Write it from the upstream's
  own files: its `COPYING` or `LICENSE`, its SPDX headers, or the
  package's `copyright` file. Use a `LicenseRef-` for terms that the
  SPDX list does not include, and say in the comment above the table
  where the terms are. A bump does not change the license, so check
  it yourself when a release changes the upstream's license.
- **`scope`** is optional. Set it to `excluded` for a file that only
  the build reads, or that only the source mirror reads.
- **`backports`** lists the upstream commits that the build applies
  to the release. Each item is the URL of a GitHub commit with its
  full 40-digit id. The SBOM records the commits as the component's
  pedigree, so the SBOM does not present the component as the stock
  release.
- **`type`** and **`cpe`**: see below.

**The corresponding source.** Some licenses require that the source
is available, and `liken/licensing/sources.sh` mirrors that source
onto the release channel. Each file of the source is an entry with
the scope `excluded` in the `pin.toml` of the pin that it belongs
to, so the bump that moves a binary moves its source too.
`sources.sh` reads each one with `fetch_pinned` and the manifest as
the last argument, and has no digest of its own. Read `sources.sh`
for the pins that it mirrors.

**The `type` of an entry.** `type` is the CycloneDX type of the
component. An entry with no `type` gets `file`, from `defaultType` in
`toolchains/manifest/manifest.go`. Choose the type for what the file
is, and for where it ships:

- **A file that an OS release ships.** Use the CycloneDX type that
  describes the file, such as `application`, `firmware`, or `data`.
  `data` marks a data file that no program is built from, such as
  `pci.ids` in `liken/hwdata/pin.toml`.
- **A file that an image ships.** syft drops a component of the type
  `file` or `data` when it reads the record inside an image, as the
  comment at `defaultType` says. So use a type that syft keeps, such
  as `library`, `application`, or `machine-learning-model`, even for
  a data file such as a font. Pick the one that is closest to how the
  image uses the file. Run
  `git grep -h '^type' -- '*pin.toml' | sort | uniq -c` to see the
  types in use.

**A file that an image ships.** When NVD lists the upstream, its entry
has the `cpe` that NVD lists it under. The bump writes the entry with
`set_pin`, with the CPE as the last argument.

The Dockerfile has a second copy of the URL and the digest in its
`ADD --checksum`, because BuildKit reads the checksum from the text
of the Dockerfile. The pin's `latest.sh --bump` writes both copies of
each file that it moves.
`ci pins` fails on an `ADD --checksum` that no manifest beside the
Dockerfile lists at that digest, after the `ARG` defaults are
substituted.

The image build runs `image-sbom` from `toolchains/manifest`, through
the build context `manifest`. `image-sbom` writes a record, and the
image includes it at `/usr/share/doc/liken/<image>.cdx.json`. syft
copies the components of each such file into the image's SBOM. The
SBOM of a release uses the same reader, `toolchains/manifest`, so a
field means the same in both. A Dockerfile that fetches a file of a
manifest and runs no `image-sbom` fails `ci pins`.

**A CPE for a file of the OS.** No package database records the
kernel, k3s, the bootloaders, or the microcode. So an entry of the OS
whose upstream NVD lists has a `cpe` and a `type` that a scanner
keeps, and the scan of each release's SBOM matches the file by the
CPE alone. "Vulnerability scans" in the `sbom-and-scans` skill
describes the scan. Give a new pin of the OS the same two fields when
NVD lists its upstream.

NVD lists an upstream at the upstream's own version. So the bump
writes the CPE with `cpe_version`, which prints `1.37.2` for
`v1.37.2+k3s1` and `2.14` for `2.14-2ubuntu3`. `ci pins` accepts a
CPE at the entry's version, or at that upstream part of it.

To find every manifest with a CPE, or to list the purl of every pin
that a release ships:

```sh
git grep -l '^cpe' -- '*pin.toml'
tomlq -r '.[] | .purl' liken/*/pin.toml trust/pin.toml
```

## The signature

When the upstream signs its checksum file or its release file,
`--bump` verifies the signature before it writes a digest. It stops
when the signature is missing, does not verify, or is from another
signer. Before you write the bump of an upstream that signs, read
[`references/signatures.md`](references/signatures.md), which gives
each way that an upstream signs and how a `latest.sh` verifies it.

A key that one script reads stays beside that script. When a second
component reads the key, move it to `trust/keys/`, and add a section
to `trust/keys/README.md` that says where the key comes from. When
the upstream signs nothing, the script's comment and the line that it
prints both say so. Then the commit says what the digest is based on.

## A static program from buildroot

buildroot downloads inside its builder, so `ci pins` cannot read its
downloads. The pins are these files:

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
and the SPDX schema itself with `ADD --checksum`, and `ci pins` fails
when a commit or a digest there differs from the pins of
`liken/userland`.

## The check

`cd ci && go run . pins -root ..` runs `ci/fetches.go` and
`ci/manifests.go`, which match the scripts, the manifests, the
Dockerfiles, and the workflows against the rules above. CI runs it
on every push. The pins hook of the root `.pre-commit-config.yaml`
runs it on each commit that changes a file that the hook's `files`
pattern names, such as a pin script, a Dockerfile, a `pin.toml`, or
a file under `.github/`. Each finding names the file, the line when
there is one, and the rule that the line breaks.

The check finds only patterns. It cannot tell when an upstream other
than kernel.org publishes a signature and a bump does not verify it.
When a line breaks a pattern for a reason that the check cannot see,
put a comment line directly above it with `fetch-check: allow` and
the reason.
