---
title: per-node-csi-driver
---

# `per-node-csi-driver`

`per-node-csi-driver` gives a pod a directory on the node it runs on,
on a [`liken`](https://liken.sh/docs/) cluster. The directory stays on
that node after the pod leaves, and a pod that comes back to that node
finds it again. A pod on a different node gets an empty directory
there. So one volume is a set of copies, one on each node, and the
driver never copies data between them.

That fits a workload that can fill an empty copy by itself:

- **A replicated store.** Several pods each keep a full copy of one
  data set and keep the copies in agreement over the network. A new
  copy catches up from the others.
- **A cache.** Pods fill a directory with items that they can make
  again, such as decoded art or downloaded files. When a copy lacks an
  item, the pod makes it.

Do not use this driver for any other workload, because a file written
on one node is not there on the next.

Start here:

* [Install the driver](/docs/guides/install/).
* [Run a replicated store](/docs/guides/replicated-store/).
* [Give pods a per-node cache](/docs/guides/cache/).

The driver name is `per-node.liken.sh`, and the `StorageClass` is
`per-node`. The driver defines no custom resources and has no
controller, so it does not create volumes. You write a
`PersistentVolume` that names the driver and the volume's handle, and
a `PersistentVolumeClaim` that binds to it.

`per-node-csi-driver` is one of the extension operators for
[mounting storage](https://liken.sh/docs/concepts/mounting-storage/).
[`library-operator`](https://liken.sh/library/) keeps its replicated
catalog on it.

* [The source](https://github.com/liken-sh/liken/tree/main/per-node-csi-driver)
* [The `liken` manual](https://liken.sh/docs/)
