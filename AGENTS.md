# The liken repository

`liken` is one system, and all of it is in this repository. Each
top-level directory is one component, named for what it ships. The
`README.md` beside this file lists the components and says what each
one is for.

Each component has two files to read before you change it. Its
`README.md` says what the component is for, what it is responsible
for, how it fits with the other components, and what its main parts
are. Its `AGENTS.md` says how the component runs and why it is
designed that way. This file has the rules that apply to every
component, so a component's `AGENTS.md` does not repeat them.

Plans that cover more than one component are in `plans/`. Each
component has its own `plans/` for work inside it, and the plans of
the OS are in `liken/plans/`.

Four repositories are outside this one: `corrosion`, `plugins`,
`log`, and `liken-dev-cluster`. Plan 69 in `plans/completed/` says
why.

## Work across components

Start the session at the repository root when a change touches more
than one component, when a task names more than one, or when you are
reasoning about the project as a whole. From the root you can see the
whole tree and make each change in the component that owns it. A
session that starts inside a component loads that component's
`AGENTS.md` and this file.

The skills for agents that work on this repository are in
`.agents/skills`. A `skills/` directory inside a component is
different: those skills are generated from the component's manual,
for the agents of the people who use the component.

## Voice

Everything the project publishes follows `brand/voice.md`: the sites,
the guides, the reference text, the comments in source files, the
plans, and the commit messages. Read it before you write, and check
your text against it before you publish.

@brand/voice.md

The rules are ASD-STE100, Simplified Technical English, with
additions. Some kinds of text have more rules, in files beside
`brand/voice.md`:

- Read `brand/messages.md` before you write an error, a log line, the
  message of a condition or an `Event`, or a test failure.
- Read `brand/names.md` before you name an identifier, a file, a
  test, or a part of the API.
- Read `brand/instructions.md` before you write an `AGENTS.md` file,
  a skill, or a brief for another agent.
- Load the `commit-messages` skill before you write a commit message
  or the description of a pull request.

## Privacy

This repository is public, and so is everything written into it. Many
people test against their own home clusters, and a home cluster has
that person's real hardware, workloads, services, and data. Keep
those details out of every comment, plan, document, commit message,
change description, and chat message. That includes hostnames, the
names of workloads and services, personal data, the people or places
behind them, and the titles in a media library. For an example title,
invent one or use a work in the public domain.

When a fact comes from a home cluster, describe the cluster only as
much as the point needs: "a home cluster", "a three-node fleet". When
you need detail, use a cluster that the project ships: the
`dev-cluster/` in `liken`, the `lab` fleet of `node-1` through
`node-5`, and the test clusters that a component's own documentation
names.

## Keep state current with events

Do not poll. An operator that reads the same state again on a timer
wakes up for nothing, adds load to the API server, and can disturb a
device. One operator scanned a CEC bus every minute, and a TV on that
bus switched its own input every few minutes until the scanning
stopped.

Every component keeps its view of the world current the same way:

1. Open the watch, subscription, or stream first.
2. When it is live, read the whole state once. This is the baseline,
   and the subscription keeps it current from then on. The
   subscription is already open, so an event that arrives during the
   read is not lost.
3. When the subscription fails, open it again and read the whole
   state again.

For Kubernetes, list first, then watch from the `resourceVersion`
that the list returned. The API server then sends every change made
after the list. A watch loop needs three guards:

- It resumes from the last version it saw. It does not list again.
- It lists again only after a 410, and only once.
- It decides how long to wait by how long the server kept the watch
  open.

Each of these guards failed review in a hand-written loop, so the
operators watch through client-go's reflector, which upstream
maintains. Load the `operators` skill before you write or review an
operator's watch, pass, or timer. It has the guards in full and the
scenarios that a watch must pass in a test.

Other sources have no version to resume from, so the subscription
must be open before the read. The same pattern applies to
`pw-dump -m`, to an MQTT subscription with retained messages, to an
inotify watch followed by one look at the directory, and to a CEC
receive loop followed by one scan of the bus.

Use a timer only as a clock: a time-to-live, a deadline, or the age
of a certificate. A timer that reads state again to look for a change
is a defect. If a component needs a long resync as a backstop, put a
comment at the timer that says which failure it covers.

## Conditions, Events, and log lines

Each of the three has a different reader:

- A condition says what is true now. `kubectl wait` and controllers
  read it.
- An `Event` says what just happened to an object. A person who runs
  `kubectl describe` reads it, and the API server deletes it an hour
  after its last write.
- A log line records every attempt and every detail.

So a component posts one `Event` for each condition transition, and
one for each action that changes no condition, such as creating a pod
again or requesting a reboot. It does not post an `Event` for a
reading, a key press, or each retry in a retry loop. A fact that
people need for longer than an hour goes in status.

Write `Event`s through `kubernetes/events`, and declare conditions
with `kubernetes/conditions`. `Recorder.SetCondition` posts the
`Event` for each transition. In a test, read the `Event`s from
`kubernetes/events/eventstest` inside a `synctest` bubble. Root plan
78 has the reasons, and the `operators` skill has the detail.

## Tests run on the Go toolchain alone

CI runs each component's tests on every push. A test that waits on
the wall clock, pulls an image, or uses the network makes every push
slow and unreliable. So:

- A test waits on the fake clock of a `synctest` bubble, not on real
  time.
- A test starts no container and no cluster, and connects to nothing
  outside its own process.
- A test of an operator talks to a fake API server through
  `kubernetes/apiservertest`. Its connections are in memory, so the
  bubble's clock keeps moving. A real socket stops the clock.

Changes are proved on real machines by hand, on the `lab` fleet and
on a physical test cluster. Load the `testing` skill before you write
or review a test. It lists the fake for each peer and device, and it
explains the coverage gate and the common mistakes.

## The manual changes with the code

People install and run each component from its manual alone. If the
manual is behind the code, it gives them a wrong command. So a commit
that changes what a person installs, runs, reads, or sees also
updates the component's manual. That includes a manifest, a
container, a flag, a metric, a printer column, a condition, an
`Event`, and any behavior that the component adds on its own. When
the change affects another component, update that component's pages
too.

Load the `manuals` skill before you write or change a page. It lists
which pages each kind of change affects.

## Plans

A plan's location shows its status. A file at the top of a `plans/`
directory is not built yet. A file in `completed/` is built.

When a commit builds a plan, the same commit moves the plan to
`completed/` and adds the date to its header. A built plan that is
still at the top of `plans/` tells the next session that the work is
not done.

Write the plan before the code. The exception is a small fix that a
test reproduces and that changes no design. Load the `plans` skill
before you write a plan or an open problem, and before the commit
that builds or closes one.

## Go components

Each operator and each CSI driver is one Go module in its own
directory, named `github.com/liken-sh/liken/<directory>`. `ci pins`
fails if a module's Go version differs from the `golang` image in
`toolchains/images.toml`, or if two modules use different versions of
the same dependency.

- **Layout.** The program is `package main`, with its files directly
  in the component's directory, one domain to a file, and each file's
  tests beside it. There is no `cmd/`, `internal/`, or `pkg/`. A
  subdirectory is its own package in these cases: another component
  imports it, it is a protocol or device driver, it has the Go types
  of the component's API, or it is a second program. A package's fake
  for other packages' tests goes in `<package>test/`. `liken/` is
  different: it is one module with a directory for each program.
- **Kubernetes access.** An operator reads, writes, and watches
  Kubernetes through the `kubernetes/` module. Its `go.mod` has a
  `replace` for that module, so it builds against the code in the
  tree. No component uses controller-runtime or kubebuilder.
  `kubernetes/README.md` describes each package.
- **`deploy/`** is a kustomize base with the CRDs, the RBAC, and the
  workloads. CRDs are written by hand, unless the component's
  `AGENTS.md` says that a generator writes them. The base sets the
  namespace to `liken-system` with a `NamespaceTransformer` in
  `unsetOnly` mode. `ci` fails if a base has a top-level
  `namespace:`.
- **`docs/`** is the component's manual. **`skills/`** has one skill
  for each guide in the manual, generated for the component's users.
  `make test-docs` generates the reference pages and the skills
  again, and fails if they differ from what is committed.
- **`cli/`** is the component's `kubectl` plugin. `Dockerfile.cli`
  builds it.
- **`make test`** runs every check that CI runs for the component:
  `gofmt`, `go mod tidy -diff`, `go vet`, the tests with the race
  detector, and the coverage gate in `.testcoverage.yml`. The
  `testing` skill explains the gate.

## Rust components

Each Rust crate is its own cargo workspace, on edition 2024. Crates
that ship in one image share a workspace. The `rust-toolchain.toml`
uses the same Rust version as the `rust` image in
`toolchains/images.toml`, and `ci pins` fails if they differ.

- **Dependencies** are pinned to an exact version, as in `=0.25.1`.
  `ci pins` fails if two `Cargo.toml` files use different versions of
  the same crate, so a bump changes every copy in one commit.
- **A crate from another component** is linked by its relative path.
  The Dockerfile copies that crate to the same relative path, from a
  build context that the component's `package.toml` declares.
- **Unit tests** are in a `#[cfg(test)]` module beside the code.
- **`make test`** runs every check that CI runs: `cargo fmt --check`,
  `cargo clippy --all-targets -- -D warnings`, and the tests under
  `cargo llvm-cov` with a minimum line coverage.

## Branches and landing

Work goes straight to `main`. This repository does not use pull
requests.

Other sessions use the same checkout, so make your changes in your
own worktree, on a branch named for the work:

```sh
git fetch
git worktree add .worktrees/<slug> -b <slug> origin/main
```

To land the work, rebase the branch onto `origin/main` and push
`HEAD:main`. Then remove the worktree and the branch. If the
maintainer wants to review first, commit on the branch and do not
push. Load the `commit-messages` skill before each commit.

`corrosion` is outside this repository. It is a shallow fork of
superfly/corrosion. Its `main` branch mirrors upstream and takes no
commits, and the project's own work is on its `liken` branch. Where a
procedure refers to the main branch, use `liken` for `corrosion`.

## Every pin stays current

`liken` uses the newest release of everything it builds with and
ships. A pin that stays on an old release line misses fixes that
upstream already made. When the base images stayed on Debian stable,
Weston fell two major versions behind and drew the wrong colors.

A pin is current when it matches the newest upstream release that is
not a pre-release. There is no waiting period. If a pin has to stay
on an older version, that is a hold, and its row in the
`make versions` report must give the reason. A comment in the code
is not enough.

A build downloads only what a commit specified. The digest of every
downloaded file is in the repository, and no build step reads a
digest from the network. That way, a person who reviews a commit has
reviewed what every later build downloads.
`cd ci && go run . pins -root ..` fails if a pin or a download breaks
one of these rules, and prek runs it on each commit that changes a
pin.

Load the `pins` skill before you add or change a `VERSION`, a
`fetch.sh`, a `latest.sh`, a `pin.toml`, an image in
`toolchains/images.toml`, or a download in a Dockerfile. Load the
`bump-components` skill to move pins to newer releases. Root plan 83
has the reasons.

## Images

Every image is built through `docker-bake.hcl`, on a workstation and
in CI. The bake target has the same name as the image. From the top
of the repository:

```sh
docker buildx bake media-operator-player --load
```

syft and Trivy find out what is in an image from records inside the
image. So each kind of program has one required way to build it in a
Dockerfile: a Go program, a Rust binary, a closure of Debian or
Ubuntu packages, and a static program. `ci pins` fails on a
Dockerfile that does it another way.

The base images are published under fixed tags. CI compares each
one's published build with a hash of its build inputs, which include
files from other directories such as `toolchains/`. If you change any
of those files, even a comment, CI fails until you raise the image's
revision. `cd ci && go run . bases -root ..` shows whether an image's
inputs changed.

Load the `images` skill before you write or change a Dockerfile, a
`FROM` line, a closure script, or a buildroot recipe under
`liken/userland/`.

## Releases and development builds

One CalVer tag releases every component whose outputs changed since
that component's last release. A push to `main` publishes a
development build of each component that changed.

Each component's `package.toml` lists its dependencies, its checks,
and its outputs. `ci/` generates `.github/workflows/`,
`.github/dependabot.yml`, and `docker-bake.hcl` from those files,
from the `FROM` lines of each Dockerfile, and from the `go.mod` and
`Cargo.lock` files in the tree. Run `make workflows` after you change
a `package.toml` or a `FROM` line. CI fails if the generated files
are out of date.

CI attaches an SBOM to every image and every OS release that it
publishes, and signs each one with cosign, keyless, as the workflow
that built it.

- Load the `releases` skill before you tag, publish an image, or pin
  a development build.
- Load the `sbom-and-scans` skill before you verify a published image
  or a release file, or change an SBOM, a signature, or a
  vulnerability scan.
