# Backing up progress

Plan 56. This is a stub from a 2026-09-11 conversation. The progress
store is the only data in the namespace that no scan rebuilds. Every
screen has a copy, and the progress pod has the durable copy. While one
agent is up, a lost claim loses no data. Today there is no way to
recover when a cluster loses every copy at once. There is also no way
for a person to export the history off the cluster.

## The problem

The export itself is simple. A pod with a `Corrosion` sidecar joins
the progress cluster and syncs. Then `corrosion backup <path>` writes a
`VACUUM INTO` copy with the per-actor tables cleared, ready for
`corrosion restore`. The difficult part is knowing when that copy is
current. A fresh agent's first version arrives late (see the [open
problem](open-problems/a-fresh-agents-first-version-arrives-late.md)),
sync fills gaps in no fixed order, and nothing in the file says "every
row the cluster has is here". A backup taken a minute after the
sidecar starts can be missing a week of rows.

Restore has a similar problem. A restored file with a stale site id or
a truncated clock table would gossip its state back into a live
cluster, and the CRDT merge would accept that state as new.

## What has to be decided

- **Where the copy comes from.** The progress pod's claim has the
  authoritative copy. A `corrosion backup` run inside that pod against
  its own file does not depend on sync at all. A separate pod that syncs
  first covers the off-cluster case, but then the backup depends on sync
  again.
- **What "synced" means.** `corrosion-admin sync generate` shows the
  versions an agent still needs from each peer, and the bookie reports
  whether the agent has a gap. The plan must state a test that a backup
  passes before it counts, and where the test's result is recorded.
- **Built-in or a recipe.** One option is a `spec.progress.backup`
  block with a schedule and a claim or an S3 destination, run by a
  `CronJob` that the operator owns. The other option is a documented
  recipe that an operator runs with `kubectl` and a `Job`. The built-in
  option can state the sync test once and put the result in
  `Catalog.status`. The recipe leaves both to the person.
- **Restore.** Whether the operator offers a restore at all, and the
  order of its steps: stop every agent, replace the progress pod's file,
  and delete the screens' copies so they sync from the restored one.
- **What a backup contains.** The three progress tables and the clock
  rows they need. It contains nothing from the catalog, because a rescan
  rebuilds the catalog.

Until this plan is built, the durable copy is on the progress pod's
claim, and the screens' copies are the redundancy.
