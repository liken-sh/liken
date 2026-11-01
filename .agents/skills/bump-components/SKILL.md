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
| `bluetooth-operator/bluetoothd/latest.sh` | the buildroot commit that the bluetoothd image builds with, which follows `liken/userland/VERSION`, step 5e |
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
   `userland/latest.sh --bump kmod` or
   `linux-firmware/latest.sh --bump wireless-regdb`.
3. The static programs in `userland/` follow buildroot's `master`.
   `userland/latest.sh --bump` moves buildroot to master's tip, writes
   the same commit and digest into `bluetooth-operator/bluetoothd/Dockerfile`,
   and deletes each line of `userland/versions.mk` whose version the
   new recipe meets. Then a program row that still reads `behind`
   takes a named bump, such as `userland/latest.sh --bump tzdata`. For
   a program of buildroot's own recipes, the bump sets the newer
   version in `versions.mk` and writes its `.hash` file under
   `userland/patches/`. For open-iscsi, kmod, and BlueZ, whose recipes
   are in `userland/iscsi/` and `userland/bluetooth/`, it writes the
   version into the recipe and the tarball's line into the recipe's
   `.hash` file. When the `buildroot` row of
   `toolchains/latest.sh` reads behind after the move, run
   `../toolchains/latest.sh --bump buildroot`. A buildroot bump can
   move every library and the compiler, so `make -C userland` must
   build before the bump is done. The first build after a buildroot
   bump compiles the cross-compiler again, and the compiler cache in
   `userland/cache/ccache/` shortens the rebuilds after it.
4. Bump `k3s` before `xtables`. The xtables pin follows the k3s-root
   release that the pinned k3s names as `VERSION_ROOT`, so
   `xtables/latest.sh` reads `k3s/VERSION` and reports xtables as
   behind only after k3s moves.
5. Finish with one `licensing/sources.sh --repin`. The mirror cache
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
* buildroot: the git log of `master` between the two commits, for
  the packages that `userland/os.config` and `userland/bluetoothd.config` select, the libraries they
  link, and the toolchain. `make -C userland` then prints each
  version in `userland/dist/legal-info/manifest.csv`; compare it with
  the old build's.
* nfs-utils: the git shortlog between tags at git.linux-nfs.org.
  Only the mount.nfs client path matters; liken runs no NFS server.
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
* storage: the lab's storage guest runs sid's daily cloud image, which
  Debian deletes after about four months.

Note each release's age. A firmware or microcode release that is
days old has no field history, and the smoke drills plus the first
metal boot are its first real test.

## 4. Verify

1. `make versions` again: every row must read `current` or `held`.
2. `git diff --stat`: the change is `VERSION` files, `pin.toml`
   files, `fetch.sh` digests, `licensing/sources.sh`, and
   `toolchains/images.toml`, nothing else.
3. `make all`: changelogs do not show configure or toolchain
   breakage, and a bump that does not build is not done. The
   programs that `userland` compiles from source against musl break
   the most often; a new configure check or a dropped default there
   is invisible until the build runs.
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
sets the date and revision 1, updates `[package.upstream]` from
the snapshot's `Packages` index, and writes the digest of each
suite's `InRelease` file into `indi/snapshot.sha256`. A move to a new
series also renames the suites in `indi/snapshot.sha256`. Then run `make workflows`. Compare the license file of
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
`cosign`: `liken/flux/latest.sh --bump` and `ci/latest.sh --bump
trivy` run it with docker to verify upstream signatures, and the
publish job of every component signs each image it pushes with it. So
a move of `cosign` needs no revision, and a flux bump needs docker.
`toolchains/latest.sh --bump cosign` verifies the new image with the
pinned cosign before it writes the line, and stops unless sigstore's
release build signed it. Run `make workflows`
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

`ci/latest.sh --bump` moves one repository of GitHub Actions, prek,
or one hook revision. `actionPins` in `ci/actions.go` names each
repository of actions once, by its release and the commit of the
release's tag, and every `uses:` reads
`<action>@<commit> # <release>`. `--bump <repository>`, such as
`--bump actions/checkout`, stops unless GitHub publishes the newest
release as a release that is not a pre-release. Then it writes the
commit of the tag, the commit under an annotated tag, into the table
and into each composite action under `.github/actions`. Run
`make workflows` after it. The `fluxcd/flux2` line follows
`liken/flux/VERSION`, and `liken/flux/latest.sh --bump` moves it. A
major version of an action can change its inputs, so read its release
notes before you take it. prek-action runs a prek that its
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
a `latest.sh` reports and bumps them.

The bluetoothd image builds its static daemons with buildroot from
`liken/userland/bluetoothd.config`, so `liken/userland/latest.sh`
reports BlueZ and dbus. `--bump bluez` writes the newest BlueZ release
into `liken/userland/bluetooth/bluez-static/`, and `--bump dbus` sets
the newest release of a stable series of dbus, which has an even
minor version, in `versions.mk`. Then run
`docker buildx bake bluetoothd`. The build stops when a patch in
`liken/userland/bluetooth/bluez-static/` no longer applies, and the
`README.md` beside it says what to do. Read the BlueZ release notes
for changes to the D-Bus API and to the mgmt socket, because the
operator reads both.

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
when the pin moves, and writes it into the repository: the
`pin.toml` beside `fetch.sh` for a pin whose files a release ships, a
`declare -A` table in `fetch.sh` for a pin that no release ships, a
buildroot `.hash` file under `liken/userland/patches/`, an `ARG` of a
Dockerfile, the `--checksum` of an `ADD`, or a constant in `ci/`. The
fetch and the build read only that copy. Each `--bump` prints one line
that says where the digest came from, such as "the digest came from
sha256sums.asc, signed by kernel.org's checksum autosigner".

**The manifest.** "Pins and downloads" in the root `AGENTS.md` says
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
  with `ubuntu_deb_digest`, against `trust/keys/ubuntu-archive.asc`.
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
  `fetch_pinned`; or a Dockerfile beside a `pin.toml` `ADD`s a URL by
  a checksum that the manifest does not name;
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

## 6. Report

Give one verdict per bumped component: what changed, whether there
is anything to worry about, and what to watch in the drills. Lead
with the concerns, if there are any. The tree stays uncommitted;
the build, the smoke drills, and `/release` are separate decisions.
