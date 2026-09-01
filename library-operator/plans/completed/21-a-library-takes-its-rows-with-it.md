# A Library takes its rows with it

Plan 21. A finalizer holds a deleted `Library` until a cleanup pod
sweeps its rows out of the catalog, so a library's rows never outlive
it.

## The problem

Nothing cleans a deleted `Library`'s rows out of the catalog. Each
scanner prunes only its own library, so a `Library`'s rows lose their
only sweeper when the `Library` goes. The rows were gossiped to every
agent in the namespace, so every surviving library's catalog copy
carries the dead library's items, files, links, and aliases forever,
and replicates them to every future agent. Only the last `Library` in
a namespace takes the whole catalog with it, because every claim goes
with its `Library`.

Plan 20 makes the fix safe: with library-scoped keys, a sweep of
`library = ?` across the six tables is surgical, and a library-scoped
delete replicates cluster-wide from any one agent.

## The shape

A finalizer on the `Library`, and a cleanup pod:

1. The operator puts a finalizer on every `Library` it manages.
2. When a `Library` carries a deletion timestamp, the operator stops
   its scanner pod first, so nothing rewrites the rows while they are
   swept.
3. The operator then launches a cleanup pod: the scanner image in a
   cleanup role beside the same Corrosion sidecar, joined to the
   namespace cluster. The cleanup scours the catalog of every row the
   departed library holds, through its own loopback agent, and
   replication carries the deletes to every peer. The Corrosion API is
   loopback-only inside a pod, which is why a pod does this work and
   the operator cannot.
4. When the cleanup pod succeeds, the operator removes the finalizer,
   and the claim and the rest follow the owner references out.

## What the build chose

The build answered three of the four questions this plan left open,
and the commit messages record the details.

- The escape hatch: there is no timeout. The operator reports any
  blocker in the `Departing` condition and releases the finalizer
  only when every surviving scanner is online and none still names
  the departed library.
- The cleanup pod's contract: it runs on the library's own catalog
  claim, or on a fresh one when that claim is already gone, and it
  deletes the six tables' rows in key batches of 500, because a
  whole-table delete wedged a harness peer. The operator reads
  success through the survivor reports, not through the pod alone.
- The last-library case: the drill found that the last scanner's
  retained Last Will republished onto a topic the release had
  cleared. Every pass now clears the retained topics of any library
  the `Library` collection does not hold.
- Whether the same sweep serves a `Library` whose `spec` moves to a
  different volume stays open.
