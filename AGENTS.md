# The liken repository

The `liken` project is one system, and this repository holds all of
it. Each top-level directory is one component, named for what it
ships, and `README.md` beside this file says what each one is for.

A component's `README.md` says what the component is for, what it is
responsible for, how it fits with the other components, and what its
main parts are. A component's `AGENTS.md` says how the component
runs, which rules a change to it must keep, and where its boundaries
are. Read both before you change a component. This file holds the
rules that every component shares, so a component's `AGENTS.md` does
not repeat them.

`plans/` holds the plans that cover more than one component, and each
component keeps its own plans in its own `plans/`. The plans of the
OS are in `liken/plans/`.

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
Files beside `brand/voice.md` each hold the rules for one kind of
text. Read `brand/messages.md` before you write an error, a log
line, the message of a condition or an `Event`, or a test failure.
Read `brand/names.md` before you name an identifier, a file, a test,
or a part of the API. Read `brand/instructions.md` before you write
an `AGENTS.md` file, a skill, or a brief for another agent. Load the
`commit-messages` skill before you write a commit message or the
description of a pull request.

## Privacy

The repository and everything written into it are public. Many
people here test against their own home clusters, and a home cluster
holds that person's real hardware, workloads, services, and data. Keep
those details out of every comment, plan, document, commit message,
change description, and chat message. That includes hostnames, the
names of workloads and services, personal data, the people or places
behind them, and the titles in a media library. For an example title,
invent one or use a work in the public domain.

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
`resourceVersion`, so the API server sends every change made after
the list. A watch loop needs three guards: resume from the last version instead
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

## Plans

Where a plan is states its status: a file at the top of a `plans/`
directory is not built, and a file in `completed/` is built. The
commit that builds a plan moves the plan to `completed/` and dates
its header, because a built plan that stays at the top tells the next
session that the work is open. A component's code exists only where a
plan calls for it. A small fix that a test reproduces and that
changes no design needs no plan. Load the `plans` skill under
`.agents/skills` before you write a plan or an open problem, and
before the commit that builds or closes one.

## Go components

Each operator and each CSI driver is one Go module in its own
directory, named `github.com/liken-sh/liken/<directory>`. `ci pins`
fails when a module's Go version differs from the `golang` image of
`toolchains/images.toml`, or when two modules name different versions
of one dependency.

- **The program is `package main`, flat at the top of the
  directory**, with one domain to a file and each file's tests beside
  it. No component has a `cmd/`, an `internal/`, or a `pkg/`. A
  subdirectory is a package only when another component imports it,
  when it is a protocol or a device driver, when it holds the Go
  types of the component's API, or when it is a second program. The
  fake of a package, for the tests of other packages, is in
  `<package>test/`. `liken/` is the exception: it is one module with
  a directory for each program.
- **An operator reads, writes, and watches Kubernetes through the
  `kubernetes/` module**, which it names with a `replace` in `go.mod`,
  so it builds against the code in the tree. No component uses
  controller-runtime or kubebuilder. `kubernetes/README.md` names
  each package.
- **`deploy/` is a kustomize base**, with the CRDs, the RBAC, and the
  workloads. A person writes each CRD by hand, except where a
  component's `AGENTS.md` names a generator. The base sets the
  namespace `liken-system` with a `NamespaceTransformer` that is
  `unsetOnly`, and `ci` fails on a top-level `namespace:`.
- **`docs/` is the component's manual**, and `skills/` holds the
  skill that each guide of the manual generates for the component's
  users. `make test-docs` writes the reference pages and the skills
  again and fails when they differ from the commit.
- **`cli/` is the component's `kubectl` plugin**, which
  `Dockerfile.cli` builds.
- **`make test` in the component runs every check that CI runs for
  it**: `gofmt`, `go mod tidy -diff`, `go vet`, the tests with the
  race detector, and the coverage gate of `.testcoverage.yml`. The
  `testing` skill holds the gate's rule.

## Rust components

Each Rust crate, or group of crates that ship in one image, is a
cargo workspace of its own, on edition 2024. Its
`rust-toolchain.toml` names the Rust of the `rust` image of
`toolchains/images.toml`, and `ci pins` fails when it differs.

- **Each dependency names one exact version**, as in `=0.25.1`, and
  `ci pins` fails when two `Cargo.toml` files name different versions
  of one crate. So a bump changes every copy in one commit.
- **A crate links a crate of another component by its relative
  path.** The Dockerfile copies that crate from a build context that
  the component's `package.toml` declares, to the same relative path.
- **Unit tests are in a `#[cfg(test)]` module beside the code.**
- **`make test` runs every check that CI runs**: `cargo fmt --check`,
  `cargo clippy --all-targets -- -D warnings`, and the tests under
  `cargo llvm-cov` with a floor on line coverage.

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
pre-release, with no soak time. A pin that must stay back is a hold,
and its row in the report of `make versions` states the reason. A
comment alone does not count.

A build uses only the bytes that a commit named, so a person who
reviews the commit reviews what every later build downloads. The
digest of every file that a pin downloads is in the repository, and
no build step reads a digest from the network.
`cd ci && go run . pins -root ..` fails on a pin or a download that
breaks a rule, and prek runs it on each commit that changes one.

Load the `pins` skill under `.agents/skills` before you add or change
a `VERSION`, a `fetch.sh`, a `latest.sh`, a `pin.toml`, an image of
`toolchains/images.toml`, or a download in a Dockerfile. Load the
`bump-components` skill to move the pins to newer releases. Root plan
83 holds the reasons.

## Images

Every image builds through `docker-bake.hcl`, on a workstation and in
CI, as the target of the same name:
`docker buildx bake media-operator-player --load` at the top of the
repository. syft and Trivy list what an image holds from records
inside it, so every Dockerfile builds a Go program, a Rust binary, a
closure of Debian or Ubuntu packages, and a static program in one
form each, and `ci pins` fails on a Dockerfile that breaks a form.
Load the `images` skill before you write or change a Dockerfile, a
`FROM` line, a closure script, or a buildroot recipe under
`liken/userland/`.

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

Every image and every OS release that CI publishes carries an SBOM
and a keyless cosign signature of the workflow that built it. Load
the `releases` skill before you tag, publish an image, or pin a
development build. Load the `sbom-and-scans` skill before you verify
a published image or a release file, or change an SBOM, a signature,
or a vulnerability scan.
