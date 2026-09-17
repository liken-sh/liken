---
title: A named cache
weight: 30
description: "Give pods a named per-node cache directory that a pod on a new node fills again. Use when a workload makes items it can make again, such as decoded art or fetched files."
---

A named cache is a directory that one or more pods fill with items they
can make again, such as decoded art or fetched files. When a copy lacks
an item, the pod makes it again. A pod that moves to a new node starts
with an empty cache and warms it. A pod that comes back to a node it
ran on before has the items it made there.

The objects are the same two as a
[replicated store](../replicated-store/): a `PersistentVolume` that
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
[gauge](../../reference/metrics/) reports each copy's bytes, so a person
can watch a cache grow.
