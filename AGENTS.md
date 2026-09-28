# The liken repository

The `liken` project is one system, and this repository holds all of
it. Each top-level directory is one component, named for what it
ships. `liken/` is the OS. The operators claim a machine's hardware
and serve its interfaces. The CSI drivers attach storage, and `brand/`
holds the theme, the voice rules, and the site tools. `kubernetes/`
is the Go module that the operators import to read, write, and watch
Kubernetes objects. `ci/` reads each
component's `package.toml` and writes the CI workflows. `liken.sh/`
declares the domain, the release channel, and the organization's
repositories in Terraform. `plans/` holds the plans that cover more
than one component, and each component keeps its own plans in its own
`plans/`. Each component has its own
`AGENTS.md` for the work inside it.

Four repositories stay outside: `corrosion`, `plugins`, `log`, and
`liken-dev-cluster`. Plan 69 in `plans/` gives the reasons.

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

## Branch names

This repository works on `main`. `corrosion`, outside this repository,
is a shallow fork of superfly/corrosion: its `main` mirrors upstream and
takes no commits, and its own work is on the `liken` branch. Treat
`liken` as `corrosion`'s main branch in any process that names one.

## Releases and development builds

One CalVer tag releases every component whose outputs changed since its
own previous release, and a push to `main` publishes a development
build of each component that changed. Each component's `package.toml`
names its dependencies, its checks, and its outputs, and `ci/` writes
`.github/workflows/` from those files. Run `make workflows` after you
change a `package.toml`; CI fails when the workflows differ from what
the generator writes.

The `releases` skill under `.agents/skills` holds the calendar scheme,
what a tag releases, and how to pin a development build. Load it before
tagging, publishing an image, or pinning a development build.
