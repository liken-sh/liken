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

## Releases and development builds

Every repository versions on the same calendar scheme. The `releases`
skill under `.agents/skills` holds the scheme, the operator release
and development build flow, and the way the `liken` OS publishes a
release. Load it before tagging a repository, publishing an image, or
pinning a development build.
