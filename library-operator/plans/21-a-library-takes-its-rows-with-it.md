# A Library takes its rows with it

Plan 21, a stub. The shape is chosen; the details wait for a design
pass before the build.

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

## To design before the build

- The escape hatch: what unsticks the finalizer when the cleanup pod
  cannot run or cannot finish (no nodes, no storage, a namespace under
  deletion). A stuck finalizer holds the `Library` forever, and that
  is the classic cost of this pattern.
- The cleanup pod's contract: its claim (fresh, or none and join as a
  memory-backed peer), its bounded lifetime, and how the operator
  reads success.
- The last-library case: when no peer remains to replicate to, the
  sweep has nothing to clean and the finalizer should release at once.
- Whether the same sweep serves a `Library` whose `spec` moves to a
  different volume, which departs the old rows the same way.
