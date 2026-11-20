# The liken repository

`liken` is one system, and all of it is in this repository. Each
top-level directory is one component, and the `README.md` beside this
file says what each one is for. Before you change a component, read
its `README.md` and its `AGENTS.md`. This file has the rules for
every component, so a component's `AGENTS.md` does not repeat them.
Start a session at the repository root when a change touches more
than one component. The skills for agents that work here are in
`.agents/skills`. A `skills/` directory inside a component is
generated from the component's manual, for the agents of its users.
`corrosion`, `plugins`, `log`, and `liken-dev-cluster` are separate
repositories, and plan 69 in `plans/completed/` says why.

## Use what Linux and Kubernetes already provide

`liken` aims to be the most Linux-native and Kubernetes-native
operating system there is. That goal decides most design questions.
When you are in doubt about how to build something, ask what the most
native Linux or Kubernetes approach is, and use it.

Prefer a mechanism of the Linux kernel first, then a mechanism of
Kubernetes, and write code of the project's own only when neither one
does the job. For example, a pod gets a device through a Dynamic
Resource Allocation claim, and not through an API that `liken`
invents. Kubernetes runs each service, so the OS has no service
manager of its own. Do not work against something that Kubernetes
owns, such as scheduling or the life of a pod. If an upstream fault
forces a workaround, put a comment at the workaround that names the
upstream issue.

## Voice

Everything the project publishes follows `brand/voice.md`, including
the comments in source files, the plans, and the commit messages.

@brand/voice.md

## Privacy

This repository is public, and people test against home clusters
that have their real hardware, workloads, and data. Keep those
details out of every comment, plan, document, commit message, and
chat message: hostnames, the names of workloads and services,
personal data, the people or places behind them, and the titles in a
media library. For an example title, invent one or use a work in the
public domain. Describe a home cluster only as much as the point
needs, such as "a home cluster". For detail, use a cluster that the
project ships, such as the `lab` fleet of `liken/dev-cluster/`.

## Keep state current with events

Do not poll. An operator that reads the same state again on a timer
wakes up for nothing, adds load to the API server, and can disturb a
device. One operator scanned a CEC bus every minute, and a TV on that
bus switched its own input every few minutes until the scanning
stopped.

Every component keeps its view of the world current the same way.
First open the watch, subscription, or stream. When it is live, read
the whole state once. An event that arrives during the read is not
lost, because the subscription is already open. When the subscription
fails, open it again and read the whole state again. The pattern is
the same for a Kubernetes watch, `pw-dump -m`, an MQTT subscription,
an inotify watch, and a CEC receive loop. The operators watch
Kubernetes through client-go's reflector, because each guard that a
watch loop needs failed review in a hand-written loop. The
`operators` skill has the guards.

Use a timer as a clock: a time-to-live, a deadline, the wait before a
retry, or the age of a certificate. A timer that reads state again to
look for a change is a defect when the source has an event to
subscribe to. It is correct in two cases, each with a comment. A long
resync can be a backstop for a subscription, and its comment says
which failure it covers. A source can publish no event at all, such
as a file behind an HTTP URL, and its comment says why none exists.

## Conditions, Events, and log lines

A condition says what is true now, for `kubectl wait` and for
controllers. An `Event` says what just happened to an object, for a
person who runs `kubectl describe`, and the API server deletes it an
hour later. A log line records every attempt and every detail. So a
component posts one `Event` for each condition transition, and one
for each action that changes no condition, such as creating a pod
again. It does not post one for a reading, a key press, or each retry
of a loop. A fact that people need for longer than an hour goes in
status. Write `Event`s through `kubernetes/events`, and set conditions
through its `Recorder.SetCondition`, which posts the `Event` for each
transition. Root plan 78 has the reasons.

## Tests run on the Go toolchain alone

CI runs each component's tests on every push, and a test that waits
on the wall clock, pulls an image, or uses the network makes every
push slow and unreliable. So a test waits on the fake clock of a
`synctest` bubble, starts no container and no cluster, and connects
to nothing outside its own process. A test of an operator talks to a
fake API server through `kubernetes/apiservertest`, whose connections
are in memory, because a real socket stops the bubble's clock.
Changes are proved on real machines by hand, as the `testing` skill
describes.

## The manual changes with the code

People install and run each component from its manual alone, so a
manual that is behind the code gives them a wrong command. A commit
that changes what a person installs, runs, reads, or sees also
updates the manual: a manifest, a container, a flag, a metric, a
printer column, a condition, an `Event`, or a behavior that the
component adds on its own. Update another component's pages too when
the change affects it. The `manuals` skill lists the pages.

## Plans

A plan's location shows its status. A file at the top of a `plans/`
directory is not built yet, and a file in `completed/` is built. So
the commit that builds a plan also moves it to `completed/` and adds
the date to its header. Write the plan before the code, except for a
small fix that a test reproduces and that changes no design. The
`plans` skill has the form of a plan and of an open problem.

A component's `AGENTS.md` describes its design, and the reasons are
in its `plans/`. Read the relevant plan before you change how
something works.

## Code layout

Each operator and each CSI driver is one Go module in its own
directory. The program is `package main`, with its files directly in
that directory, one domain to a file, and each file's tests beside
it. There is no `cmd/`, `internal/`, or `pkg/`. A subdirectory is its
own package when another component imports it, or when it is a
protocol or device driver, the Go types of the component's API, or a
second program. A package's fake for other packages' tests goes in
`<package>test/`. `liken/` is one module with a directory for each
program.

An operator uses Kubernetes through the `kubernetes/` module, and no
component uses controller-runtime or kubebuilder. CRDs are written by
hand, unless the component's `AGENTS.md` says that a generator writes
them. The `deploy/` base of an operator or a CSI driver sets its
namespace with a `NamespaceTransformer` in `unsetOnly` mode. A Rust
dependency is
pinned to an exact version, as in `=0.25.1`, and `ci pins` fails when
two Go modules or two crates use different versions of the same
dependency. In each component whose `Makefile` has a `test` target,
`make test` runs the checks that CI runs. A component's
`package.toml` names the command of each CI job.

## Branches and landing

Work goes straight to `main`, with no pull requests. Other sessions
use the same checkout, so work in your own worktree:

```sh
git fetch
git worktree add .worktrees/<slug> -b <slug> origin/main
```

To land the work, rebase the branch onto `origin/main` and push
`HEAD:main`. If the rebase brought in changes to the component you
changed, run its tests again first. Then remove the worktree, and
delete the branch with `git branch -D`, because the local `main` is
behind and `-d` refuses. If the maintainer wants to review first, do
not push. The `commit-messages` skill has the form of a commit. The
`corrosion` repository does its own work on its `liken` branch.

## Pins, images, and releases

`liken` uses the newest release of everything it builds with and
ships, because a pin on an old release line misses fixes that
upstream already made. When the base images stayed on Debian stable,
Weston fell two major versions behind and drew the wrong colors. A
pin that has to stay on an older version is a hold, and its row in
the `make versions` report gives the reason. A comment alone is not
enough.

A build downloads only what a commit specified. The digest of every
downloaded file is in the repository, so a person who reviews a
commit has reviewed what every later build downloads.
`cd ci && go run . pins -root ..` fails on a pin, a download, or a
Dockerfile that breaks a rule. The `pins` and `images` skills have
the rules, and `bump-components` moves pins to newer releases.

Build every image through `docker-bake.hcl`, from the top of the
repository, as the bake target of the same name:
`docker buildx bake media-operator-player --load`. The base images
are published under fixed tags. CI compares each one's published
build with a hash of its build inputs, which include files from other
directories such as `toolchains/`. If you change any of those files,
even a comment, CI fails until you raise the image's revision.
`cd ci && go run . bases -root ..` shows whether an image's inputs
changed.

One CalVer tag releases every component whose outputs changed since
that component's last release, and a push to `main` publishes a
development build of each one that changed. `ci/` generates the
workflows and `docker-bake.hcl` from each `package.toml` and
Dockerfile. Run `make workflows` after you change a `package.toml` or
a `FROM` line, because CI fails when the generated files are out of
date. The `releases` and `sbom-and-scans` skills have the detail.
