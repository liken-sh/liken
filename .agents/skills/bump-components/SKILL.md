---
name: bump-components
description: Bump every pin that is behind the newest upstream release, under the rule in root plan 83 - the top-level make versions report, each latest.sh --bump, the changelog research, and a final report of what changes and what to watch - including the toolchains, the whole Go module and Rust crate graphs with a vulnerability check, the pinned base images, their Debian snapshot and their revisions. Use when I say /bump-components or ask to bump, update, or refresh the component pins or the base images.
---

Bump every vendored pin that is behind, read the changelogs, and
report what is changing and what to look out for.

Additional context from me: $ARGUMENTS

If that context names specific components, or says to skip the
research or stop after a step, it wins over the defaults below.

**Core principle: the bumps are cheap working-tree edits. The value
is the research. Nothing here commits, builds, or releases.**

A pin is current when it matches the newest upstream release that is
not a pre-release. A pin built from source follows the newest
upstream release. A pin from a package repository, a registry, or a
channel server follows the newest channel that the repository offers
to install as a whole (Debian sid, Alpine `edge`, Ubuntu's
development series, k3s `latest`, the newest series a PPA publishes)
and the newest package in it. Root plan 83 gives the reasons.

Run `make versions` at the top of the repository. The commands of
steps 2 to 4 run from `liken/`, and their paths are relative to
`liken/`. Steps 5 and later work at the top of the repository.

## 1. Pull the report

Run `make versions` at the top of the repository. `versions.sh` finds
every `latest.sh` in the tree, runs them in parallel, and prints one
table. It reaches every upstream, so give it a minute or two. Collect
the rows whose state is `behind` or `wrong channel`. An indented row
is a sub-pin that hangs off the row above it; it needs its own named
bump.

Each `latest.sh` prints tab-separated rows of seven columns: name,
pinned, latest, channel, newest channel, hold, and note.

- `channel` is the suite, series, release, or channel that the pin
  reads, and `newest channel` is the newest one its repository
  offers. A pin built from source, or one that follows another pin,
  prints `-` in both.
- `hold` is `-`, or the reason the pin stays back. A hold needs a
  reason a reader can check, and it lives in the row, never in a
  comment alone.
- `latest` is `?` when the upstream did not answer.

The state is `held` when the row has a hold, `unknown` when an
upstream did not answer, `wrong channel` when the two channels
differ, `behind` when pinned and latest differ, and `current`
otherwise. A new `latest.sh` opens with a header that says what it
pins, where the bytes come from, which channel it reads, and why.

The report has these sources:

| Script | Pins |
|---|---|
| `liken/<domain>/latest.sh` | the OS domains, steps 2 to 4 |
| `liken/dev-cluster/firmware/capsule/latest.sh` | edk2 and the Ubuntu snapshot of the lab's capsule firmware builder, which builds on the `ubuntu` entry of `toolchains/images.toml`; it has no `--bump`, because each move changes the compiler that builds the firmware, so edit `build.sh` or the `Dockerfile` by hand and boot the lab with `FIRMWARE=capsule`, and do the same after a move of the `ubuntu` entry |
| `toolchains/latest.sh` | each image in `toolchains/images.toml`, Go and Rust, each Go module and crate that is behind |
| `toolchains/cargo-auditable/latest.sh` | cargo-auditable, which every Rust image builds with, step 5c |
| `vulkan/latest.sh` | the Debian snapshot of the bases and `astrometry`, step 5 |
| `indi/latest.sh` | the snapshots of Ubuntu and the two PPAs, step 5a |
| `trust/latest.sh` | the CA bundle, step 5b |
| `bluetooth-operator/bluetoothd/latest.sh` | the BlueZ and dbus releases that the bluetoothd image builds, step 5e |
| `library-operator/latest.sh` | OpenVINO, Intel's compute-runtime, gmmlib, and the graphics compiler in the appearances image, step 5e |
| `ci/latest.sh` | the GitHub Actions, prek, and the hook revisions |
| `liken.sh/latest.sh` | Flatcar and the Terraform providers |

## 2. Bump

Run `<domain>/latest.sh --bump` for each behind domain, sequentially,
in one background task. These rules matter:

1. Put `systemd-boot` and `grub` last. Their source mirrors fetch
   from launchpad.net, which is slow and sometimes refuses
   connections, and each bump's tail runs `licensing/sources.sh
   --repin`, which retries launchpad every time.
2. A sub-pin needs a named bump, for example
   `nfs-utils/latest.sh --bump libtirpc` or
   `linux-firmware/latest.sh --bump wireless-regdb`. The alpine
   builder of the static builds is the `alpine` entry in
   `toolchains/images.toml`, and `../toolchains/latest.sh --bump
   alpine` moves it for `open-iscsi`, `nfs-utils`, `tzdata`,
   `wpa-supplicant`, `e2fsprogs`, and `bluetoothd` at once.
   `licensing/latest.sh` reads `musl`, `util-linux`, `libnl`, and
   `openssl` from the same Alpine branch, so those rows follow the
   builder.
3. Bump `k3s` before `xtables`. The xtables pin follows the k3s-root
   release that the pinned k3s names as `VERSION_ROOT`, so
   `xtables/latest.sh` reads `k3s/VERSION` and reports xtables as
   behind only after k3s moves.
4. Finish with one `licensing/sources.sh --repin`. The mirror cache
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
* e2fsprogs: the release notes at
  e2fsprogs.sourceforge.net. Only `mke2fs` ships.
* trust, hugo: no research unless the version jump looks unusual.
* alpine, storage: a new Alpine release or a move to `edge` can
  drop a static library or change a default that a build from
  source depends on, so build every domain that uses the builder.
  The lab's storage guest runs sid's daily cloud image, which
  Debian deletes after about four months.

Note each release's age. A firmware or microcode release that is
days old has no field history, and the smoke drills plus the first
metal boot are its first real test.

## 4. Verify

1. `make versions` again: every row must read `current` or `held`.
2. `git diff --stat`: the change is `VERSION` files, `fetch.sh`
   digests, `licensing/sources.sh`, and `toolchains/images.toml`,
   nothing else.
3. `make all`: changelogs do not show configure or toolchain
   breakage, and a bump that does not build is not done. The
   domains that compile from source on the musl builder break the
   most often; a new configure check or a dropped default there is
   invisible until the build runs.
4. `make smoke-uefi`: one boot to Ready proves the pieces still
   assemble into a machine.

## 5. The base images

The base images, `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`, are
pinned components at the top of the repository. `vulkan/latest.sh`
reports their snapshot date and each `[package.upstream]` entry
against the newest snapshot of sid. Each one's `package.toml` states a `version`, the date
of the snapshot.debian.org archive it installs from as `YYYYMMDD`, and
a `revision`. Each one publishes under the tag `<version>-<revision>`.

**A new snapshot.** All five move together, because a library that two
bases hold must be the same file in both. `vulkan/latest.sh --bump`
writes the date, revision 1, and the `[package.upstream]` entries
into every `package.toml` that installs from the snapshot, the sha256
of the snapshot's `InRelease` file into `vulkan/snapshot.sha256`, and
the digest of the newest `debian:sid-slim` into the `debian` entry of
`toolchains/images.toml`. `vulkan/README.md` gives the same steps by
hand. Then run `make workflows`, run `make images`, and run each
image's smoke check. The report shows the snapshot as `behind` on
every day after a bump, because sid takes uploads every day. Compare the package
versions with the old snapshot and report the ones that moved.
The suite is sid, Debian unstable, so a new date can bring a new
major release of Mesa, ffmpeg, mpv, or Weston. Read the upstream
release notes of each major release that moved, as in step 3, and
roll the dev builds to the `liken-1` testbed before any other
cluster takes them. Root plan 82 gives the reasons for sid.
`astrometry`, the plate solver, is not a base, but it installs from
the same snapshot through `vulkan/snapshot.sh`, and
`vulkan/snapshot.sha256` holds the checksums of one date. So it moves
with the five: its `version`, `revision`, digest, and
`[package.upstream]` change in the same commit.

Each base's `package.toml` also lists, under `[package.upstream]`, the
upstream releases that matter in it, such as `mesa` and `ffmpeg`. A
pinned version is a date, so these entries are how a reader of the
registry learns what a tag holds: each one becomes the image label
`sh.liken.upstream.<name>`. A new snapshot updates every entry to the
version that the snapshot installs. Read the versions from the
snapshot's `Packages` indexes, or with `dpkg-query -W` in the builder.

The `weston` entry of `weston/package.toml` is the exception. The
weston image builds Weston from the upstream release, so
`weston/latest.sh` reports and bumps that entry, and the snapshot
holds no package that the image installs under that name.
`built_from_source` in `vulkan/latest.sh` names that entry, so the
snapshot's report and its `--bump` skip it.

**A new revision.** CI hashes each base's recipe: its `package.toml`,
its `Dockerfile`, every file in its build context, the digest of each
image outside the repository that it starts from, and the tag of each
component in its `[depends]`. A change to any of them at the same
version needs the next revision. The rule cascades: a base's tag is in
the recipe of every pinned component above it, so a new revision of
`ffmpeg` changes the recipe of `mpv`, and `mpv` needs a new revision in
the same commit. Walk the graph up from each changed base and raise the
revision of every pinned component that depends on it, directly or
through another base:

* `vulkan` raises `vaapi`, `ffmpeg`, `mpv`, `weston`, and `astrometry`.
* `vaapi` raises `ffmpeg` and `mpv`.
* `ffmpeg` raises `mpv`.
* `mpv`, `weston`, and `astrometry` raise nothing.

A new snapshot sets every revision to 1, so it needs no cascade. When
CI fails with "the recipe of N pinned components changed with no new
revision", the message names each component to raise.

The files that a base's `.dockerignore` leaves out, such as its
`README.md` and its `smoke/` check, are not in the recipe, and a
change to them needs no revision.

`vulkan/closure.sh` is the closure library, and every closure on a
Debian or Ubuntu builder sources it: the bases, `astrometry`, and the
closures of `audio-operator` and `git-csi-driver`, which take the
`vulkan` directory as the build context `builder`. So a change to it
needs a new revision of `vulkan` and the cascade above. `indi` keeps a
copy, `indi/closure.sh`, and `ci pins` fails when the copy differs, so
copy the change into it and raise the revision of `indi` too. The
library writes each image's notices, its list of packages, and its
dpkg records under `/var/lib/dpkg/status.d/`, which syft and Trivy
read. "Closures" in the root `AGENTS.md` gives the form.

## 5b. The trust store

`trust` at the top of the repository pins the CA bundle that every part
of `liken` trusts: the OS build reads its `VERSION` through
`trust/fetch.sh`, and every image that verifies TLS copies the bundle
from the `trust` image. `make versions` reports it as `trust`. Its `--bump` writes the new date into `VERSION`, the image's
version and `[package.upstream]` into `package.toml`, and the snapshot
and its published checksum into the `Dockerfile`. Then run
`make workflows` at the top of the repository and raise the revision of
`indi`, the one pinned component that copies the bundle. The tracked
components that copy it release because their `[depends]` names
`trust`. A snapshot removes trust as well as adding it, so read what
left the bundle before taking the bump.

## 5a. The INDI images

`indi` is a pinned component too, with the 17 images that
observatory-operator runs. It is not one of the five bases above: it
installs from Ubuntu 26.04, the INDI PPA, and the PHD2 PPA, through
dated snapshots of all three, and it moves on its own schedule. Its
`version` is the date of those snapshots, and `[package.upstream]`
states the `indi-bin`, `indi-3rdparty-drivers`, `gsc`, and `phd2`
package versions that the date installs. Its smoke checks fail when the image holds other versions.

`indi/latest.sh` reports the snapshot date, the four package
versions, and the newest Ubuntu series that both PPAs publish. When a
PPA publishes a newer series, the row reads `wrong channel`, and the
move to that series is a hand edit of `indi/snapshot.sh` and the
`ubuntu` entry of `toolchains/images.toml`. `indi/latest.sh --bump`
sets the date and revision 1 and updates `[package.upstream]` from
the snapshot's `Packages` index. Then run `make workflows`. Compare the license file of
each SDK directory in indi-3rdparty, at the commit that the PPA built
from, with `indi/sdk-licenses/`, and copy each new or changed one; the
section "The notices" in `indi/README.md` holds the steps. Build every target in
`indi/package.toml` and run each smoke check. The build fails when a
new release adds a third-party driver that no list in `indi/images/`
names; add it to an image's list or to `indi/images/unpublished`, and
say which in the report. Then run `make -C observatory-operator
drivers firmware-rules`. `drivers` writes the new tag into
`drivers/generated.go`, and `firmware-rules` copies `85-qhyccd.rules`
out of the new `indi-qhy` image and writes `deploy/firmware/qhy.yaml`
from it, one `Firmware` for each bare QHY chip, on the new tag. Report
each object it adds or removes: a new object is a new QHY camera that
`firmware-operator` loads, and the test that counts the objects needs
the new count. Then run `make -C observatory-operator
captures`, which records the captures of the standard INDI properties
and the simulator transcripts again on the new libindi; the operator's
tests fail until it runs. Then run `make -C observatory-operator
schema crosscheck`; `observatory-operator/AGENTS.md` gives the steps.
Report each new line of `observatory-operator/schema/disagreements.txt`.
CI builds the 17 images only when this tag is new.

## 5c. The toolchains

`toolchains/images.toml` names each outside image once. A Dockerfile
names an image from its `[build]` table bare, as in `FROM golang`,
and `ci/` writes the reference and its digest into the bake target.
Manifests, guides, skills, and tests name a `[run]` image at its tag.
`toolchains/latest.sh --bump <key>` writes the newest tag of an image
and, for a `[build]` image, its digest. `--bump go` and `--bump
rustc` move the toolchain image and every `go.mod` or
`rust-toolchain.toml`. `--bump go:<module>` and `--bump
crate:<name>` move every copy of one dependency. The k8s.io modules
follow the k3s release in `liken/k3s/VERSION`, so they move after
k3s does. `--bump go` and `--bump go:<module>` then run `go mod tidy`
in each module they wrote, and in each module that replaces one of
those with a local directory, such as every operator that replaces
`github.com/liken-sh/liken/kubernetes`. Without tidy, `go get` keeps
the `go.sum` lines of the old version, and the `go mod tidy -diff`
check of each component fails.

A new reference in `[build]` changes the recipe of each pinned
component whose Dockerfile names that key, so each one needs a new
revision, and the cascade in step 5 applies. No Dockerfile names
`cosign`: `liken/flux/latest.sh --bump` runs it with docker to verify
the signature of Flux's checksums file, so a move of `cosign` needs
no revision, and a flux bump needs docker. Run `make workflows`
after every bump of `images.toml`. `cd ci && go run . pins -root ..`
fails when two copies of a Go version, a Rust version, a module, a
crate, a hook revision, or an image tag disagree; CI runs it on every
push.

`toolchains/cargo-auditable/latest.sh --bump` moves cargo-auditable,
the cargo subcommand that every Rust image builds with, so each binary
records its crates in a `.dep-v0` section that syft and Trivy read.
"Rust binaries" in the root `AGENTS.md` gives the form. The bump writes
the version and the digest from the release's `.sha256` file into
`toolchains/cargo-auditable/fetch.sh`. The project signs neither file.
Then build each image whose Dockerfile runs `cargo auditable build`,
and check that syft still lists the crates of one of them:
`docker run --rm -v /var/run/docker.sock:/var/run/docker.sock
anchore/syft:latest <image> -o json -q | jq '[.artifacts[] |
select(.type == "rust-crate")] | length'` prints more than 0. The Rust
images are tracked components, so no revision changes.

`ci/latest.sh --bump` moves one GitHub Action, prek, or one hook
revision. A major version of an action can change its inputs, so read
its release notes before you take it. prek-action runs a prek that its
own checksum table does not name without a check, so `--bump prek`
also writes `prekSHA256` in `ci/workflow.go`, the digest of the
archive that the action downloads. The bump takes the digest from the
release's `.sha256` file and verifies the archive's GitHub attestation
with `gh`, so it needs `gh`.

## 5d. The Go modules and the Rust crates

`make versions` reports only the direct and tool requirements of each
`go.mod` and the exact `=` pins of each `Cargo.toml`. An indirect
module or a crate that only a lock file names never shows in the
report, and a known vulnerability is most often in one of those: the
operators took `golang.org/x/net` through client-go as an indirect
requirement. So every bump also updates the whole dependency graph of
each module and each workspace.

1. Run `toolchains/latest.sh --bump go:<module>` and `--bump
   crate:<name>` for each row that is behind, as in step 5c.
2. In the directory of each `go.mod` that `git ls-files` lists, except
   under `testdata/`, record the `k8s.io/` and `sigs.k8s.io/` lines,
   then run `GOWORK=off go get -u ./...`. Add the `tool` pattern when
   the `go.mod` has a `tool` block, so the docs modules move Hugo's
   graph too. Then run `go get` again with each recorded
   `k8s.io/...@<version>` and `sigs.k8s.io/...@<version>`, because
   those modules follow the k3s release, and run `go mod tidy`.
3. In the directory of each `Cargo.lock`, run `cargo update`. It
   moves every crate that the `=` pins and the semver ranges of the
   graph allow, and it moves nothing past them.
4. A crate whose newest release is a new major version can be blocked
   by another crate in the graph. `toolchains/latest.sh --bump crate:`
   then fails with `failed to select a version for the requirement`,
   and the message names the crate that holds the old range. Decide
   for each one:
   - Keep the old pin when the code passes the crate's types to the
     crate that holds it back, or when the old version is already in
     the build. `tiny-skia` stays on the version that winit's
     `sctk-adwaita` requires for its window decorations, because
     `media-operator/display` rasterizes with that same build, and a
     second copy is a second build of it.
   - Take the new pin when the two copies never meet, and let cargo
     build both. `base64` in `media-browser` is an example, because
     `metrics-exporter-prometheus` uses its own copy internally.
   Record each kept pin as a case in `hold_of` in
   `toolchains/latest.sh`. The case names the crate that holds the
   pin back, `lock_holders` reads that crate's entry in the lock
   file, and the row reads `held` with the holder's version in the
   hold column. When the holder no longer depends on the pinned
   version, the hold lapses and the row reads `behind`. A Go module
   that stays back gets a case the same way, with `module_holders`,
   which reads the module graph. Report each kept pin with the crate
   or module that holds it back.
5. A new major version of a direct dependency can change its API or
   its behavior. Read its migration notes, port each call site, and
   check each behavior that a comment at the call site depends on.
   `ureq` 3 replaced the per-socket read timeout of 2 with whole-stage
   deadlines, and it kept a per-read timeout only as
   `timeout_per_read`, which it reports as `ureq::Error::Timeout`
   inside an `io::Error` of kind `Other`.
6. Check the result for known vulnerabilities. Run `govulncheck ./...`
   in each module and `osv-scanner scan source --lockfile=<lock>` on
   each `Cargo.lock`. Neither tool is pinned in the repository, so
   install each one into the session's scratch directory with
   `GOBIN=<dir> go install <module>@latest`. Report each finding that
   remains, with its fixed version or the reason no bump reaches it.
   Root plan 87 holds the design of a check in CI that fails on a
   finding a bump can reach.
7. Run `cd ci && go run . pins -root ..`, then `make test` in each
   changed component and the `run` of each `[[jobs]]` entry that names
   a `dir` in its `package.toml`. A component's `make test` does not
   always build those directories: `library-operator`'s skips
   `appearances`, which CI builds as `rust-appearances`.

## 5e. The sources inside operator images

Two operator images fetch upstream releases in their Dockerfiles, and
a `latest.sh` beside each Dockerfile reports and bumps them.

`bluetooth-operator/bluetoothd/latest.sh --bump` moves BlueZ, and
`--bump dbus` moves dbus to the newest release of a stable series,
which has an even minor version. Each bump writes the version and the
checksum that upstream publishes into `bluetoothd/Dockerfile`. Then
run `docker buildx bake bluetoothd`. The build stops when a patch in
`bluetoothd/patches/bluez/` no longer applies, and
`bluetoothd/patches/README.md` says what to do. Read the BlueZ
release notes for changes to the D-Bus API and to the mgmt socket,
because the operator reads both.

`library-operator/latest.sh --bump` moves OpenVINO within the
archive channel that the pin names, such as `ubuntu24`. A row that
reads `wrong channel` means Intel publishes an archive for a newer
Ubuntu release; the move to it is a hand edit of the URL in
`Dockerfile.appearances` and `appearances/README.md`, followed by the
`--bump`. `--bump compute-runtime` moves the OpenCL driver, gmmlib
from the same release, and the graphics compiler release that the
runtime's release text names. Both bumps write URLs and checksums
into `Dockerfile.appearances`. `appearances/inference-closure.sh`
names the versioned library files of each runtime, so move those
names by hand, then run `docker buildx bake
library-operator-appearances`.

## 5f. A new or changed latest.sh or fetch.sh

"Pins and downloads" in the root `AGENTS.md` gives the rules and the
reasons. This step gives the library that every pin script uses, and
the check in `ci/` that holds the scripts to it.

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
- `fetch_text`, `url_exists`, `newest_tag`, `github_api`, and
  `registry_digest` read an upstream, with the retries and time limits
  that every read takes. `github_api` sends `GITHUB_TOKEN` or the gh
  CLI's token when one is at hand.
- `fetch_verified [--resume] <url> <digest> <path>` puts a pinned file
  in the cache: it keeps a cached file that matches, downloads anything
  else into a `.partial` file, deletes a `.partial` that does not match
  and stops, and moves a match into place. `--resume` is for a large
  file. The digest is the one the script pins, often
  `"${k3s_sha256[$version]:-}"` from a `declare -A` table, and an empty
  digest stops the fetch.
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
  `trust/keys/ubuntu-archive.asc`.
- `container_runtime` and `image_of <key>` give a build its docker or
  podman and its image from `toolchains/images.toml`.
- `row` prints one report row of seven columns, with `?` for an empty
  latest.
- `bump_needed`, `bumped`, and `next_step` print the lines of a bump.
  `set_line`, `set_var`, `set_toml`, `set_entry`, and `add_entry` write
  a bump into the file that holds the pin, and each one stops when its
  pattern matches no line or more than one, so a bump never reports a
  move that it did not write. `repin_sources` runs
  `liken/licensing/sources.sh --repin` after a bump of a version that
  the source mirror names.

When a new script needs a step that the library lacks, add the step
to `pin.sh` with a comment, and do not write it in the script.

**The digest.** `latest.sh --bump` reads the upstream's checksum once,
when the pin moves, and writes it into the repository: a `declare -A`
table or a variable in `fetch.sh`, a file such as
`liken/tzdata/DIGESTS`, an `ARG` of a Dockerfile, the `--checksum`
of an `ADD`, or a constant in `ci/`. The fetch and the build read only
that copy. Each `--bump` prints one line that says where the digest
came from, such as "the digest came from sha256sums.asc, signed by
kernel.org's checksum autosigner".

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
- dbus signs each release tarball. `bluetooth-operator/bluetoothd/latest.sh`
  verifies the tarball against `bluetoothd/dbus.asc`, accepts one
  pinned fingerprint, and pins the digest of the verified bytes.
- IANA signs each tzdata tarball, and weston's release managers sign
  each weston tarball. `liken/tzdata/latest.sh` and `weston/latest.sh`
  verify the tarball against the key file beside the script before
  they pin its digest, and `liken/tzdata/fetch.sh` verifies the
  signature again on every build.
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
  Debian's archive keys in `liken/dev-cluster/firmware/debian-archive.asc`.
- Ubuntu signs its archive the same way. `liken/grub/latest.sh` and
  `liken/systemd-boot/latest.sh` take their digests through the chain
  with `ubuntu_deb_digest`, against `trust/keys/ubuntu-archive.asc`.

A key that one script reads stays beside that script. A key that a
second component reads moves to `trust/keys/`, with a section in its
`README.md` that says where the key comes from. When the upstream
signs nothing, the script's comment and the printed line say so, so
the commit says what stands behind the digest.

**A static binary.** gcc on Alpine builds a static PIE for `-static`,
so a check with file(1) accepts both words:
`grep -qE "statically linked|static-pie linked"`.

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
- a grep for "statically linked" does not accept "static-pie linked".

The pins hook of the root `.pre-commit-config.yaml` runs the check on
each commit that changes a pin script, a Dockerfile, a `VERSION`,
`toolchains/images.toml`, `versions.sh`, the library, or
`trust/keys/`. The check sees only these forms. It does not see a
signature that an upstream other than kernel.org publishes and a bump
skips. A line that breaks a pattern for a reason the check cannot see
takes a comment line directly above it with `fetch-check: allow` and
the reason.

## 6. Report

Give one verdict per bumped component: what changed, whether there
is anything to worry about, and what to watch in the drills. Lead
with the concerns, if there are any. The tree stays uncommitted;
the build, the smoke drills, and `/release` are separate decisions.
