---
title: How liken works
weight: 10
---

# How `liken` works

A `liken` machine boots straight into Kubernetes. It runs the Linux
kernel, k3s, and a small `init`. It has no shell, no SSH server, no
package manager, and no configuration files to edit. You
manage the machines the same way you manage your workloads, by
editing Kubernetes resources with `kubectl`.

## The `Cluster` and `Machine` resources

A `liken` cluster is an ordinary Kubernetes cluster with two extra
resources:

* A [`Cluster`](/docs/reference/cluster/) describes the whole fleet:
  the release that every machine runs, the network, and the settings
  that the machines share.
* A [`Machine`](/docs/reference/machine/) describes one machine: its
  disks, its network ports, and its kernel modules. The machine also
  reports what it finds, such as its hardware, in the resource's
  `status`.

Each machine reads these two documents and changes itself to match
them. To change a machine, you edit its `Machine` or the `Cluster`.

## How a change reaches a machine

A machine applies each change with the least disruption that the
change needs:

* Some values, such as `sysctls` and `nodeLabels`, apply within
  seconds.
* Some, such as a cluster feature or the registry settings, restart
  k3s in place, and the pods keep running.
* Some wait for the machine's next boot, whatever the reason for that
  boot.
* Some, such as a change to storage or the network, need a reboot of
  their own.

A machine never reboots on its own schedule. When a change needs a
reboot, the machine stages the change, reports it in the `Machine`'s
`status.pending`, and waits until the cluster's disruption budget
gives it a turn. With the default `rebootPolicy`, `Manual`, it also
waits for you to approve the reboot, which
[`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot) does. With `Auto`, it
takes the reboot when its turn comes. Either way, it cordons and
drains itself before it goes down.

You can also ask a machine to reboot when nothing has changed, for
example when a driver bound the wrong device, or on a machine you're
experimenting on.
[`liken request-reboot`](/docs/reference/cli/#liken-request-reboot)
makes that request. The reboot then waits for its turn and, under
`Manual`, for your approval, with the same cordon and drain as any
other.

## What a machine boots

Every machine boots the same operating system image. The project
builds the image and publishes it in each release. It is a read-only
squashfs file, and nothing on the machine changes it.

Your cluster's own files are in a small archive called the
deployment layer. It holds your `Cluster` document, your `Machine`
manifests, and the cluster's identity: its certificate authorities
and its join token.
[`liken layer`](/docs/reference/cli/#liken-layer) builds the archive,
and each boot loads the image and your layer together. So everything
a machine runs comes from those two declared files.

## Releases and upgrades

A release is a set of files with a version such as `2026.07.20-001`.
The project publishes releases on
[the release channel](/docs/reference/release-channel/), a directory
that any web server can serve. Your `Cluster` names the channel, pins
each release by the digest of its release document, and sets
`spec.version` to the release that the machines run.

To upgrade, you change `spec.version`, as
[Upgrade the fleet](/docs/guides/upgrade/) shows. Each machine
downloads the release from the channel, checks every byte against the
pinned digest, and reboots into it when its turn comes.

Each machine keeps two copies of the operating system, in slots
named A and B, so an upgrade never overwrites the copy that is
running. The install writes slot A, and the first upgrade writes
slot B. After that, each upgrade goes to the slot that is not
running. The machine boots the new slot once, as a trial. If the
trial fails in any way, the machine boots the slot that it last
booted successfully.
[Roll back](/docs/guides/rollback/) describes each way back.

## Next steps

[Install a cluster](/docs/guides/install/) takes you from a
downloaded release to `kubectl get nodes`. When something goes
wrong, [Troubleshoot](/docs/guides/troubleshoot/) maps each symptom
to the status field that explains it.
