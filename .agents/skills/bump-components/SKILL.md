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
| `liken/releases/sbom/schema/latest.sh` | the CycloneDX schema that `liken/releases/sbom` checks the OS release's SBOM against; the bump writes the schema files of the newest specification release, and a new minor version changes the `specVersion` that the SBOM states, so run `make -C liken test-go` after it |
| `liken/verify/latest.sh` | Sigstore's trusted root, which `liken verify` and `liken sbom` check every signature against; the bump takes the root through Sigstore's TUF repository with the pinned cosign, so it needs docker, and its diff shows each key that came or went |
| `toolchains/latest.sh` | each image in `toolchains/images.toml`, Go and Rust, each Go module and crate that is behind |
| `toolchains/cargo-auditable/latest.sh` | cargo-auditable, which every Rust image builds with, step 5c |
| `vulkan/latest.sh` | the Debian snapshot of the bases and `astrometry`, step 5 |
| `indi/latest.sh` | the snapshots of Ubuntu and the two PPAs, step 5a |
| `trust/latest.sh` | the CA bundle, step 5b |
| `bluetooth-operator/bluetoothd/latest.sh` | the buildroot commit that the bluetoothd image builds with, which follows `liken/userland/VERSION`, step 5e |
| `library-operator/latest.sh` | OpenVINO, Intel's compute-runtime, gmmlib, and the graphics compiler in the appearances image, step 5e |
| `ci/latest.sh` | the GitHub Actions, prek, and the hook revisions |
| `liken.sh/latest.sh` | the Terraform providers |

## 2. Bump

Run `<domain>/latest.sh --bump` for each behind domain, sequentially,
in one background task. These rules matter:

1. Put `systemd-boot` and `grub` last. Each bump downloads the files
   of its Ubuntu source package from the archive to check them
   against the signed Sources index, and those downloads are the
   slowest of the run.
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
   `userland/patches/`. buildroot's recipe holds patches for the
   version that it names, and buildroot applies them to the version
   that `versions.mk` sets too. A patch whose change the new release
   already holds does not apply, and the build stops at the package's
   patch step. Check each patch of the recipe against the source of
   the new version, and write the file name of each one that the
   release holds into `userland/patches/<package>/<version>/held`,
   with a comment line that says where the release holds it.
   `userland/buildroot.sh` keeps buildroot from applying a held patch,
   and still applies every other one, as `zic` needs. Report each
   patch that the bump drops. The report also has a row
   for util-linux, a library that `mke2fs` and `mount.nfs` link, and
   `--bump util-linux` sets its version and its series. For open-iscsi, kmod, and BlueZ, whose recipes
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
   behind only after k3s moves. Its `iptables` and `buildroot`
   sub-rows are the sources that the k3s-root release builds from,
   and the xtables bump writes them too.
5. The bump of `kernel`, `xtables`, `systemd-boot`, or `grub` also
   writes the entries of the corresponding source that
   `licensing/sources.sh` mirrors, in the same `pin.toml`. Finish with
   one `licensing/sources.sh --verify`, which downloads each of those
   files into `licensing/cache/` and checks it against its digest. It
   skips a cached file that matches, so a pass after the first is
   cheap. If it fails, stop and report which file it could not fetch
   or which digest did not match.

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
   files, `fetch.sh` digests, and `toolchains/images.toml`, nothing
   else.
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

The `weston` entry of `weston/package.toml` and the `mpv` entry of
`mpv/package.toml` are the exceptions. The weston image builds Weston
from the upstream release, and the mpv image builds mpv from the
upstream release, so `weston/latest.sh` and `mpv/latest.sh` report
and bump those entries, with the tarball's URL and digest in the
Dockerfile and in the `pin.toml` beside it, which the image's SBOM
record reads. The snapshot holds no package that either image installs
under that name. `built_from_source` in `vulkan/latest.sh` names both
entries, so the snapshot's report and its `--bump` skip them. A bump
of mpv can bring a new meson option, which the build leaves off until
`mpv/Dockerfile` names it, so read the release's `meson.options`
against the Dockerfile's list.

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

A new revision has no published image until `main` publishes it, so
the first run of the bump builds the new base from the tree, from
snapshot.debian.org, and builds each consumer on it. Every run after
that pulls the base by its digest, and `make bases` on a workstation
does the same. A bump needs no second commit.

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
read. "Closures" in the `images` skill gives the form.

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
No Dockerfile names `buildkit` either: `ci/` writes it into the
`driver-opts` of each `docker/setup-buildx-action` step, so CI's
builders run that BuildKit daemon. A move of `buildkit` needs no
revision, and `make workflows` writes it into the workflows.
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
"Rust binaries" in the `images` skill gives the form. The bump writes
the version and the digest from the release's `.sha256` file into
`toolchains/cargo-auditable/fetch.sh`. The project signs neither file.
Then build each image whose Dockerfile runs `cargo auditable build`,
and check that syft still lists the crates of one of them:
`docker run --rm -v /var/run/docker.sock:/var/run/docker.sock
anchore/syft:v1.55.0 <image> -o json -q | jq '[.artifacts[] |
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
   The open problem
   `plans/open-problems/what-a-vulnerability-finding-does-to-a-build.md`
   holds the findings that no bump reaches and the idea of a check
   in CI that fails on a finding a bump can reach.
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
into `Dockerfile.appearances`, and the OpenVINO bump writes the same
facts, with the purl and the CPE, into `library-operator/pin.toml`,
which the image's SBOM record reads. `appearances/inference-closure.sh`
names the versioned library files of each runtime, so move those
names by hand, then run `docker buildx bake
library-operator-appearances`.

## 5f. A new or changed latest.sh or fetch.sh

Load the `pins` skill before you write or change a `latest.sh`, a
`fetch.sh`, or a `pin.toml`. It holds the rules of a pin, the library
`toolchains/pin.sh`, the manifest, the signature that a bump verifies,
and each pattern that `cd ci && go run . pins -root ..` refuses.

## 6. Report

Give one verdict per bumped component: what changed, whether there
is anything to worry about, and what to watch in the drills. Lead
with the concerns, if there are any. The tree stays uncommitted;
the build, the smoke drills, and `/release` are separate decisions.
