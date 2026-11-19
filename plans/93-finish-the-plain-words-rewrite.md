# 93, Finish the plain-words rewrite

This plan, written on 2026-11-18, records what the rewrite of the
repository's text left undone. Agents wrote most of the comments,
messages, and manual pages over many sessions, in words that only
those sessions defined: "answers" for returns, "holds", "stands",
"the ask", "desk", "shelf". On 2026-11-18 a workflow of agents
rewrote that text from the code, one component at a time, under the
rules in [`brand/voice.md`](../brand/voice.md),
[`brand/messages.md`](../brand/messages.md), and
[`brand/names.md`](../brand/names.md).

Status on 2026-11-18: paused on purpose. The rewritten components
need a period of real use first, to show whether the new text reads
well and whether a rename or a message broke anything that the tests
do not cover. Nothing below is designed in detail.

## What is done

The rewrite covered the comments, the messages, the private names,
the CRD descriptions, and the manual of these components:
`audio-operator`, `bluetooth-operator`, `equipment-operator`,
`display-operator`, `per-node-csi-driver`, `people-operator`,
`brand`, `firmware-operator`, `kubernetes`, `ci`, `git-csi-driver`,
`media-screen`, `astrophotography-operator`, `observatory-operator`,
`media-operator`, `transect`, and, inside `liken`, `init`,
`machine-operator`, `machine`, and `cluster-operator`.

For the rest of `liken`, the rewrite covered only the text that a
user reads: the manual, the README files, the descriptions of the
`Cluster` and `Disruption` CRDs, and the help, output, errors, and
log lines of each Go package.

The rewrite fixed no defect. Each thing that looked like a defect is
an open problem in the `plans/open-problems/` of its component,
marked as suspected and not reproduced.

## The method

Each step ran as its own agent, and no agent changed a file outside
its component:

1. One agent reads the code and writes each comment or passage again
   from it. It deletes a comment that repeats the code, and it renames
   a private name that only its own files use.
2. A second agent, which does not see the old text, cuts the result
   and checks each statement against the code.
3. A third agent rewrites the errors, the log lines, the messages of
   conditions and `Event`s, and the failures of tests.
4. A cleanup agent makes the renames that cross files, chooses one
   term for each thing, generates the reference pages and skills
   again, and runs the component's checks.
5. A reader with no history grades a sample of files, and an agent
   applies its findings to the whole component.

Two lessons cost a failed check each. An agent that replaces single
words leaves the sentence as hard to read as before, so the first
agent must write from the code and not from the old comment. A
message added to a Rust `assert!` counts as an uncovered line when it
holds an expression, so the Rust tests keep their bare assertions.

The briefs and the workflow script are not in the repository. Moving
them in, with the briefs under `brand/` in place of
`brand/editing-prompt.md`, is part of this plan.

## Components not rewritten

- The comments and the private names of the Go packages of `liken`
  outside `init`, `machine-operator`, `machine`, and
  `cluster-operator`: the CLI, `cluster`, `disks`, `hardware`,
  `image`, `kubernetes`, `logs`, `releases`, and the smaller packages.
- The scripts and build files of `liken`: `dev-cluster`, `userland`,
  `image`, the bootloaders, and the files at the top of the
  directory. They keep their long explanatory comments, and the
  rewrite changes only their words.
- `library-operator`, with its media browser. The browser's Rust
  coverage floor is 97% and counts test code.
- The image components: `indi`, `toolchains`, `astrometry`, `ffmpeg`,
  `mpv`, `notices`, `trust`, `vaapi`, `vulkan`, `weston`, and
  `liken.sh`.
- The files at the top of the repository.

## Text that more than one component shares

No run of one component could change these, because each change
reaches other components:

- The exported names of `kubernetes` that the operators import:
  `informer.Held`, `memo.Versions.Unheld`, `Noted`, `HoldAs`,
  `Refuse`, `Count`, and `Holder`. A rename of `informer.Held`
  reaches 31 files in 12 operators.
- The messages of `kubernetes` that other components quote: the
  recorder's "last write had not returned", the election's "holding
  Lease" and "waiting for Lease", and the conversion's "holds no
  copy".
- The names of `liken/machine` that `init` and `machine-operator`
  use: `AskSupplicant`, `AskSupplicantAssociation`, the `Answer` and
  `Asked` of the fake supplicant, `ApprovalGrants`,
  `RebootRequestNames`, `OffsetBoundOf`, and `OutrunsSlew`. The fact
  path `time/standing` is in the same group, and `init` and the
  operator can run different releases, so a rename of it needs a
  migration.
- The file names `steward.go` and `janitor.go` of
  `liken/cluster-operator`, which `machine-operator` and the manual
  name.
- The public names `Moment` and `pressed` of `media-screen`, and the
  words "shade", "desire", "window", and "step back" in the code that
  uses them.
- The warning about a version mismatch that six CLIs print. One of
  them names the command `kubectl liken plugins sync` and the others
  name `liken plugins sync <dir>`.
- The CRD validation messages and the descriptions of FITS keywords
  that `observatory-operator` and `astrophotography-operator` share.
- The heading "Answers" that `brand`'s API reference generator writes
  on every routes page, and the em-dash that `baseof.html` writes into
  every page title.
- The help text of `liken_build_info` in every component.
- The descriptions in each `package.toml`, which `docker-bake.hcl` is
  generated from, and the wording of `ci/templates`, which
  `.github/` is generated from.
- The comment "no content file answers for" in the docs tests of
  seven components.
- `compDirectiveError`, which every CLI declares and none uses.

## Leftovers in rewritten components

Each one is an open problem of its component:

- [`display-operator`](../display-operator/plans/open-problems/comments-that-the-rewrite-did-not-reach.md):
  comments that the rewrite did not reach.
- [`equipment-operator`](../equipment-operator/plans/open-problems/log-lines-that-join-two-statements.md):
  log lines that join two statements.
- `transect`: exported names and page copy. No document lists them,
  so a survey of the crates' exported names and of the page
  templates is the first step.

## Instructions, plans, and other repositories

- The `AGENTS.md` files and the skills under `.agents/skills` have
  the same private words. The root `AGENTS.md` is also at the 32 KiB
  limit of one of the agents that reads it, so its sections on pins,
  closures, Go programs, static programs, and releases move into
  skills.
- The plans keep their dates, measurements, and reasons, as
  `brand/voice.md` says, so they need a brief that changes words and
  no fact.
- `corrosion`, `plugins`, and `liken-dev-cluster` are outside this
  repository. `corrosion` keeps upstream's style for its messages.

## Decisions put off

- A rename of anything in the API: a kind, a field, a condition type,
  a reason, a metric, or a flag. The figurative names that the rewrite
  met are the reasons `NotPlantedByLiken`, `FluxEngineSeeded`, and
  `FluxDeployKeyMinted`, the name `liken-engine-planter`, and the
  `seed/` directory of `liken/cluster-operator`.
- Which level each log line takes, and whether the components share
  one way to log.
