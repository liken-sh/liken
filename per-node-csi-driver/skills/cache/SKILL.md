---
name: cache
description: "Give pods a named per-node cache directory that a pod on a new node fills again. Use when a workload makes items it can make again, such as decoded art or fetched files."
---

This skill is the guide at https://liken.sh/per-node/docs/guides/cache/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Give pods a per-node cache

A cache on this driver is a directory that pods fill with items they
can make again, such as decoded art or fetched files. A pod that moves
to a new node starts with an empty cache there and fills it. A pod
that comes back to a node it ran on before finds the items it made
there.

A cache uses the same two objects as a
[replicated store](https://liken.sh/per-node/docs/guides/replicated-store/): a `PersistentVolume` that
names the driver and the handle, and a `PersistentVolumeClaim` that
binds to it. Copy the manifests from that guide and give the volume
its own name. Use the access mode `ReadWriteMany`, so that pods on
any node can mount the cache.

Nothing keeps the copies of a cache in agreement. When an item is
missing on one node, the pod there makes it again. So a cache needs no
anti-affinity. With several pods, each pod fills the copy on its own
node, and pods on the same node share that copy. Two pods that make the same item at the same time must not write
over each other, so write each item to a temporary file and rename it
into place, or take a lock on the item with `flock`.

The driver sets no size limit. A cache grows until the workload trims
it or the filesystem of the store is full. Every copy on the node
shares that filesystem with the node's other volumes, so a cache that
is never trimmed takes space from them. The
[`per_node_csi_copy_bytes` gauge](https://liken.sh/per-node/docs/reference/metrics/) reports the
size of each copy, so you can watch a cache grow.
