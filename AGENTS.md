# The liken repository

The `liken` project is one system, and this repository holds all of
it. Each top-level directory is one component, named for what it
ships. `liken/` is the OS. The operators claim a machine's hardware
and serve its interfaces. The CSI drivers attach storage, and `brand/`
holds the theme, the voice rules, and the site tools. `kubernetes/`
is the Go module that the operators import to read, write, and watch
Kubernetes objects. `notices/` holds the scripts that copy the license
notices of the third-party software in an image into that image. `ci/` reads each
component's `package.toml` and writes the CI workflows. `liken.sh/`
declares the domain, the release channel, and the organization's
repositories in Terraform. `plans/` holds the plans that cover more
than one component, and each component keeps its own plans in its own
`plans/`. The plans of the OS are in `liken/plans/`. Each component has its own
`AGENTS.md` for the work inside it.

Four repositories stay outside: `corrosion`, `plugins`, `log`, and
`liken-dev-cluster`. Plan 69 in `plans/completed/` gives the reasons.

## Work across components

Start the session at the repository root when a change crosses a
component boundary, when a task names more than one component, or when
the work is to reason about the project as a whole. From there the
whole tree is visible, and each change can land in the component that
owns it. A session inside one component reads that component's
`AGENTS.md` and the rules here.

The agent skills are under `.agents/skills`. The `skills/` directory
inside a component holds a different kind of skill: the skills that
the component's manual publishes for its users.

## Voice

Every word the project publishes follows the rules in
`brand/voice.md`. The rules cover the sites, the guides,
the reference text, the comments in the source files, the plans, and
the commit messages. Read the file before you write, and check your
text against it before you publish it.

@brand/voice.md

The rules are ASD-STE100, Simplified Technical English, with additions.
Four of them matter most in a session that crosses components: name the
component and the object it acts on, give the reason before the
description, keep the code face on every identifier, and describe the
system as it is now. `liken` names the code, so it takes the code face
too.

## Privacy

The repository and everything written into it are public. Many
people here test against their own home clusters, and a home cluster
holds that person's real hardware, workloads, services, and data. Keep
those details out of every comment, plan, document, commit message,
change description, and chat message. That includes hostnames, the
names of workloads and services, personal data, and the people or
places behind them.

When a fact comes from a home cluster, describe the cluster only as far
as the point needs: "a home cluster", "a three-node fleet". When you
need detail, use a cluster the project ships: the `dev-cluster/` in
`liken`, the `lab` fleet of `node-1` through `node-5`, and the test
clusters named in a component's own documentation.

## Keep state current with events

An operator that reads the same state again on a timer wakes for
nothing, loads the API server, and can disturb a device. A TV on a CEC
bus switched its input every few minutes while an operator scanned the
bus each minute, and stopped when the operator went silent. So every
component keeps its view of the world current in the same three steps:

1. Open the watch, subscription, or stream first.
2. When it is live, read the whole state once, to set the baseline.
   The subscription keeps the state current after that. The
   subscription opens before the read, so an event that arrives during
   the read is not lost.
3. When the subscription fails, open it again and read the whole
   state again.

For Kubernetes, list first and then watch from the list's
`resourceVersion`. The watch starts at that version, not at the present,
so the API server sends every change made after the list, and the order
loses nothing. A `410 Gone` means the version is too old: list again. A
watch with no `resourceVersion` first sends every object as it is now,
and then the changes, so each open costs as much as a list. Start a
watch from a version, and open it again from the last version it
delivered.

A watch loop needs three guards: resume from the last version instead
of listing, list again only for a 410 and only once, and decide the
wait by how long the server kept the watch open. Each guard failed in
review in a hand-written loop, so the operators watch through
client-go's reflector, which upstream maintains. The `operators` skill
under `.agents/skills` holds the guards in full, the scenarios a watch
must pass in a test, and the shape of the client-go port. Load it
before you write or review an operator's watch, pass, or timer.

Every other source has no version to resume from, so the subscription
must open before the read. The same shape fits `pw-dump -m`, an MQTT
subscription with retained messages, an inotify watch followed by one
look at the directory, and a CEC receive loop followed by one scan of
the bus.

A timer is correct only as a clock: a time-to-live, a deadline, or the
age of a certificate. A timer that reads state again to find a change
is a defect. If a component needs a long resync as a backstop, a
comment at the timer gives the failure it covers.

## Conditions, Events, and log lines

A condition answers "what is true now", for `kubectl wait` and for a
controller. An `Event` answers "what just happened to this object",
for a person who runs `kubectl describe`, and the API server deletes
it an hour after its last write. A log line holds every attempt and
detail. So every component posts one `Event` for each condition
transition, and one for each action it takes that changes no
condition, such as a pod created again or a reboot requested. It never
posts a reading, a key press, or each retry of a retry loop. A fact
that must outlast the hour stays in status.

Every component writes its `Event`s through `kubernetes/events` and
declares its conditions with `kubernetes/conditions`. The setter
`Recorder.SetCondition` posts the `Event` of each transition. A test
reads the `Event`s from `kubernetes/events/eventstest` inside a
`synctest` bubble. Root plan 78 holds the rule and the reasons, and
the `operators` skill holds the detail.

## Tests run on the Go toolchain alone

CI runs each component's tests on every push, so a test that waits on
the wall clock, pulls an image, or reaches the network makes every push
slow and fragile. A test waits on the fake clock of a `synctest`
bubble, never on real time. A test starts no container and no cluster,
and reaches no host outside the process. A test of an operator talks to
a fake API server through `kubernetes/apiservertest`, whose in-memory
connections keep the bubble's clock moving where a real socket stops
it. Proof against real machines happens by hand on the `lab` fleet and
on a physical test cluster. The `testing` skill under `.agents/skills`
holds the fakes for each peer and device, the coverage gate, and the
gotchas. Load it before you write or review a test.

## The manual changes with the code

A reader installs and runs each component from its manual alone, so a
manual that lags the code gives that reader a wrong command. A commit
that changes what a person installs, runs, reads, or sees updates the
component's manual in the same commit. That includes a manifest, a
container, a flag, a metric, a printer column, a condition, an
`Event`, and a behavior the component adds on its own. When the change
crosses into another component's domain, update that component's
pages too. The `operators` skill under `.agents/skills` lists the
pages each kind of change touches.

## Branch names

This repository works on `main`. `corrosion`, outside this repository,
is a shallow fork of superfly/corrosion: its `main` mirrors upstream and
takes no commits, and its own work is on the `liken` branch. Treat
`liken` as `corrosion`'s main branch in any process that names one.

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
`bump-components` skill holds the procedure, the patterns, and the
comment that excuses one line.

- The digest of every file that a pin downloads is in the
  repository, and `latest.sh --bump` writes it. No `fetch.sh`,
  Dockerfile, or other build step reads a digest from the network,
  because a checksum file read at build time accepts whatever the
  upstream serves that day.
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
  name, so that one entry pins each outside image.
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

## Closures

An image that ships files of Debian or Ubuntu packages without the
packages is a closure on scratch, and a closure holds no
`/var/lib/dpkg/status`. Without a package record, syft writes an SBOM
with almost none of the image's packages, and Trivy finds no package
to match against an advisory.

So every closure on a Debian or Ubuntu builder sources
`vulkan/closure.sh`, the closure library, through a build context, and
calls `collect` and then `notices`. No closure writes its own ldd walk
or its own copy of the notices. For every package that the tree holds
a file of, `notices` writes `/var/lib/dpkg/status.d/<package>`, the
package's stanza from the builder's dpkg status, and
`/var/lib/dpkg/status.d/<package>.md5sums`. It also copies
`/etc/os-release` and the distribution's version file, so a scanner
knows which advisories apply. syft and Trivy read this layout, which
Google's distroless images use. `indi/closure.sh` is a copy of the
library, because the build context of `indi` does not reach `vulkan/`,
and `cd ci && go run . pins -root ..` fails when the two copies
differ.

## Rust binaries

A Rust binary holds no record of the crates that it links, so syft
and Trivy list none of them from an image, and a scan matches no
advisory against them. So every Dockerfile that builds Rust runs
`cargo auditable build` in place of `cargo build`. cargo-auditable
records the crate graph of the build in the binary's `.dep-v0`
section, and both scanners read that section. The stub build of the
dependency layer runs through it too, so the dependencies compile
once under the same wrapper that the real build uses.

`toolchains/cargo-auditable/fetch.sh` pins cargo-auditable, and the
`latest.sh` beside it reports and bumps the pin. Each Rust image
takes `toolchains/` as the build context `toolchains`, copies
`fetch.sh` and `pin.sh` from it, and runs `fetch.sh`, so one pin
serves every image. `cd ci && go run . pins -root ..` fails on a
Dockerfile that runs `cargo build`.

## Go programs

A Go binary records the modules it links in its build info, and syft
and Trivy read that record to list the modules of an image. Every
Dockerfile that ships a Go program builds it the same way, so the
record is in every binary, and a person, a manifest, or an operator
that writes a pod's command finds the program at the same path in
every image:

```dockerfile
FROM golang AS build
...
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /<name> .
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /<name> /<name>
COPY --from=build /notices /
ENTRYPOINT ["/<name>"]
```

- The builder is the `golang` entry of `toolchains/images.toml`. A CLI
  image runs the build stage with `--platform=$BUILDPLATFORM` and
  cross-compiles with `GOOS` and `GOARCH`.
- `CGO_ENABLED=0` makes a static binary that runs on `scratch` and on
  any base, and the build needs no C toolchain. No Go program in the
  repository needs cgo. `media-capabilities` calls libva through
  purego, which needs no cgo, but its binary names the dynamic loader,
  so it runs only on the `vaapi` base that carries libva.
- `-trimpath` keeps the paths of the build machine out of the binary.
- `-s -w` drops the symbol table and the DWARF data. The build info is
  in its own section, which `-s -w` keeps, and
  `go version -m <binary>` prints it. A panic's stack trace keeps its
  function names. No build runs upx, strip, or objcopy on a Go
  program, because upx and objcopy can hide or remove the build info.
- `-X main.version=${VERSION}` is there only when the program has a
  `version` variable.
- The program is at `/<name>`, where `<name>` is the program's name,
  in every image that ships it, on any base. A second program of the
  same image, such as a pod build or a helper that a manifest runs, is
  at `/<its name>` too.
- `notices/go.sh` writes the license notices of the standard library
  and of each linked module. An image whose program makes HTTPS
  requests to hosts outside the cluster copies the `trust` component.
- When every pod that runs the image sets a non-root `runAsUser`, the
  image names the same user with a numeric `USER`, so `docker run`
  runs the program the same way. An image that a pod runs as root
  keeps root and names no `USER`.

`cd ci && go run . pins -root ..` fails on a `go build` without
`CGO_ENABLED=0`, `-trimpath`, or `-ldflags "-s -w"`, on a program
built, copied, or named in an `ENTRYPOINT` at a path other than
`/<name>`, and on upx, strip, or objcopy run on a program.

## Static programs

A program that the OS image carries from another project must be one
static binary, because a `liken` machine has no loader and no shared
libraries. buildroot builds these programs, from the BR2_EXTERNAL tree
in `liken/userland/`, because it builds the musl toolchain and every
library from source tarballs that its `.hash` files pin, and its
`legal-info` and `utils/generate-cyclonedx` write the source offer and
the CycloneDX SBOM of each build. An Alpine container with `apk add`
installs the packages of the day of the build, so no new build takes
that form.

- A new program is a buildroot package that `liken/userland/defconfig`
  selects, and `liken/userland/build.sh` copies its file into `dist/`.
  A change to buildroot's recipe is a hook in `external.mk`.
- buildroot is pinned to a commit on its `master` branch in
  `liken/userland/VERSION`, and its builder is the `buildroot` entry of
  `toolchains/images.toml`. When master packages an older release than
  upstream, `liken/userland/versions.mk` sets the newer version, and
  `patches/<package>/<version>/<package>.hash` pins its tarball.
- `liken/open-iscsi` and the `bluetoothd` image still build on Alpine,
  because buildroot's recipes for open-iscsi, kmod, and BlueZ refuse a
  static-only build.

## Releases and development builds

One CalVer tag releases every component whose outputs changed since its
own previous release, and a push to `main` publishes a development
build of each component that changed. Each component's `package.toml`
names its dependencies, its checks, and its outputs, and `ci/` writes
`.github/workflows/`, `.github/dependabot.yml`, and `docker-bake.hcl`
from those files, from the `FROM` lines of each Dockerfile, and from
the `go.mod` and `Cargo.lock` files in the tree. Run `make workflows` after you
change a `package.toml` or a `FROM` line; CI fails when the generated
files differ from what the generator writes.

Every image builds through `docker-bake.hcl`, on a workstation and in
CI, as the target of the same name:
`docker buildx bake media-operator-player --load` at the top of the
repository. A Dockerfile that builds on a base image of the repository
names it bare, as in `FROM mpv`, and the bake file builds that base
from its own directory at the same commit. No Dockerfile names a
published tag of a base.

The base images, `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`, are
pinned components: each states a version, the date of the snapshot of
Debian sid that it installs from, and a revision, and publishes under the tag
`<version>-<revision>`. A change to a base's recipe needs a new
revision, and so does each pinned component that builds on it. CI fails
when a recipe changed with no new revision. Each base lists the
upstream releases it holds under `[package.upstream]`, and each entry
becomes the image label `sh.liken.upstream.<name>`. `indi` is pinned
the same way, on dated snapshots of Ubuntu, the INDI PPA, and the PHD2
PPA, and CI builds its 17 images only when its tag is new.
`astrometry`, the plate solver that `astrophotography-operator` runs,
is pinned on the bases' Debian snapshot and moves with them. `trust` pins
the CA bundle that the OS build and every image that verifies TLS read,
so one bump reaches all of them. The `bump-components`
skill under `.agents/skills` holds the procedure.

Every image that CI publishes, as a development build, a release, or
a pinned component's tag, carries an SBOM and the provenance of its
build, so a person who pulls it can read what it holds and how it was
built. buildx writes both as attestations into the image's index on
ghcr.io, with the syft scanner that `toolchains/images.toml` pins.
Then cosign, pinned in the same file, signs the digest of that index
keyless, as the component's workflow at `refs/heads/main` or at the
release tag. The signature is in the same ghcr.io repository, and
Rekor records it.
One signature covers the image and both attestations, because the
index names each of them by its digest. `ci/sign.go` holds the
settings, and the `releases` skill holds the commands that verify an
image:

```sh
cosign verify ghcr.io/liken-sh/<image>@<digest> \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/liken-sh/liken/\.github/workflows/component-[a-z0-9-]+\.yaml@refs/(heads/main|tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3})$'
docker buildx imagetools inspect ghcr.io/liken-sh/<image>@<digest> --format '{{ json .SBOM }}'
docker buildx imagetools inspect ghcr.io/liken-sh/<image>@<digest> --format '{{ json .Provenance }}'
```

The `releases` skill under `.agents/skills` holds the calendar scheme,
what a tag releases, and how to pin a development build. Load it before
tagging, publishing an image, or pinning a development build.
