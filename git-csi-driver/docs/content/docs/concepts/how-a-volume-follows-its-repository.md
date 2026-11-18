---
title: How a volume follows its repository
weight: 10
---

# How a volume follows its repository

A git volume is a checkout of one branch or tag of a repository,
mounted into a pod as a plain directory. On a read-only volume, the
driver pulls new commits into the checkout. On a writeable volume, it
also commits what the application writes and pushes it back.

## The objects

The driver defines no custom resources. It uses the standard
Kubernetes storage objects:

* A `PersistentVolume` sets the repository and the ref.
* A `VolumeAttributesClass` sets the commit and push policy of a
  writeable volume: how long the tree must be quiet before a commit,
  the longest wait before a push, the commit author, and the files to
  ignore. You can change a live claim's class.
* A `PersistentVolumeClaim` binds the two, and the pod mounts the
  claim.

A read-only volume needs no class. A pod can also mount one inline,
with only a `csi` block in its spec.

## Read-only volumes

The `pull` attribute of a read-only volume sets when it pulls new
commits: never, only on demand, or at an interval. You demand a pull with an
annotation on the `PersistentVolume`, or a forge demands one on each
push through the driver's webhook. When several pods on one node mount
the same repository, the node plugin fetches it once for all of them.

## Writeable volumes

On a writeable volume, the driver watches the tree for writes. When
the tree has had no writes for the class's `push.quiesce`, the driver
commits the changes and pushes them. If the application never stops
writing for that long, the driver pushes anyway once the oldest
unpushed commit is older than `push.maxLatency`. It also pushes when
the pod stops. When that push fails, the commits stay in the node's
work tree until the volume is staged on the same node again.

When the pod starts, the driver fetches the ref. If upstream has
moved and the volume also has commits of its own, the driver rebases
the volume's commits onto upstream. When the rebase conflicts, the
driver moves the volume to a side branch, so no write is lost. If the
tree has writes that are not committed yet, the driver does not touch
the tree at all.

While the pod runs, the driver changes files in the pod's tree only
after a rejected push. When the forge rejects a push because
upstream moved, the driver rebases and pushes again, and then writes
the files that upstream changed into the tree. It does not overwrite a
file that the application wrote since its last commit.

A new claim on the same repository starts from the last push, which is
how you restore an application's configuration.
