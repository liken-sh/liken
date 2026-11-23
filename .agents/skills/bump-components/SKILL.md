---
name: bump-components
description: Bumps every pin that is behind the newest upstream release, under the rule in root plan 83 - the top-level make versions report, each latest.sh --bump, the changelog research, and a final report of what changes and what to watch - including the toolchains, the whole Go module and Rust crate graphs with a vulnerability check, the pinned base images, their Debian snapshot and their revisions. Use when the maintainer says /bump-components or asks to bump, update, or refresh the component pins, the kernel, k3s, or the base images, to clear out Dependabot pull requests or dependency bumps, or to bump dependencies to clear critical and high CVEs or code scanning alerts. To add a pin or write a latest.sh, use the pins skill.
---

Bump every vendored pin that is behind, read the changelogs, and
report what is changing and what to look out for.

Additional context from the maintainer: $ARGUMENTS

If that context names specific components, or says to skip the
research or to stop after a step, follow it instead of the defaults
below.

## Which section to read

* **A pass over every pin:** every step, in order.
* **A request that names one pin, such as Flux:** steps 1 to 4 and
  step 6 for that pin, and the part of step 5 about its kind, if any.
* **The base images and their snapshot:** step 5. **INDI:** step 5a.
  **The CA bundle of `trust`:** step 5b.
* **A toolchain, an outside image, or a GitHub Action:** step 5c.
* **The Go and Rust graphs, Dependabot, or a CVE:** step 5d.
* **The bluetoothd or appearances image:** step 5e.
* **htmx, which `transect` serves:** step 2, rule 6, and
  [`references/htmx.md`](references/htmx.md).

The bumps themselves are quick edits to the working tree. The value
of a pass is in the research, and in the builds that prove each
bump. A pass ends with one commit for each domain that moved. Step 6
gives the order of the rollout.

A pin is current when it matches the newest upstream release that is
not a pre-release. A pin built from source follows the newest
upstream release. A pin from a package repository, a registry, or a
channel server follows two things: the newest channel that the
repository offers to install as a whole, and the newest package in
that channel. These channels are Debian sid, Alpine `edge`, Ubuntu's
development series, k3s `latest`, and the newest series that a PPA
publishes. The reasons are in root plan 83.

Take a new major version in the same pass as every other bump. Read
its release notes, port the code that it breaks, and prove it with
the tests, the builds, and the smoke checks. Hold it back only when
it breaks something that the pass cannot fix. Then write the reason
in the hold column of its report row, as for every hold. A major
version that waits for a pass of its own stays behind, with every
fault that upstream has already fixed.

Step 1 runs at the top of the repository. In steps 2 to 4, a command
runs from `liken/`, and its paths are relative to `liken/`, unless
the step says that it runs at the top of the repository, as
`make workflows` and `make versions` do. Steps 5 and later work at
the top of the repository.

## 1. Pull the report

Run `make versions` at the top of the repository. `versions.sh` finds
every `latest.sh` in the tree, runs them in parallel, and prints one
table. It contacts every upstream, so allow a minute or two. Collect
the rows whose state is `behind` or `wrong channel`. An indented row
is a sub-pin of the row above it, and it needs a bump of its own, by
name.

Each `latest.sh` prints tab-separated rows of seven columns: name,
pinned, latest, channel, newest channel, hold, and note.

- **`channel`** is the suite, series, release, or channel that the
  pin reads. **`newest channel`** is the newest one that its
  repository offers. A pin built from source, or a pin that follows
  another pin, prints `-` in both.
- **`hold`** is `-`, or the reason that the pin stays back. A hold
  needs a reason that a reader can check. The reason is in the row,
  never only in a comment.
- **`latest`** is `?` when the upstream did not respond.

The state of a row is the first of these that applies:

1. `held` when the row has a hold.
2. `unknown` when an upstream did not respond.
3. `wrong channel` when the two channels differ.
4. `behind` when pinned and latest differ.
5. `current` otherwise.

The report can show a pin as `current` when the maintainer says that
it is behind. Some pins follow a channel that lags the upstream
project. For example, `liken/kernel/latest.sh` follows Canonical's
mainline builds, which come some days after kernel.org tags a
release. When this happens, read the header comment of the pin's
`latest.sh` to learn what the pin follows. Then tell the maintainer
what the report shows and why. Then stop. `--bump` moves a pin only
to the version that its `latest.sh` reports, so there is nothing to
bump.

The steps below cover each pin whose bump needs more than its
`--bump` and the build that its `next:` line names. Any other row,
such as a Terraform provider of `liken.sh/latest.sh`, needs only
those two. Some scripts need a step that no section below gives.
[`references/pins-with-a-manual-step.md`](references/pins-with-a-manual-step.md)
lists them, such as `liken/verify/latest.sh`.

## 2. Bump

Run `<domain>/latest.sh --bump` for each domain that is behind, one
after another, in one background task. Follow these rules:

1. Put `systemd-boot` and `grub` last. Each of these bumps downloads
   the files of its Ubuntu source package from the archive, to check
   them against the signed Sources index. Those downloads are the
   slowest part of the run.
2. A sub-pin needs a bump by name, for example
   `userland/latest.sh --bump kmod` or
   `linux-firmware/latest.sh --bump wireless-regdb`.
3. The static programs in `userland/` follow buildroot's `master`
   branch. When a row of `userland/latest.sh` shows `behind`, read
   [`references/userland.md`](references/userland.md) for the bump
   and the build that proves it. When a bump sets a newer version in
   `userland/versions.mk`, also read
   [`references/buildroot-patches.md`](references/buildroot-patches.md).
   The patches of buildroot's recipe can fail to apply to the new
   version, and that file says how to mark each one as held.
4. Bump `k3s` before `xtables`. The xtables pin follows the k3s-root
   release that the pinned k3s names as `VERSION_ROOT`. So
   `xtables/latest.sh` reads `k3s/VERSION`, and it reports xtables as
   behind only after k3s moves. The `iptables` and `buildroot`
   sub-rows of xtables are the sources that the k3s-root release
   builds from, and the xtables bump writes them too.
5. The bump of `kernel`, `xtables`, `systemd-boot`, or `grub` also
   writes the entries of the corresponding source that
   `licensing/sources.sh` mirrors, in the same `pin.toml`. At the end,
   run `licensing/sources.sh --verify` once. It downloads each of
   those files into `licensing/cache/` and checks it against its
   digest. It skips a cached file that matches, so a run after the
   first is fast. If it fails, stop, and report which file it could
   not fetch or which digest did not match.

   When a pin of the OS moves, check `liken/licensing/NOTICES.md` in
   the same commit, because a release can change the upstream's
   license or its copyright line. A new component whose license
   requires the source needs a mirror in `liken/licensing/sources.sh`.
6. `transect/crates/transect/src/web/static/latest.sh --bump` stops
   at a new major version of htmx, because the templates and the
   scripts of `transect` need a port first.
   [`references/htmx.md`](references/htmx.md) gives the port, the
   bump that follows it, and the browser check that proves both.

Each bump prints a `next:` line that names the make target that
rebuilds its domain. Run the line from the pin's own directory. For
example, `liken/kernel/latest.sh` prints `make -C .. kernel`, which
you run in `liken/kernel/`. `make all` covers those targets, except
`docs`: `liken/docs/latest.sh` prints `make -C .. docs`, and `all`
does not build the site. One target
needs care: `make storage` is not a build. It starts the lab's
storage guest under QEMU, and it runs in the foreground for the whole
life of the guest. So never chain it before another target.

These bumps need more care:

- **A k3s minor bump** is a Kubernetes minor bump. It can remove an
  API version that a workload still uses, so read the upstream
  release notes before you take one.
- **A k3s bump can add a packaged manifest.** k3s writes the files of
  `manifests/` in its repository into the auto-deploy directory at
  each start. A file that needs a controller that `liken` turns off,
  such as a `HelmChart` while the Helm controller is off, never
  applies. Then k3s's deploy controller applies every manifest again
  on every pass, and the smoke drills fail with
  `k3s did not apply these manifests`. Compare `manifests/` between
  the two k3s tags, and add each such file to `controllerManifests` in
  `liken/cluster/features.go`. `liken` milestone 101 has the detail.
- **A kernel bump.** Before you take one, search the repository for
  items tied to the current kernel, such as a workaround that should
  be tested again on the next kernel. Search the open problems with
  `git grep -l -i kernel -- ':(glob)**/plans/open-problems/**'`, and
  the source for a comment that names a kernel version with
  `git grep -n -i -E '(kernel|linux) v?[0-9]+\.[0-9]+' -- ':!*.md'`.
  The search finds comments such as "Linux 7.3.1" in
  `bluetooth-operator/bluetoothd/rescan/main.go` and "kernel 7.1.5" in
  `liken/image/stick.go`.
- **A Flux bump.** `flux/latest.sh --bump` also runs
  `ci/latest.sh --bump fluxcd/flux2`, which writes the commit of the
  flux action into `ci/actions.go`. The generated workflows name that
  commit. So run both `next:` lines that the bump prints:
  `make workflows` at the top of the repository, and
  `make cluster-operator` in `liken/`.

## 3. Read the changelogs

Read the changelogs for every bump. Do it for a bump that clears a
vulnerability finding too, because a release that fixes one advisory
can also change behavior or break compatibility.

For a pass over many pins, dispatch research agents in parallel, with
a few components for each. For a request that names one pin, read
its release notes in the session. Each reader reads the real release
notes on the web. It reports
breaking changes, changes of behavior, CVE fixes, regressions that
people reported against the new tag, and anything that matters on a
machine with 1GB of memory. Give each agent the entries for its
components from
[`references/changelog-sources.md`](references/changelog-sources.md).
That file says where the notes of each OS component are, and which
part of them matters to `liken`.

Note the age of each release. A firmware or microcode release that
is a few days old has no history in the field. The smoke drills and
the first boot on real hardware are its first real test.

## 4. Verify

1. Run `make versions` again, at the top of the repository. Every row
   must show `current` or `held`.
2. Run `make workflows` at the top of the repository after any bump.
   It reads only the tree, so it changes nothing when no generated
   file depends on the pin.
3. Run `cd ci && go run . pins -root ..` at the top of the repository
   after any bump. It fails when two copies of a pin disagree, or
   when an `ADD --checksum` has a digest that no manifest lists.
4. Run `git diff --stat`. Each of these files appears only when the
   bump changes it:
   - `VERSION` files, `pin.toml` files, digests in `fetch.sh`, and
     `toolchains/images.toml`
   - `docker-bake.hcl` and `.github/`, from `make workflows`
   - `go.mod`, `go.sum`, and `Cargo.lock`
   - the files under `userland/patches/`
   - the generated files of `observatory-operator`, after an `indi`
     bump
   - each manifest, guide, and skill that names a `[run]` image at
     its tag, as step 5c says

   Any other file needs a reason in the report.
5. Run `make all` in `liken/` for a pass, and `make docs` there when
   Hugo moved, or the build that a single bump's `next:` line names,
   such as `make kernel`. A changelog does
   not show breakage in configure or in the toolchain, and a bump that
   does not build is not done. The programs that `userland` compiles
   from source against musl break most often. A new configure check
   or a dropped default there is invisible until the build runs.
6. Run `make smoke-uefi` and `make smoke-bios` in `liken/` after
   `make all`, and after a bump that changes how a machine boots, such
   as the kernel, a boot loader, the firmware, or `userland`. A boot to
   Ready proves that the parts still assemble into a working machine.
   The BIOS boot proves a `grub` bump, because the core image of GRUB
   must still fit in the `biosBoot` partition. The `dev-cluster-drills`
   skill says how to run the drills and how to stop the lab after
   them.

## 5. The base images

The base images, such as `vulkan` and `mpv`, are pinned components at
the top of the repository. The root `README.md` names them. The
`package.toml` of each one sets a `version`, a `revision`, and the
date of the snapshot.debian.org archive that it installs from, as
`YYYYMMDD`. Each one publishes under the tag `<version>-<revision>`.
`vulkan/latest.sh` reports their snapshot date, and each
`[package.upstream]` entry, against the newest snapshot of sid.

**A new snapshot.** Every base moves together, because a library
that two bases include must be the same file in both.
`vulkan/latest.sh --bump` writes these:

- the date, revision 1, and the `[package.upstream]` entries, into
  every `package.toml` that installs from the snapshot
- the sha256 of the snapshot's `InRelease` file, into
  `vulkan/snapshot.sha256`
- the digest of the newest `debian:sid-slim`, into the `debian` entry
  of `toolchains/images.toml`

Then run `make workflows`. Build each image with
`docker buildx bake <targets> --load`. On each image that you built,
run the `smoke` script that its `[[outputs.images]]` entry in the
component's `package.toml` names. `make images` builds every image of
the repository, and a bump of the bases does not need that.

The report shows the snapshot as `behind` on every day after a bump,
because sid accepts uploads every day. Compare the package versions
with the old snapshot, and report the ones that moved.

The suite is sid, Debian unstable, so a new date can bring a new
major release of a library such as Mesa or ffmpeg. Read the upstream
release notes of each major release that moved, as in step 3. Roll
the development builds out to the `liken-1` testbed before any other
cluster takes them. The reasons for sid are in root plan 82.

`astrometry`, the plate solver, is not a base. But it installs from
the same snapshot through `vulkan/snapshot.sh`, and
`vulkan/snapshot.sha256` has the checksums of only one date. So it
moves with the bases: its `version`, `revision`, digest, and
`[package.upstream]` change in the same commit.

**The upstream entries.** A pinned version is a date. So each base's
`package.toml` also lists, under `[package.upstream]`, the upstream
releases that matter in it, such as `mesa` and `ffmpeg`. Each entry
becomes the image label `sh.liken.upstream.<name>`, so a reader of
the registry can see which releases a tag contains. A new snapshot
updates every entry to the version that the snapshot installs. Read
the versions from the snapshot's `Packages` indexes, or with
`dpkg-query -W` in the builder.

**Weston and mpv.** The `weston` and `mpv` images build Weston and
mpv from the upstream releases, not from packages of the snapshot,
so the snapshot bump skips their entries. When the `weston` or `mpv`
row shows `behind`, read
[`references/weston-and-mpv.md`](references/weston-and-mpv.md).

**A new revision.** CI computes a hash of each base's recipe. The
recipe covers the base's `package.toml`, its `Dockerfile`, every file
in its build context, the digest of each image from outside the
repository that it starts from, and the tag of each component in its
`[depends]`. A change to any of them at the same version needs the
next revision.

The rule cascades. The tag of a base is in the recipe of every pinned
component above it. So a new revision of `ffmpeg` changes the recipe
of `mpv`, and `mpv` needs a new revision in the same commit. Walk the
graph up from each base that changed, through the `[depends]` of each
`package.toml`. Raise the revision of every pinned component that
depends on the base, directly or through another base. A new snapshot
sets every revision to 1, so it needs no cascade. When CI fails with
"the recipe of N pinned components changed with no new revision", the
message names each component to raise.

A recipe also covers the build contexts from other directories, and
the images from outside the repository, such as `toolchains/manifest`
and the `golang` entry. So a bump of Go or an edit under
`toolchains/` can need new revisions too. "The bake file and the base
images" in the `images` skill has the rule, and the check to run
before a push.

A new revision has no published image until `main` publishes it. So
the first CI run of the bump builds the new base from the tree, from
snapshot.debian.org, and builds each image that uses it on top. Every
run after that pulls the base by its digest, and so does `make bases`
on a workstation. A bump needs no second commit.

The files that a base's `.dockerignore` excludes, such as its
`README.md` and its `smoke/` check, are not part of the recipe. A
change to them needs no new revision.

**The closure library.** A change to `vulkan/closure.sh` needs a new
revision of `vulkan` and of `indi`, and the cascade above. Before you
change it, read
[`references/closure-library.md`](references/closure-library.md).

## 5a. The INDI images

When a row of `indi/latest.sh` shows `behind` or `wrong channel`,
read [`references/indi-images.md`](references/indi-images.md). It
covers the snapshot bump, the SDK licenses, the builds and smoke
checks, and the steps that generate the files of
`observatory-operator` again.

## 5b. The trust store

When the `trust` row shows `behind`, read
[`references/trust-store.md`](references/trust-store.md). It covers
the bump of the CA bundle, the revisions that the bump needs, and
what to read before you take it.

## 5c. The toolchains

`toolchains/images.toml` lists each image from outside the repository
once. A Dockerfile names an image from its `[build]` table by its bare
name, as in `FROM golang`, and `ci/` writes the reference and its
digest into the bake target. Manifests, guides, skills, and tests
name a `[run]` image at its tag.

`toolchains/latest.sh --bump <key>` writes the newest tag of an
image, and also its digest for a `[build]` image. For a `[run]`
image, it writes only `toolchains/images.toml`, and its `next:` line
lists each file that still has the old reference. Edit each of those
files. Then run `make -C <component>/docs skills` in each component
whose guides changed. The published skills are generated from the
guides, and the component's `test-docs` target fails when they are
out of date.

The bumps of Go, Rust, and their dependencies write more than one
file:

- **`--bump go` and `--bump rustc`** move the toolchain image and
  every `go.mod` or `rust-toolchain.toml`.
- **`--bump go:<module>` and `--bump crate:<name>`** move every copy
  of one dependency.
- **The k8s.io modules** follow the k3s release in
  `liken/k3s/VERSION`, so they move after k3s does.
- **`go mod tidy`.** After `--bump go` and `--bump go:<module>`, the
  bump runs `go mod tidy` in each module that it wrote. It also runs
  it in each module that replaces one of those with a local
  directory, such as every operator that replaces
  `github.com/liken-sh/liken/kubernetes`. Without tidy, `go get`
  leaves the `go.sum` lines of the old version, and the
  `go mod tidy -diff` check of each component fails.

A new reference in `[build]` changes the recipe of each pinned
component whose Dockerfile names that key. So each of those
components needs a new revision, and the cascade in step 5 applies.

No Dockerfile names the `cosign` or the `buildkit` entry, so a move
of either needs no revision. When either row shows `behind`, read
[`references/cosign-and-buildkit.md`](references/cosign-and-buildkit.md).

Run `make workflows` after every bump of `images.toml`.
`cd ci && go run . pins -root ..` fails when two copies of a Go
version, a Rust version, a module, a crate, a hook revision, or an
image tag disagree, and CI runs it on every push.

When the `cargo-auditable` row shows `behind`, read
[`references/cargo-auditable.md`](references/cargo-auditable.md). It
covers the bump, the builds of the Rust images, and the syft check of
their crates.

When a row of `ci/latest.sh` shows `behind`, read
[`references/ci-actions.md`](references/ci-actions.md). It covers the
bump of a repository of GitHub Actions, prek, or a hook revision, and
what else each bump writes.

## 5d. The Go modules and the Rust crates

`make versions` reports only the direct and tool requirements of each
`go.mod`, and the exact `=` pins of each `Cargo.toml`. An indirect
module, or a crate that only a lock file lists, never shows in the
report. A known vulnerability is most often in one of those: for
example, the operators used `golang.org/x/net` through client-go as
an indirect requirement. So every bump also updates the whole
dependency graph of each module and each workspace.

Dependabot pull requests, and code scanning alerts on the Security
tab of the repository, are inputs to this step too. Read the open
pull requests with `gh pr list`, and the open alerts with
`gh api --paginate 'repos/liken-sh/liken/code-scanning/alerts?state=open&per_page=100'`.
The repository does not accept pull requests, so do not merge a
Dependabot pull request. Make the same change in a worktree, such as
`GOWORK=off go get <module>@<version>` in each module that requires
the module. Run the checks of item 7, and land the commit on `main`.
Dependabot closes its pull request when `main` has the version.

1. Run `toolchains/latest.sh --bump go:<module>` and
   `--bump crate:<name>` for each row that is behind, as in step 5c.
2. Go to the directory of each `go.mod` that `git ls-files` lists,
   except under `testdata/`. In each one:
   1. Record the `k8s.io/` and `sigs.k8s.io/` lines.
   2. Run `GOWORK=off go get -u ./...`. Add the `tool` pattern when
      the `go.mod` has a `tool` block, so that the docs modules move
      the graph of Hugo too.
   3. Run `go get` again with each recorded `k8s.io/...@<version>` and
      `sigs.k8s.io/...@<version>`, because those modules follow the
      k3s release.
   4. Run `go mod tidy`.
3. In the directory of each `Cargo.lock`, run `cargo update`. It
   moves every crate as far as the `=` pins and the semver ranges of
   the graph allow, and no further.
4. Another crate in the graph can block a crate whose newest release
   is a new major version. `toolchains/latest.sh --bump crate:` then
   fails with `failed to select a version for the requirement`. When
   it does, or when a Go module must stay back, read
   [`references/blocked-crates.md`](references/blocked-crates.md). It
   says when to keep the old pin, how to record the hold, and what to
   report.
5. A new major version of a direct dependency can change its API or
   its behavior. Read its migration notes, port each call site, and
   check each behavior that a comment at the call site depends on.
   For example, `ureq` 3 replaced the read timeout for each socket of
   `ureq` 2 with a deadline for each whole stage of a request.
6. Check the result for known vulnerabilities. Run `govulncheck ./...`
   in each module, and `osv-scanner scan source --lockfile=<lock>` on
   each `Cargo.lock`. Neither tool is pinned in the repository, so
   install each one into the session's scratch directory with
   `GOBIN=<dir> go install <module>@latest`. Report each finding that
   remains, with its fixed version, or the reason that no bump reaches
   it. The open problem
   `plans/open-problems/what-a-vulnerability-finding-does-to-a-build.md`
   lists the findings that no bump reaches. It also has the idea of a
   check in CI that fails on a finding that a bump can reach.
7. Run `cd ci && go run . pins -root ..`. Then run `make test` in
   each changed component, and the `run` of each `[[jobs]]` entry in
   its `package.toml` that names a `dir`. A component's `make test`
   does not always build those directories. For example,
   `library-operator`'s `make test` skips `appearances`, which CI
   builds as `rust-appearances`.

## 5e. The sources inside operator images

When the `bluez` or `dbus` row of `liken/userland/latest.sh`, or a row
of `library-operator/latest.sh`, shows `behind` or `wrong channel`,
read
[`references/operator-image-sources.md`](references/operator-image-sources.md).
It covers the bumps of the bluetoothd image and the appearances
image, and the builds that prove them.

## 5f. A new or changed latest.sh or fetch.sh

Load the `pins` skill before you write or change a `latest.sh`, a
`fetch.sh`, or a `pin.toml`. It has the rules of a pin, the library
`toolchains/pin.sh`, the manifest, the signature that a bump
verifies, and the check `cd ci && go run . pins -root ..`.

## 6. Report

Give one verdict for each bumped component: what changed, whether
there is anything to worry about, and what to watch in the drills.
Put the concerns first, if there are any. List each row that still
does not show `current`, with its hold.

Make one commit for each domain that moved, such as tzdata, the boot
loaders, the bases and their snapshot, `indi`, the CI actions, or the
Terraform providers. Then a reader or a bisect finds each move on its
own. A change that a bump forced in another file, such as a port to
a new major version or a regenerated file, goes in the commit of that
bump. So does a file outside the domain that the bump script wrote,
such as `ci/actions.go` and the workflows for Flux. The rest of
the mechanics are in the root `AGENTS.md` section "Branches and
landing" and in the `commit-messages` skill.

The rollout goes to the `liken-1` testbed first: the release, or the
development builds of the operators, and a soak there. A home cluster
takes the bump only when the maintainer says so.
