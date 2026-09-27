# The liken-sh organization

The `liken` project is one system split across the repositories under
the `liken-sh` organization on GitHub. The OS is `liken`. The operators
claim a machine's hardware and serve its interfaces. The CSI drivers
attach storage, and `brand`, `log`, `corrosion`, and the cluster
repositories support the project. Each repository has its own
`AGENTS.md` for the work inside it.

This repository holds what the repositories share. A session reaches
these rules when it starts at the organization root, the directory that
holds the repositories side by side. For example, the checkouts here
live under `~/src/github.com/liken-sh`.

## Set up a checkout

Clone this repository as `.agents` beside the other repositories, and
point the organization root's `AGENTS.md` at this file:

```sh
cd ~/src/github.com/liken-sh
git clone git@github.com:liken-sh/.agents.git .agents
./.agents/setup.sh
```

The harnesses read every `AGENTS.md` on the path from the repository
they work in up to the filesystem root. They read `AGENTS.md` at the
organization root; they do not look inside `.agents` for it. `setup.sh`
creates that file as a symlink to `.agents/AGENTS.md`. Run it after the
first clone, and again after the organization directory moves. Without
the symlink, the repositories still work, and these rules do not reach
them.

## Work at the organization level

Start the session at the organization root when a change crosses a
repository boundary, when a task names more than one repository, or
when the work is to reason about the project as a whole. From there the
whole tree is visible, and each change can land in the repository that
owns it. A session inside one repository reads that repository's
`AGENTS.md` and the rules here.

The skills under `.agents/skills` load when the session starts at the
organization root. A session inside one repository stops its search at
that repository's own git root, so it does not reach them.

## Voice

Every word the project publishes follows the rules in the `brand`
repository at `brand/voice.md`. The rules cover the sites, the guides,
the reference text, the comments in the source files, the plans, and
the commit messages. Read the file before you write, and check your
text against it before you publish it.

@brand/voice.md

The rules are ASD-STE100, Simplified Technical English, with additions.
Four of them matter most in a cross-repository session: name the
component and the object it acts on, give the reason before the
description, keep the code face on every identifier, and describe the
system as it is now. `liken` names the code, so it takes the code face
too.

## Privacy

The repositories and everything written into them are public. Many
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
clusters named in a repository's own documentation.

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

A watch loop written by hand needs three guards, and each one has
failed in review at least once:

- When a watch closes, open the next one from the last
  `resourceVersion` it delivered, with `allowWatchBookmarks=true` so
  that the version moves while nothing changes. Do not list again.
- List again at once only on a `410 Gone`, as a response or as an
  `ERROR` event, and only once: if the watch that opens from that
  fresh list also gets a 410, wait out the backoff. After any other
  error event, wait out a backoff before the list, or a fault that
  lasts makes a tight list loop. An event whose object does not decode
  counts as an error event. On any error, close the stream at once: a
  server holds a watch open for minutes, and a loop that reads the
  stream to its end loses every event in that time. A 410 that ends a
  watch which ran a second or longer counts as a first 410.
- A watch that closes less than a second after it opened is a
  failure, whatever it delivered, so the backoff applies. A watch
  with no version replays every object first, so "no events" does not
  identify a short watch. A watch that ran for a second or longer
  resets the backoff, even when it ended with an error. Measure a
  watch's life from when the server accepted it, not from when the
  request began: a watch the server never accepted did not run, so a
  slow dial or a slow refusal must not reset the backoff.

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

Every repository works on `main` except `corrosion`. `corrosion` is a
shallow fork of superfly/corrosion: its `main` mirrors upstream and takes
no commits, and its own work is on the `liken` branch. Treat `liken` as
`corrosion`'s main branch in any process that names one.

## Releases and development builds

Every repository versions on the same calendar scheme. The `releases`
skill under `.agents/skills` holds the scheme, the operator release
and development build flow, and the way the `liken` OS publishes a
release. Load it before tagging a repository, publishing an image, or
pinning a development build.
