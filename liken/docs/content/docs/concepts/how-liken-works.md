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

A `liken` cluster is an ordinary Kubernetes cluster with extra
resources. Two of them describe the machines:

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
* Some take effect at the machine's next boot, whatever causes it.
* Some, such as a change to storage or the network, need a reboot of
  their own.

A machine never reboots on its own schedule. When a change needs a
reboot, the machine stages the change, reports it in the `Machine`'s
`status.pending`, and waits until the cluster gives it a reboot turn.
With the default `rebootPolicy`, `Manual`, it also waits for you to
approve the reboot, with
[`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot). With `Auto`, it
reboots when its turn comes. Either way, it cordons and
drains itself before it goes down.

You can also ask a machine to reboot when nothing has changed, for
example when a driver bound the wrong device, or on a machine you're
experimenting on.
[`liken request-reboot`](/docs/reference/cli/#liken-request-reboot)
makes that request. The reboot then waits for its turn and, under
`Manual`, for your approval, with the same cordon and drain as any
other.

## The reboot turn

A reboot or a restart of k3s takes a machine out of service, so no
machine does either until `cluster-operator` gives it a reboot turn.
The turns keep enough of the fleet in service.

Each request for a turn is a
[`Disruption`](/docs/reference/disruption/). It records the machine,
the requester, the kind (`Reboot` or `Restart`), and the reason.
`machine-operator` keeps one request for each machine, named
`<machine>.machine-operator`. It covers every staged change that waits
for a reboot or a restart, and a demotion. `machine-operator` updates
the request when more changes wait, and deletes it when nothing
waits. A person's request is named `<machine>.reboot-<boot>`, where
`<boot>` identifies the boot that the request asks to end.
`liken request-reboot` creates one, and you can also create one with
`kubectl`. `kubectl get disruptions` lists every request in the
fleet:

    kubectl get disruptions

    NAME                          MACHINE   REQUESTER                   KIND     APPROVED   GRANTED   AGE
    node-3.machine-operator       node-3    liken.sh/machine-operator   Reboot   True       True      4m
    node-4.machine-operator       node-4    liken.sh/machine-operator   Reboot   True       False     4m
    node-5.reboot-77b0c1d9e2a4    node-5    kubernetes-admin            Reboot   False      False     30s

`cluster-operator` reports two conditions on each request. `Approved`
is `True` when the machine's `rebootPolicy` is `Auto`, or when the
request's `spec.approvedHash` equals its `spec.hash` or is a prefix of
it at least 12 characters long. `liken approve-reboot` sets that field
for you. `Granted` is `True`
while the machine holds a turn that covers the request. While the
request waits, the message of `Granted` names what it waits for, for
example `The default budget allows 1 out of service, and node-3 is
out`.

A [`MachineDisruptionBudget`](/docs/reference/machinedisruptionbudget/)
limits how many of the machines it selects can be out of service at
the same time. A machine counts against a budget when it holds a
turn, and also when it is `Lost`, `Degraded`, or unavailable for any
other reason. So a fleet that already has a machine down waits before
it takes another one down. The budget named `default` selects every
machine. When it is missing, `cluster-operator` creates it with a
limit of one machine, and until it exists, the fleet takes one machine
out at a time. Edit the budget to change the fleet's limit:

    kubectl patch mdb default --type=merge -p '{"spec":{"maxUnavailable":2}}'

A budget with a selector gives a group of machines a stricter limit.
The selector reads the labels on each `Machine`, which you set with
`kubectl label machine`. A machine takes a turn only when every budget
that selects it has room. `kubectl get mdb` shows each budget's limit,
how many machines it selects, how many of them are out, and how many
more it allows:

    kubectl get mdb

    NAME      MAX   SELECTED   OUT   ALLOWED   AGE
    default   1     5          1     0         9d

Whatever the budgets allow, only one leader is out of service at a
time, because the datastore needs a majority of the leaders.

When a machine has an approved request and every budget that selects
it has room, `cluster-operator` gives it a turn. When more machines
wait than the budgets allow, the machine with the oldest request goes
first, so a machine that asks again after each turn cannot keep
another machine waiting. Among requests made in the same second, a
follower goes before a leader. `cluster-operator` records the turn
in the `Cluster`'s
[`status.rebootTurns`](/docs/reference/cluster/#statusrebootturns),
with the names of the requests that the turn covers. That entry is
what grants the turn. Before `machine-operator` acts on its machine's
entry, it reads the `Cluster` again from the API server, so an entry
that `cluster-operator` has already removed starts no drain. Then it
cordons and drains the node and reboots. For a restart of k3s,
it drains nothing, because the pods keep running. `cluster-operator`
also writes the `RebootApproved` condition on the `Machine`, because
a `machine-operator` from an earlier release acts only on that
condition. All
of a machine's requests share one turn, one drain, and one reboot. A
request that arrives while the machine holds its turn joins that
turn. While a machine's new release still downloads, `cluster-operator`
delays the machine's turn, so the release shares the reboot too.

A request can pause the turn after the drain with `spec.hold`. A
component sets it when it must change the machine while no workload
runs there, and the change takes effect at the next boot:
`firmware-operator` writes a Secure Boot database update this way.
When the node is drained, `machine-operator` sets the request's
`Held` condition to `True` and waits. The component's pod runs on the
drained node and does its work, and the component removes `spec.hold`
when it has checked the result. Then the machine reboots. A hold has
no deadline: until `spec.hold` is gone, the machine stays drained and
counts against its budgets, so the other turns wait. To let the
reboot go ahead, remove the hold yourself:

    kubectl patch disruption <request> --type=json -p '[{"op":"remove","path":"/spec/hold"}]'

Some work during a hold needs the next boot to start a program other
than the proven slot, the slot that the machine last booted
successfully. A UEFI capsule update is one example: fwupd sets the
firmware's `BootNext` variable to its updater, and the firmware boots
the updater once, applies the capsule, and resets. On every other
reboot, `init` sets `BootNext` to the proven slot, or to a release on
trial. So the component sets `spec.keepBootNext` when it removes the
hold, and on that reboot `init` leaves `BootNext` as the component set
it and starts no trial. A staged release waits for a later reboot.

After the updater resets, the firmware boots the first entry of
`BootOrder`, so `init` keeps the component's `BootNext` only when that
entry is certain to be the proven slot. One of its checks is the
Machine's `status.firmware.bootOrderKept`. It is `true` when the
proven slot led `BootOrder` at power-on, which shows that the firmware
kept the order through the last reset. When a check fails, `init`
prints the reason and sets `BootNext` to the proven slot as on any
other reboot, and the component's next check finds that its work did
not take effect. `init` also keeps the symlink `/run/liken/proven-esp`
pointed at the proven slot, whose ESP the next boot reads, so the
component's pod can write its files there.

The drain moves every pod except DaemonSet pods, static pods, and pods
that tolerate the cordon's taint, `node.kubernetes.io/unschedulable`,
by its key. Such a pod could be scheduled straight back onto the
cordoned node, so evicting it moves nothing. A component that holds a
turn gives this toleration to its own controller and to the pod that
works on the machine, so both keep running on a small cluster where
no other node accepts them. A toleration with no key, which tolerates
every taint, does not count, and that pod still moves.

Only a person approves a request. A service account cannot set or
change `spec.approvedHash`, so a component that files a request can
ask for a turn but cannot approve one on a `Manual` machine.

A request ends when its work is done. `machine-operator` deletes its
own request when nothing waits. It marks a person's request
`Complete` when the machine has booted again, and `cluster-operator`
deletes a complete request one day later.

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
