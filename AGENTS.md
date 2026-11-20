# The liken repository

`liken` is one system, and all of it is in this repository. Each
top-level directory is one component, named for what it ships, and
the `README.md` beside this file says what each one is for. Before
you change a component, read its `README.md` and its `AGENTS.md`.
This file has the rules for every component, so a component's
`AGENTS.md` does not repeat them.

Four repositories are outside this one: `corrosion`, `plugins`,
`log`, and `liken-dev-cluster`. Plan 69 in `plans/completed/` says
why.

Start the session at the repository root when a change touches more
than one component, or when you reason about the project as a whole.
The skills for agents that work on this repository are in
`.agents/skills`. A `skills/` directory inside a component is
different: it is generated from the component's manual, for the
agents of the people who use the component.

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

Every component keeps its view of the world current the same way.
First open the watch, subscription, or stream. When it is live, read
the whole state once, as the baseline that the subscription keeps
current. An event that arrives during the read is not lost, because
the subscription is already open. When the subscription fails, open
it again and read the whole state again.

For Kubernetes, list first, then watch from the `resourceVersion`
that the list returned. Each guard that a watch loop needs failed
review in a hand-written loop, so the operators watch through
client-go's reflector, which upstream maintains. The `operators`
skill has the guards and the scenarios that a watch must pass in a
test.

Other sources have no version to resume from, so the subscription
must be open before the read. The same pattern applies to
`pw-dump -m`, to an MQTT subscription with retained messages, to an
inotify watch followed by one look at the directory, and to a CEC
receive loop followed by one scan of the bus.

Use a timer only as a clock: a time-to-live, a deadline, or the age
of a certificate. A timer that reads state again to look for a
change is a defect when the source has an event to subscribe to.

A timer that reads state is correct in two cases, and each needs a
comment at the timer. A long resync can be a backstop for a
subscription, and its comment says which failure it covers. Some
sources publish no event at all, such as a file behind an HTTP URL.
A timer is then the only way to see a change, and its comment says
why no event exists.

## Conditions, Events, and log lines

A condition says what is true now, for `kubectl wait` and for
controllers. An `Event` says what just happened to an object, for a
person who runs `kubectl describe`, and the API server deletes it an
hour after its last write. A log line records every attempt and every
detail.

So a component posts one `Event` for each condition transition, and
one for each action that changes no condition, such as creating a pod
again or requesting a reboot. It does not post an `Event` for a
reading, a key press, or each retry in a retry loop. A fact that
people need for longer than an hour goes in status. Write `Event`s
through `kubernetes/events`, and declare conditions with
`kubernetes/conditions`, whose `Recorder.SetCondition` posts the
`Event` for each transition. Root plan 78 has the reasons.

## Tests run on the Go toolchain alone

CI runs each component's tests on every push. A test that waits on
the wall clock, pulls an image, or uses the network makes every push
slow and unreliable. So a test waits on the fake clock of a
`synctest` bubble. It starts no container and no cluster, and
connects to nothing outside its own process. A test of an operator
talks to a fake API server through `kubernetes/apiservertest`, whose
connections are in memory, because a real socket stops the bubble's
clock. Changes are proved on real machines by hand, on the `lab`
fleet and on a physical test cluster. The `testing` skill lists the
fake for each peer and device.

## The manual changes with the code

People install and run each component from its manual alone. If the
manual is behind the code, it gives them a wrong command. So a commit
that changes what a person installs, runs, reads, or sees also
updates the component's manual. That includes a manifest, a
container, a flag, a metric, a printer column, a condition, an
`Event`, and any behavior that the component adds on its own. When
the change affects another component, update that component's pages
too. The `manuals` skill lists which pages each kind of change
affects.

## Plans

A plan's location shows its status. A file at the top of a `plans/`
directory is not built yet, and a file in `completed/` is built. So
the commit that builds a plan also moves it to `completed/` and adds
the date to its header. Write the plan before the code, except for a
small fix that a test reproduces and that changes no design. The
`plans` skill has the form of a plan and of an open problem.

A component's `AGENTS.md` describes its design in a few paragraphs.
The reasons for each decision are in the component's `plans/`. Read
the relevant plan before you change how something works.

## Code layout

Each operator and each CSI driver is one Go module in its own
directory. The program is `package main`, with its files directly in
the component's directory, one domain to a file, and each file's
tests beside it. There is no `cmd/`, `internal/`, or `pkg/`. A
subdirectory is its own package when another component imports
it, when it is a protocol or device driver, when it has the Go types
of the component's API, or when it is a second program. A package's
fake for other packages' tests goes in `<package>test/`. `liken/` is
different: it is one module with a directory for each program.

An operator reads, writes, and watches Kubernetes through the
`kubernetes/` module. No component uses controller-runtime or
kubebuilder. CRDs are written by hand, unless the component's
`AGENTS.md` says that a generator writes them. A `deploy/` base sets
its namespace with a `NamespaceTransformer` in `unsetOnly` mode, and
`ci` fails on a base with a top-level `namespace:`.

A Rust dependency is pinned to an exact version, as in `=0.25.1`. A
crate from another component is linked by its relative path, and the
Dockerfile copies it to the same relative path from a build context
that `package.toml` declares.

`ci pins` fails when two modules or two crates use different versions
of the same dependency, so a bump changes every copy in one commit.
In each component, `make test` runs the checks that CI runs.

## Branches and landing

Work goes straight to `main`, with no pull requests. Other sessions
use the same checkout, so work in your own worktree:

```sh
git fetch
git worktree add .worktrees/<slug> -b <slug> origin/main
```

To land the work, rebase the branch onto `origin/main` and push
`HEAD:main`. If the rebase brought in changes to the component you
changed, run its tests again before you push. Then remove the
worktree, and delete the branch with `git branch -D`, because the
local `main` is behind and `-d` refuses. If the maintainer wants to
review first, commit on the branch and do not push.

`corrosion` is a shallow fork of superfly/corrosion. Its `main`
branch mirrors upstream and takes no commits, and the project's own
work is on its `liken` branch. Where a procedure refers to the main
branch, use `liken` for `corrosion`.

## Every pin stays current

`liken` uses the newest release of everything it builds with and
ships, because a pin on an old release line misses fixes that
upstream already made. When the base images stayed on Debian stable,
Weston fell two major versions behind and drew the wrong colors. A
pin is current when it matches the newest upstream release that is
not a pre-release, with no waiting period. A pin that has to stay on
an older version is a hold, and its row in the `make versions`
report must give the reason. A comment in the code is not enough.

A build downloads only what a commit specified. The digest of every
downloaded file is in the repository, and no build step reads a
digest from the network. That way, a person who reviews a commit has
reviewed what every later build downloads.
`cd ci && go run . pins -root ..` fails if a pin or a download breaks
one of these rules, and prek runs it on each commit that changes a
pin. The `pins` skill has the rules of a pin, and the
`bump-components` skill moves pins to newer releases. Root plan 83
has the reasons.

## Images

Build every image through `docker-bake.hcl`, as the bake target of
the same name, from the top of the repository:

```sh
docker buildx bake media-operator-player --load
```

syft and Trivy find out what is in an image from records inside the
image. So each kind of program has one required way to build it in a
Dockerfile, which the `images` skill describes. `ci pins` fails on a
Dockerfile that does it another way.

The base images are published under fixed tags. CI compares each
one's published build with a hash of its build inputs, which include
files from other directories such as `toolchains/`. If you change any
of those files, even a comment, CI fails until you raise the image's
revision. `cd ci && go run . bases -root ..` shows whether an image's
inputs changed.

## Releases and development builds

One CalVer tag releases every component whose outputs changed since
that component's last release. A push to `main` publishes a
development build of each component that changed. CI attaches an SBOM
to every image and every OS release that it publishes, and signs each
one with cosign, keyless, as the workflow that built it. The
`releases` and `sbom-and-scans` skills have the detail.

`ci/` generates the workflows and `docker-bake.hcl` from each
component's `package.toml` and Dockerfiles. Run `make workflows`
after you change a `package.toml` or a `FROM` line, because CI fails
when the generated files are out of date.
