---
name: remove-nodes
description: "Remove one machine from a running liken cluster: demote it first if it is a leader, drain it, power it off, delete its Node and Machine, and rebuild the install stick without it. Use when a node leaves the cluster for good, including a machine whose hardware is already gone."
---

This skill is the guide at https://liken.sh/docs/guides/remove-nodes/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Remove machines from a cluster

You remove one machine at a time. If the machine is a leader, make it
a follower first, so that it leaves the datastore while it still runs.
Then move its workloads off, power it off, and delete its objects from
the cluster. To replace a machine's disks or install it again under
the same name, do not remove it.
[Install a cluster](https://liken.sh/docs/guides/install/#to-reinstall-a-machine-that-liken-installed)
gives those steps.

If the machine is already gone, for example because its hardware
failed, go to [A machine that is already gone](#a-machine-that-is-already-gone).

## 1. If the machine is a leader

A leader runs a control plane and holds one member of the cluster's
datastore. Remove its name from
[`spec.leaders`](https://liken.sh/docs/reference/cluster/#spec--leaders), and the
machine becomes a follower. Its datastore member leaves the cluster
while the other leaders keep the majority.

Keep an odd number of leaders, so that the datastore can always make
a majority. Two leaders tolerate no failure: while one of them
reboots, the cluster's API does not answer. To retire one of three
leaders, first promote a follower in its place, as in
[Add machines](https://liken.sh/docs/guides/add-nodes/#2-if-the-new-machine-is-a-leader),
and wait until the `ClusterConverged` condition of every machine is
`True`. Then remove the old leader's name.

### Let the machine finish its own demotion

The machine boots as a follower, but its Node object still claims the
control plane, and its datastore member is still registered. A member
that does not vote still counts toward the majority, so the next leader
reboot can lose the majority. The machine's operator fixes this. It
deletes the machine's own Node, which makes k3s remove the datastore
member, and it reboots once more to register as a follower.

The operator does this only when the machine's
[`spec.rebootPolicy`](https://liken.sh/docs/reference/machine/#spec--rebootpolicy) is
`Auto`. Under `Manual`, the machine reports `NodeCurrent: False` with
the reason `DemotionPending`, and it waits. Set the policy before you
edit the leaders:

    kubectl patch machine <name> --type merge -p '{"spec":{"rebootPolicy":"Auto"}}'

### Edit the leaders

Remove the machine's name from `spec.leaders` in two places:

* On the live cluster, with `kubectl edit cluster`. The machines read
  this document, and each machine gets its role from it.
* In your `mycluster/cluster.yaml`. The file applies only to a new
  cluster. But if you keep it correct, a new build gives the cluster
  that you run.

If [`spec.endpoint`](https://liken.sh/docs/reference/cluster/#spec--endpoint) gives
this machine's address, change it to another leader's address in the
same edit. A new follower joins the cluster through the endpoint, and
the machine you remove will not answer there.

The first name in `spec.leaders` is the founding leader. Every other
leader joins the datastore at the founder's address when it boots. If
you remove the first name, the next name becomes the founder. k3s
ignores `cluster-init` on a machine whose datastore already exists, so
the new founder keeps its data.

A change to `spec.leaders` reboots every machine, one at a time,
because each machine reads the leaders only when it boots. The leaders
give each follower its time sources, and the founder's address gives
each leader its join URL. Under `Manual`, each machine reports
`RebootPending`, and its request reads `False` in the APPROVED column
of `kubectl get disruptions`. Approve each one with
[`liken approve-reboot`](https://liken.sh/docs/reference/cli/#liken-approve-reboot), as
in [Upgrade the fleet](https://liken.sh/docs/guides/upgrade/).

### Watch the demotion

    kubectl get machine <name> -o jsonpath='{.status.conditions[?(@.type=="NodeCurrent")]}'

The machine reboots as a follower and reports `AwaitingTurn` until the
cluster gives it a reboot turn. The demotion is part of the request
`<name>.machine-operator`, which `kubectl get disruptions` lists. Then it reports `DemotionRebooting`,
deletes its Node, and reboots. When it returns, the condition is
`True` with the reason `NodeMatchesRole`, and `kubectl get nodes` shows
the machine with no `control-plane` or `etcd` role. Continue from step
2 when the `ClusterConverged` condition of every machine is `True`.

## 2. Move the workloads off

    kubectl drain <name> --ignore-daemonsets --delete-emptydir-data

The drain cordons the node and evicts its pods, so their controllers
start them on other nodes. The drain leaves DaemonSet pods, which
include the OS workloads, until the machine powers off.

Some data stays on the node's own disk. The default `StorageClass`
keeps each volume in this node's pod storage, and the volume binds its
pod to this node. Such a pod does not start on another node after the
drain. Copy the data off, or accept its loss, before you continue.

## 3. Power the machine off

Power the machine off at its switch. About 40 seconds later, `kubectl
get machines` shows it `Lost`, and the Cluster reads `Degraded`.

The machine's disks still hold its copy of the cluster's join token
and certificates. Erase the disks before you use the hardware outside
this cluster.

## 4. Delete the Node and the Machine

    kubectl delete node <name>
    kubectl delete machine <name>

The cluster keeps both objects until you delete them, and no other
machine creates them again. Delete them, because a `Lost` machine
counts against every
[`MachineDisruptionBudget`](https://liken.sh/docs/reference/machinedisruptionbudget/)
that selects it. With the `default` budget of one, no other machine
gets a reboot turn, and upgrades and Cluster edits stop. Each
`Disruption` names its `Machine` as its owner, so Kubernetes deletes
the machine's requests with it. `machine-operator` and
`liken request-reboot` set the owner when they file a request, and
`machine-operator` adds it to a request created with `kubectl`.

Power off before you delete. A machine that still runs creates its
Machine again when its operator starts, and its kubelet registers the
Node again at its next boot.

If you run the fleet from git, with the `flux` feature, also delete
the machine's file from the repository. The Machine documents carry
`kustomize.toolkit.fluxcd.io/prune: disabled`, so Flux does not delete
the object when the file goes. While the file stays, Flux applies the
Machine again.

## 5. Rebuild the install stick

Delete the machine's file from `mycluster/machines/`. While the file
stays, `liken layer` packs it, and the stick's menu lists `install as
<name>` and `wipe and reinstall as <name>` for a machine that is gone.
Use the release that your fleet runs. `kubectl get clusters` shows it
in the VERSION column. Pack the layer and the stick again with
[`liken layer`](https://liken.sh/docs/reference/cli/#liken-layer) and
[`liken stick`](https://liken.sh/docs/reference/cli/#liken-stick):

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick channel/<version> mycluster/deployment.cpio mycluster/install.img

Check the device name before the next command. The command
overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

The machines that run keep the layer they were installed with, so
their copy still holds the old file. That copy has no effect, because
no `spec.leaders` entry names the machine.

## 6. Watch it leave

    kubectl get machines
    kubectl get clusters

The machine is gone from both `kubectl get machines` and `kubectl get
nodes`. The Cluster returns to `Ready` when every machine that remains
is ready.

## A machine that is already gone

If the machine cannot boot, it cannot demote itself or drain its pods.
Delete its objects first:

    kubectl delete node <name>
    kubectl delete machine <name>

For a leader, the deletion of the Node also removes its datastore
member. Do this before any other Cluster edit. The `Lost` machine
counts against the `default` budget. With the limit of one, an edit
to `spec.leaders` does not roll while the Machine exists.

Then, for a leader, remove its name from `spec.leaders` as in step 1,
both on the live cluster and in `mycluster/cluster.yaml`, and change
`spec.endpoint` if it gives this machine's address. The machine does
not run, so you do not need to set its reboot policy. The other
machines reboot one at a time. If two leaders remain, the cluster's API
does not answer while each of them reboots. Promote a follower
afterward, as in
[Add machines](https://liken.sh/docs/guides/add-nodes/#2-if-the-new-machine-is-a-leader),
to return to three.

Then rebuild the stick without the machine's file, as in step 5.
