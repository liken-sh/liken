---
title: per-node.liken.sh
---

`per-node-csi-driver` gives a pod a directory on the node it runs on.
The directory stays on that node when the pod leaves, and the same pod
finds it again if it comes back to that node. A pod that lands on a
different node finds an empty directory there. One volume is a set of
copies, one per node, and the driver keeps none of them in agreement.

Two workloads use this driver:

- **A replicated store.** Several pods each hold a full copy of one
  data set and keep the copies in agreement over the network. A fresh
  copy catches up from its peers.
- **A named cache.** Pods fill a directory with items they can make
  again. A copy that lacks an item makes it.

A workload that is neither has no use for this class. A plain file
written on one node is not on the next. The driver does not copy it
between nodes.

The driver name is `per-node.liken.sh`, and the StorageClass is
`per-node`. The driver defines no custom resources and has no
controller. A `PersistentVolume` names the driver and the handle, and a
`PersistentVolumeClaim` binds to it.

Start with the [manual](docs/). The design and the plans are in the
[repository](https://github.com/liken-sh/per-node-csi-driver).
