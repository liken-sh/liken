---
title: Mounting storage
weight: 40
---

# Mounting storage

`git-csi-driver` mounts a git repository as a volume, and
`per-node-csi-driver` gives a pod a directory on the node it runs on.
Kubernetes has neither kind of volume built in. Both are CSI drivers,
so a pod mounts the volume like any other and sees a plain directory.

For example, Home Assistant can keep its configuration in a git
repository on a writeable volume. When Home Assistant edits its own
files, the driver commits the changes and pushes them with a deploy
key, by default after the volume has had no writes for 30 seconds. If
you delete the claim, a new claim on the same repository starts from
the last push.

## The operators

[`git-csi-driver`](https://liken.sh/git/) has the driver name
`git.liken.sh`. A read-only volume can be inline in a pod spec or
come from a claim. Its `pull` attribute is `never`, `on-demand`, or
an interval. When several pods on one node mount the same
repository, the driver fetches it once for all of them. A writeable
volume commits what the pod writes and pushes it upstream, as its
`VolumeAttributesClass` sets. Several applications can share one
repository, with one directory each, and none of them can read the
files of another.

[`per-node-csi-driver`](https://liken.sh/per-node/) has the driver
name `per-node.liken.sh`. A volume has a separate copy on each node,
and the driver does not synchronize the copies. Use it for a cache
that a pod on a new node fills again, or for a replicated store,
where each pod keeps a full copy and the pods synchronize with each
other over the network. `library-operator` keeps its replicated
catalog on this driver.

## What they depend on

Each driver runs its node plugin as a privileged `DaemonSet`, and the
kubelet connects to the plugin through a socket under
`/var/lib/kubelet/plugins`. Each driver stores its data under
`/var/lib/liken/pod-storage`, on the partition that the `Machine`
declares for pod storage. On a machine with no such partition, that
directory is in the root filesystem in RAM, so the data does not
survive a reboot.

Neither driver claims hardware, so neither needs one of the
operators that publish devices.

## Extension points

* **Webhooks.** `git-csi-driver` has a `Service` that a forge sends a
  request to on each push, and the driver pulls the volumes of the
  repository in the request. It accepts webhooks from GitHub, GitLab,
  Gitea, and Forgejo. The `Service` serves plain HTTP, so the
  `Ingress` that you write must terminate TLS.
* **The commit and push policy.** A `VolumeAttributesClass` sets how long
  the driver waits before it commits, the longest delay before a push,
  the commit author, and the files to ignore. You can change the class
  of a live claim.
* **A pull by hand.** An annotation on the `PersistentVolume` makes
  the driver pull immediately.
* **Credentials.** The `Secret` that you name on the volume holds the
  key or token for a private repository.
