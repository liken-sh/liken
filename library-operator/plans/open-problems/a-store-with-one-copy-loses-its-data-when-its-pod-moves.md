# A store with one copy loses its data when its pod moves

Open design question, recorded on 2026-11-19. A unit test reproduced
it on 2026-11-19.

A `Catalog` stands one copy of the catalog store and one copy of the
progress store when it asks for no more. On a per-node class, each
node keeps its own directory for the store's claim, so the rows of a
one-copy store are on the node where its pod ran. When that pod goes,
because the kubelet evicts it, a drain moves it, or a person deletes
it, the operator creates the pod again, and the scheduler can put it
on any node. On another node the directory is empty or holds an older
copy, and no peer holds the rows to sync from.

A person then sees the browser show an empty wall or an old one. A
rescan builds the catalog again from the libraries. Nothing builds
the progress store again: each person's place in each film and
episode is gone, and the manual's `catalog.md` says that a rescan
cannot recover progress.

## The reproduction

A test seeded the fake API server with one ended copy of each store,
`house-catalog-0` and `house-progress-0`, as `buildCatalogPod` and
`buildProgressPod` build them for a `Catalog` that asks for the
default number of copies. Each pod ran on `node-1`, which is `Ready`,
and the kubelet had evicted it an hour earlier. The test ran the pass
that stands the pod, `standCatalogPod` and `standProgressPod`, inside a
`synctest` bubble. The pass deleted the ended pod and created it
again.

The fake API server has no scheduler, so the test asserted what the
operator builds: that the spec of the replacement names `node-1`, in
any field. Both cases failed with:

```
the replacement of the one copy house-catalog-0 names no node, want it kept on node-1, where its per-node directory holds the store
the replacement of the one copy house-progress-0 names no node, want it kept on node-1, where its per-node directory holds the store
```

The test is not in the tree, because a failing test cannot land on
`main`.

## The cause

- `defaultStoreReplicas` is 1 (`namespacecatalog.go:125`), and
  `catalogReplicaCount` and `progressReplicaCount`
  (`namespacecatalog.go:130`, `namespacecatalog.go:138`) return it
  when the `Catalog` names no count.
- The only placement rule on a copy is `antiAffinity`
  (`storereplicas.go:59`), which keeps two copies of one store off one
  node. `buildCatalogPod` sets it at `catalogpod.go:69`, and
  `buildProgressPod` at `progresspod.go:100`. Neither sets a node
  affinity or a `nodeName`.
- `buildPerNodeVolume` (`pernodevolume.go:55`) writes a
  `PersistentVolume` with no node affinity, because every node that
  mounts it holds a directory of its own. So the volume does not keep
  the pod on a node either. On a class such as `local-path`, the bound
  claim keeps the pod on its node, and this problem does not occur.
- The heal of a stranded copy (`storeheal.go:120`) deletes the pod
  alone on a per-node class, so the copy starts on another node with a
  fresh directory. With more than one copy, the fresh copy syncs from
  its peers. With one copy, it has no peer.

## The decision

Should the operator keep a one-copy store on the node that holds its
data, or ask for more than one copy?

- **Keep the copy on its node.** When the operator creates a copy
  again, it gives the pod a node affinity for the node of the old
  pod. A drain then leaves the pod `Pending` until the node is back,
  and the store has no reader while it waits. The heal must remove the
  affinity when the node is gone for good, or the copy never starts,
  and then the data is lost all the same. The operator must also read
  the old pod's node before it deletes the pod, and keep that fact in
  the API, because it keeps no state in memory.
- **Make more than one copy the default.** Two copies of each store on
  two nodes survive the move of one pod, because the new copy syncs
  from the other. It costs a second pod and a second directory for each
  store, on every namespace with a `Catalog`, and a one-node cluster
  cannot place the second copy: it stays `Pending`, and
  `status.replicas` of the `Catalog` reports fewer ready copies than it
  asks for.
- **Document it and leave it.** The manual says that a one-copy store
  on a per-node class loses its rows when the pod moves, and tells a
  person to set `spec.storage.replicas` and `spec.progress.replicas`
  to 2 for progress worth keeping. It costs nothing to build, and the
  first person who learns of it learns after the loss.
