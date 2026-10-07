---
title: How a volume follows its repository
weight: 10
---

# How a volume follows its repository

A git volume is a checkout of one ref of one repository, mounted into
a pod as a plain directory. The driver keeps the checkout and the
repository the same, in one direction or in both.

## The objects

The driver defines no custom resources. It uses the standard CSI
objects:

* A `PersistentVolume` names the repository and the ref.
* A `VolumeAttributesClass` sets the commit and push policy of a
  writeable volume: how long the tree must be quiet before a commit,
  the longest wait before a push, the commit author, and the files to
  ignore. You can change a live claim's class.
* A `PersistentVolumeClaim` binds the two, and the pod mounts the
  claim.

A read-only volume needs no class. A pod can also mount one inline,
with only a `csi` block in its spec.

## Read-only volumes

A read-only volume pulls new commits on the schedule in its `pull`
attribute: never, only when asked, or at an interval. You ask for a
pull with an annotation on the `PersistentVolume`, or a forge asks
for one through the driver's webhook on each push. Pods on one node
that mount the same repository share one fetch.

## Writeable volumes

On a writeable volume, the driver watches the tree. When the
application's writes have been quiet for the class's `push.quiesce`,
the driver commits them, and it pushes them when no new write comes,
when the oldest unpushed commit gets too old, and always when the pod
stops.

The driver takes changes from upstream when the pod starts. If
upstream moved while the volume also has commits of its own, the
driver rebases the volume's commits onto upstream, and a rebase that
conflicts moves the volume to a side branch instead, so no write is
lost. A tree with writes that no commit holds yet stays as it is,
whatever upstream did. While the pod runs, the files change under the
application in one case only: when the forge rejects a push because
upstream moved, the driver rebases and pushes again, and the tree
takes the files that upstream changed, unless the application wrote
the same file since its last commit. A new claim on
the same repository starts from the last push, which is how you
restore an application's configuration.
