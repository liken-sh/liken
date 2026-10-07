---
title: A copy on each node
weight: 10
---

# A copy on each node

A volume of this driver is a directory on each node, not one shared
directory. A pod gets the copy on the node where it runs. The copy
stays on that node when the pod leaves, and a pod that comes back to
that node finds it again. A pod on a different node gets that node's
copy, which starts empty. The driver never copies data between nodes.

So the driver fits a workload that can fill an empty copy by itself:

- **A replicated store.** Several pods each hold a full copy of one
  data set, and keep the copies in agreement over the network. A new,
  empty copy catches up from the others.
- **A cache.** Pods fill the directory with items they can make
  again, such as decoded art or downloaded files. A copy that lacks an
  item makes it.

For any other workload, a file that one pod writes is missing when the
pod moves to another node.

The driver has no controller and provisions nothing. You write a
`PersistentVolume` that names the driver and the volume's handle, and a
`PersistentVolumeClaim` that binds to it. Use the access mode
`ReadWriteMany` for a volume that pods on many nodes mount, because
each node writes only its own copy, or `ReadWriteOncePod` for a volume
that one pod at a time mounts. The driver allows one pod on each node
to mount a volume. Each copy is kept under
`/var/lib/liken/pod-storage`, on the partition that the node's
`Machine` declares for pod storage. A machine that declares no such
partition keeps it in memory, so the copy is lost when the machine
reboots.
