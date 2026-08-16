---
title: How liken works
weight: 10
---

# How liken works

This page gives the model in one read: what a machine runs, how a
fleet changes, and where your part of the system lives. The guides
assume this model.

## One image, plus your layer

Every machine runs the same operating system image. The image is a
read-only squashfs file, built by the project and published in every
release. Nothing on a machine edits it.

Everything that makes a cluster yours lives in one small archive: the
deployment layer. It holds your Cluster document, your Machine
manifests, and the cluster's identity, which is its certificate
authorities and its join token.
[`liken layer`](/docs/reference/cli/#liken-layer) packs the archive,
and every boot loads the image and your layer together.

A machine has no package manager and no configuration files to edit.
What a machine runs is the image plus the layer, and both are
declared.

## Two boot slots

Each machine keeps two copies of the operating system, in slots named
A and B. The machine runs from one slot and downloads upgrades into
the other. An upgrade reboots into the new slot one time, as a trial.
A trial that fails, in any way, ends with the machine back on the
slot it already proved. [Roll back](/docs/guides/rollback/) describes
the fallback paths.

An installation writes slot A. The first upgrade fills slot B, and
after that the slots alternate.

## Two resources drive everything

A `liken` cluster is an ordinary Kubernetes cluster with two extra
resources:

* A [Cluster](/docs/reference/cluster/) declares the fleet in one
  document: the release that every machine runs, the network, and
  the settings that the machines share.
* A [Machine](/docs/reference/machine/) declares one machine: its
  disks, its network ports, and its kernel modules. The machine
  reports what it observes in the resource's `status`.

You operate the fleet when you edit these two resources with
`kubectl`. A machine has no shell and no SSH server. Each machine
reads the two documents and converges to them.

A change applies with the smallest disruption that it needs. Some
values apply live, within seconds. Some restart k3s in place, and
the pods stay up. Some wait for the machine's next boot, whatever
causes it. Some need a reboot of their own. A machine that needs a
disruption stages the change, reports it in the Machine's
`status.pending`, and waits for its turn under the cluster's
disruption budget.

A machine that agrees with every document still reboots for one
reason: because you asked it to.
[`liken request-reboot`](/docs/reference/cli/#liken-request-reboot)
is that request, for a driver that bound the wrong device or a
machine you are experimenting on. It takes the same turn, the same
cordon, and the same drain as every other reboot.

## Releases and the channel

A release is a set of files with a version such as `2026.07.20-001`.
Releases are published on
[the release channel](/docs/reference/release-channel/), a directory
that any web server can share. Your Cluster names the channel, pins
each release by the digest of its release document, and points
`spec.version` at the release to run. Each machine downloads from
the channel, verifies every byte against the pinned digest, and
takes its turn to reboot. [Upgrade the
fleet](/docs/guides/upgrade/) is one edit to the Cluster.

## Where to go next

[Install a cluster](/docs/guides/install/) gives the steps from a
downloaded release to `kubectl get nodes`. When something does not
go to plan, [Troubleshoot](/docs/guides/troubleshoot/) maps each
symptom to the field that explains it.
