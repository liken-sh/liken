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
repository on a writeable volume. Home Assistant edits its own files,
and after 30 seconds with no writes the driver commits the changes
and pushes them with a deploy key. If you delete the claim, a new
claim on the same repository starts from the last push.

## The operators

[`git-csi-driver`](https://liken.sh/git/) mounts a git repository as a
volume, under the driver name `git.liken.sh`. A read-only volume can
be inline in a pod spec, or a claim, and its `pull` attribute is
`never`, `on-demand`, or an interval. Pods on one node that mount the same repository share
one fetch. A writeable volume commits what the pod writes and pushes it
upstream, by the policy of a `VolumeAttributesClass`. Several
applications can share one repository, with one directory each, and
none of them reads the files of another.

[`per-node-csi-driver`](https://liken.sh/per-node/) gives a pod a
directory on the node it runs on, under the driver name
`per-node.liken.sh`. A volume has one copy on each node, and the
driver does not keep the copies in agreement. Use it for a cache
that a pod on a new node fills again, or for a replicated store, where each pod holds a full copy and the pods keep
the copies in agreement over the network. `library-operator` keeps its
replicated catalog on this driver.

## What they depend on

Each driver runs a node plugin as a privileged `DaemonSet`, which the
kubelet reaches through its plugin directories. Each driver keeps its
data on the partition that a `Machine` declares for pod storage, under
`/var/lib/liken/pod-storage`. A machine with no such partition keeps
that data in its RAM root, so it does not survive a reboot.

Neither driver uses the operators for claiming hardware.

## Extension points

* Webhooks. `git-csi-driver` serves one `Service` that a forge posts
  to on each push, and the driver pulls the volume that the request
  names. GitHub, GitLab, Gitea, and Forgejo work. The
  `Service` speaks plain HTTP, and the `Ingress` that you write
  terminates TLS.
* The commit and push policy. A `VolumeAttributesClass` sets how long
  the driver waits before it commits, the longest delay before a push,
  the commit author, and the files to ignore. You can change the class
  of a live claim.
* A pull by hand. An annotation on the `PersistentVolume` asks the
  driver to pull now.
* Credentials. A `Secret` that you name on the volume holds the key or
  token for a private repository.
