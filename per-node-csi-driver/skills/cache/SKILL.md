---
name: cache
description: "Give pods a named per-node cache directory that a pod on a new node fills again. Use when a workload makes items it can make again, such as decoded art or fetched files."
---

This skill is the guide at https://per-node.liken.sh/docs/guides/cache/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

A named cache is a directory that one or more pods fill with items they
can make again, such as decoded art or fetched files. A copy that lacks
an item makes it. A pod that lands on a new node starts with an empty
cache and warms it. A pod that comes back to a node it ran on before
finds the items it made there.

The objects are the same two as a
[replicated store](https://per-node.liken.sh/docs/guides/replicated-store/): a `PersistentVolume` that
names the driver and the handle, and a `PersistentVolumeClaim` that
binds to it. Take the manifests from that guide and give the volume a
name of its own. The access mode is `ReadWriteMany`, because many nodes
mount the one volume read-write, and each node holds its own copy.

One difference separates a cache from a replicated store. Nothing keeps
the copies of a cache in agreement, and nothing has to. An item made on
one node is not on the next, and a pod on the next node makes it again.
So a cache needs no anti-affinity when one pod runs at a time, and when
several pods run, each one warms the copy on its own node.

The driver keeps no size budget. A cache grows until the workload trims
it or the filesystem that holds the store fills. Every copy shares that
filesystem with every other volume on the node, so a cache that grows
without a trim takes space from them. The
[gauge](https://per-node.liken.sh/docs/reference/metrics/) reports each copy's bytes, so a person
can watch a cache grow.
