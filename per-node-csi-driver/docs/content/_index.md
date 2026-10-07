---
title: per-node-csi-driver
---

# `per-node-csi-driver`

`per-node-csi-driver` gives a pod a directory on the node it runs on,
on a [`liken`](https://liken.sh/docs/) cluster. The directory stays on
that node when the pod leaves, and a pod that comes back to that node
finds it again. A pod on a different node finds an empty directory
there. So one volume is a set of copies, one on each node, and the
driver never copies data between them.

That fits two kinds of workload:

- **A replicated store.** Several pods each hold a full copy of one
  data set and keep the copies in agreement over the network. A new
  copy catches up from the others.
- **A cache.** Pods fill a directory with items that they can make
  again, such as decoded art or downloaded files. A copy that lacks an
  item makes it.

Any other workload shouldn't use this driver, because a file written
on one node isn't there on the next.

Start here:

* [Install the driver](/docs/guides/install/).
* [Run a replicated store](/docs/guides/replicated-store/).
* [Give pods a per-node cache](/docs/guides/cache/).

The driver name is `per-node.liken.sh`, and the `StorageClass` is
`per-node`. The driver defines no custom resources and has no
controller. You write a `PersistentVolume` that names the driver and
the volume's handle, and a `PersistentVolumeClaim` that binds to it.

`per-node-csi-driver` is one of the extension operators for
[mounting storage](https://liken.sh/docs/concepts/mounting-storage/).
[`library-operator`](https://liken.sh/library/) keeps its replicated
catalog on it.

* [The source](https://github.com/liken-sh/liken/tree/main/per-node-csi-driver)
* [The `liken` manual](https://liken.sh/docs/)
