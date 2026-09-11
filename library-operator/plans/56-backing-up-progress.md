# Backing up progress

Plan 56. A stub from a 2026-09-11 conversation. The progress store is
the one thing in the namespace that no scan rebuilds. Every screen
holds a copy and the progress pod holds the durable one, so a lost
claim costs nothing while one agent is up. A cluster that loses every
copy at once, or a person who wants the history off the cluster, has
no path today.

## The problem

Getting the file out looks easy. A pod with a `Corrosion` sidecar
joins the progress cluster, syncs, and `corrosion backup <path>`
writes a `VACUUM INTO` copy with the per-actor tables cleared, ready
for `corrosion restore`. The hard question is when that copy is
current. A fresh agent's first version arrives late (see the [open
problem](open-problems/a-fresh-agents-first-version-arrives-late.md)),
sync fills gaps in no fixed order, and nothing in the file says "every
row the cluster holds is here". A backup taken a minute after the
sidecar starts can be missing a week.

The same question stands for a restore. A restored file with a stale
site id or a truncated clock table would gossip its state back into a
live cluster, and the CRDT merge would take it as new.

## What has to be decided

- **Where the copy comes from.** The progress pod's claim is the copy
  of record, and a `corrosion backup` run inside that pod against its
  own file skips the sync question entirely. A separate pod that syncs
  first answers the off-cluster case but reopens the question.
- **What "synced" means.** `corrosion-admin sync generate` shows the
  versions an agent still needs from each peer, and the bookie can
  answer whether the agent has a gap. The plan states a test a backup
  passes before it counts, and where the test's answer lands.
- **Built-in or a recipe.** A `spec.progress.backup` block with a
  schedule and a claim or an S3 destination, run by a `CronJob` the
  operator owns, against a documented recipe an operator runs with
  `kubectl` and a `Job`. The built-in path can state the sync test
  once and put the answer in `Catalog.status`; the recipe leaves both
  to the person.
- **Restore.** Whether the operator offers one at all, and the order
  it runs in: every agent down, the progress pod's file replaced,
  the screens' copies dropped so they sync from the restored one.
- **What a backup holds.** The three progress tables and the clock
  rows they need, and nothing of the catalog, which a rescan rebuilds.

Until this plan is built, the durable copy is the progress pod's claim
and the screens' copies are the redundancy.
