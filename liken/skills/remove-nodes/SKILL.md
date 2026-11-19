---
name: remove-nodes
description: "Remove one machine from a running liken cluster: demote it first if it is a leader, drain it, power it off, delete its Node and Machine, and rebuild the install stick without it. Use when a node leaves the cluster for good, including a machine whose hardware is already gone."
---

This skill is the guide at https://liken.sh/docs/guides/remove-nodes/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Remove machines from a cluster

Remove one machine at a time. If the machine is a leader, make it a
follower first, so that it leaves the datastore while it still runs.
Then move its workloads off, power it off, and delete its objects from
the cluster. To replace a machine's disks or install it again under
the same name, do not remove it. Follow
[Install a cluster](https://liken.sh/docs/guides/install/#to-reinstall-a-machine-that-liken-installed)
instead.

If the machine is already gone, for example because its hardware
failed, go to [A machine that is already gone](#a-machine-that-is-already-gone).

## 1. If the machine is a leader

A leader runs a control plane and is a member of the cluster's etcd
datastore. When you remove its name from
[`spec.leaders`](https://liken.sh/docs/reference/cluster/#spec--leaders), the machine
becomes a follower, and its etcd member leaves while the other leaders
keep a majority.

Keep an odd number of leaders. With two leaders, etcd has no majority
while either one is down, so the cluster's API stops responding each
time one of them reboots. To retire one of three
leaders, first promote a follower in its place, as in
[Add machines](https://liken.sh/docs/guides/add-nodes/#2-if-the-new-machine-is-a-leader),
and wait until the `ClusterConverged` condition of every machine is
`True`. Then remove the old leader's name.

### Let the machine finish its own demotion

After the machine reboots as a follower, its Node still has the
control-plane role, and etcd still lists its member. etcd counts that
member when it computes the majority, so the next reboot of a leader
can lose the majority. `machine-operator` on the machine fixes this:
it deletes the machine's Node, which makes k3s remove the etcd member,
and it reboots once more to register as a follower.

The operator does this only when the machine's
[`spec.rebootPolicy`](https://liken.sh/docs/reference/machine/#spec--rebootpolicy) is
`Auto`. Under `Manual`, the machine reports `NodeCurrent: False` with
the reason `DemotionPending`, and it waits. Set the policy before you
edit the leaders:

    kubectl patch machine <name> --type merge -p '{"spec":{"rebootPolicy":"Auto"}}'

### Edit the leaders

Remove the machine's name from `spec.leaders` in two places:

* On the live cluster, with `kubectl edit cluster`. Each machine reads
  its role from this object.
* In your `mycluster/cluster.yaml`. The file is read only when you
  build a new cluster, so keep it in step with the live cluster.

If [`spec.endpoint`](https://liken.sh/docs/reference/cluster/#spec--endpoint) gives
this machine's address, change it to another leader's address in the
same edit. A new follower joins the cluster through the endpoint, and
the machine you remove will no longer respond there.

The first name in `spec.leaders` is the founding leader. Every other
leader joins etcd at the founding leader's address when it boots. If
you remove the first name, the next name becomes the founding leader.
k3s ignores `cluster-init` on a machine whose datastore already exists,
so the new founding leader keeps its data.

A change to `spec.leaders` reboots every machine, one at a time,
because each machine reads the leaders only when it boots: a follower
uses the leaders as its time sources, and a leader joins at the
founding leader's address. Under `Manual`, the `ClusterConverged`
condition of each machine reports `RebootPending`, and its request reads `False` in the APPROVED column
of `kubectl get disruptions`. Approve each one with
[`liken approve-reboot`](https://liken.sh/docs/reference/cli/#liken-approve-reboot), as
in [Upgrade the fleet](https://liken.sh/docs/guides/upgrade/).

### Watch the demotion

    kubectl get machine <name> -o jsonpath='{.status.conditions[?(@.type=="NodeCurrent")]}'

After the machine reboots as a follower, the condition has the
reason `AwaitingTurn` until the machine gets a reboot turn. The
demotion belongs to the `Disruption` named `<name>.machine-operator`,
which `kubectl get disruptions` lists. When the turn comes, the reason
is `DemotionRebooting`, and the machine deletes its Node and reboots.
When it is back, the condition is `True` with the reason
`NodeMatchesRole`, and `kubectl get nodes` shows the machine with no
`control-plane` or `etcd` role. Continue from step
2 when the `ClusterConverged` condition of every machine is `True`.

## 2. Move the workloads off

    kubectl drain <name> --ignore-daemonsets --delete-emptydir-data

The drain cordons the node and evicts its pods, so their controllers
start them on other nodes. DaemonSet pods, which include the OS
workloads, keep running until the machine powers off.

The default `StorageClass`, k3s's `local-path`, stores each volume on
the disk of the node that first used it. A pod with such a volume
cannot start on another node after the drain, and the volume's data
is lost with the machine. Copy the data off, or accept its loss,
before you continue.

## 3. Power the machine off

Power the machine off at its switch. About 40 seconds later, `kubectl
get machines` shows it `Lost`, and the Cluster reads `Degraded`.

The machine's disks still hold its copy of the cluster's join token
and certificates. Erase the disks before you use the hardware outside
this cluster.

## 4. Delete the Node and the Machine

    kubectl delete node <name>
    kubectl delete machine <name>

Nothing deletes these objects for you. A `Lost` machine counts
against every
[`MachineDisruptionBudget`](https://liken.sh/docs/reference/machinedisruptionbudget/)
that selects it, and the `default` budget allows one machine at a
time, so until you delete the Machine no other machine gets a reboot
turn, and upgrades and Cluster edits stop. Each `Disruption` has its
`Machine` as its owner, so Kubernetes deletes the machine's
`Disruption`s with it.

Power off before you delete. A machine that still runs creates its
Machine again when its `machine-operator` starts, and its kubelet
registers the Node again at its next boot.

If you run the fleet from git with the `flux` feature, also delete the
machine's file from the repository, or Flux creates the Machine again.
[The GitOps guide](https://liken.sh/docs/guides/gitops/) puts
`kustomize.toolkit.fluxcd.io/prune: disabled` on the Machine
documents, so removing the file does not delete the object. You delete
it with `kubectl`.

## 5. Rebuild the install stick

Delete the machine's file from `mycluster/machines/`. While the file
is there, `liken layer` includes it, and the stick's menu offers
`install as <name>` and `wipe and reinstall as <name>` for the removed
machine. Use the release that your fleet runs, which the VERSION
column of `kubectl get clusters` shows. Build the layer and the stick
again with
[`liken layer`](https://liken.sh/docs/reference/cli/#liken-layer) and
[`liken stick`](https://liken.sh/docs/reference/cli/#liken-stick):

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick channel/<version> mycluster/deployment.cpio mycluster/install.img

Check the device name before the next command. The command
overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

The remaining machines keep the layer they were installed with, which
still has the old file. It has no effect, because the machine is not
in `spec.leaders`.

## 6. Watch it leave

    kubectl get machines
    kubectl get clusters

The machine is gone from both `kubectl get machines` and `kubectl get
nodes`. The Cluster returns to `Ready` when every machine that remains
is ready.

## A machine that is already gone

A machine that cannot boot cannot demote itself or drain its pods, so
delete its objects first:

    kubectl delete node <name>
    kubectl delete machine <name>

For a leader, deleting the Node also removes its etcd member. Do
this before any other Cluster edit: the `Lost` machine uses up the
`default` budget of one, so an edit to `spec.leaders` reboots no
machine while the Machine exists.

Then, for a leader, remove its name from `spec.leaders` as in step 1,
both on the live cluster and in `mycluster/cluster.yaml`, and change
`spec.endpoint` if it gives this machine's address. The machine does
not run, so you do not need to set its reboot policy. The other
machines reboot one at a time. If two leaders remain, the cluster's API
stops responding while each of them reboots. Promote a follower
afterward, as in
[Add machines](https://liken.sh/docs/guides/add-nodes/#2-if-the-new-machine-is-a-leader),
to return to three.

Then rebuild the stick without the machine's file, as in step 5.
