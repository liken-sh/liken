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

* A `PersistentVolume` sets the repository and the ref, and the
  `paths` to check out when the volume needs only some directories or
  files of the repository.
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

### What a new commit writes

When the ref moves, the driver compares the commit that the tree is
at with the new commit, with `git diff-tree`, and writes only the
files that differ. It checks out each changed file beside the tree and
moves it into place with one rename, so a reader sees the old file or
the new one. It deletes each file that the new commit removed, and
then each directory that those deletes left empty. A file that did not
change keeps its inode, so a pod that watches its files with inotify
sees an event only for a file that changed. A volume with `paths`
compares only its entries, so a commit that changes nothing inside
them writes no file.

The driver checks out the whole tree, or every entry of `paths`, again
in these cases:

* The tree has no recorded commit. The driver deletes the record of
  the commit before it changes the tree and writes it again after, so
  a driver that stops in the middle of an update leaves no record.
* The node's repository no longer has the old commit, for example
  after a forced push and a later cleaning of the node's store.
* A `.gitattributes` file changed in a directory that contains an
  entry, or inside an entry. A rule in that file, such as
  `* text eol=crlf`, can change the bytes of a file that did not
  change in git. For a volume with no `paths`, any `.gitattributes`
  file counts.

A full checkout keeps each directory that both commits have, so a
`subPath` mount of it keeps working. It moves every file into the tree
with a rename, so each file gets a new inode.

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

A writeable volume with `paths` uses git's sparse checkout, so its
work tree has only the files of its entries. The driver commits only
changes inside the entries, and a file that the pod writes outside
them stays on the node. So several applications can each write their
own directory of one repository, as [Give many applications one
repository](../../guides/one-repository-many-apps/) shows.

A new claim on the same repository starts from the last push, which is
how you restore an application's configuration.
