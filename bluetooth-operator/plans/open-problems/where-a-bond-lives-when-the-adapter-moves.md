# Where a bond lives when the adapter moves

Open problem. A Bluetooth link key belongs to one adapter, and today
the operator keeps that key on a volume named for the replica's
ordinal. The two do not have to agree. The workload is a StatefulSet
because of that volume, so the storage decision picks the workload
shape, and nobody has priced any other place to keep the bonds.

This document starts from the state and not from the workload. Five
facts describe the state: what it is, what it must survive, who writes
it, who must read it, and what identifies it. The candidates come
after them, and each candidate is priced against them.

This is recorded in liken's
[milestone 58](https://github.com/liken-sh/liken/blob/main/plans/58-the-bluetooth-operator.md)
as well. It belongs here, with the operator that owns the storage.

## What the state is

bluetoothd keeps its link keys and its device cache under
`/var/lib/bluetooth`. That is one adapter's bonds, and it is
kilobytes. It is also the difference between pressing the PS button
and holding Create and PS to pair again. A bond that died with the pod
would turn every restart into a re-pairing with a person's hands on
the controller.

## What it must survive

Four events. Today's storage answers the first two, answers the third
only when the node stops cleanly, and does not answer the fourth.

- **A pod restart.** The bluetoothd container restarts and the keys
  have to still be there.
- **An operator upgrade.** A new image replaces the pod, and in some
  workload shapes the replacement carries a different name.
- **A node reboot.** A clean reboot is fine. A node that loses power
  is not, and that is true of the volume today. BlueZ never calls
  `fsync` on these files. It writes them through glib's
  `g_file_set_contents`, which fsyncs the temporary file only when the
  destination already exists and is not empty, and never fsyncs the
  containing directory. BlueZ creates a zero-byte `info` first, so the
  first write of a new bond gets no fsync at all. A node that loses
  power right after a pairing can lose that bond today, on the volume,
  with nothing else wrong.
- **The adapter moving to a different machine.** A USB dongle moves
  between machines, and the keys belong to the dongle. No candidate
  below answers this one for free.

## Who writes it

bluetoothd writes it, in its own format, on its own schedule.

The format is `/var/lib/bluetooth/<adapter-mac>/<device-mac>/info`, an
ini-format file that BlueZ reads and writes with glib's `GKeyFile`.
BlueZ documents that layout in
[`doc/settings-storage.txt`](https://git.kernel.org/pub/scm/bluetooth/bluez.git/tree/doc/settings-storage.txt),
and the same file states what the documentation is for: "It is
intended as reference for developers. Direct access to the storage
outside from bluetoothd is highly discouraged." That sentence has been
in the tree since 2012, in commit `bc2e9b815`, under the subject
"doc: Storage documentation is for developers and nobody else".

The layout has changed repeatedly inside 5.x, and each change is a
release that would have broken a reader:

| Release | Change to `info` |
|---|---|
| 5.15 | `[LongTermKey] Master` removed, `[SlaveLongTermKey]` added |
| 5.63 | `[PeripheralLongTermKey]` added, duplicating `[SlaveLongTermKey]` |
| 5.80 | `PreferredBearer` added to `[General]` |
| 5.83 | `LastUsedBearer` and `CablePairing` added to `[General]` |

The 5.63 change is the shape to expect again. BlueZ writes both groups
today and its own commit message says the old term should be
deprecated later, so a reader has to hold both and follow the
deprecation.

The image builds BlueZ 5.82 from source, so this repository picks
which BlueZ it reads. That narrows the risk to the upgrades this
repository makes, and it does not remove it. 5.83 already added two
keys, so the next bump is one of them.

The schedule splits in two, and this is verified in BlueZ's source.
The key material is written synchronously inside the management event
callback: `new_link_key_callback` calls `store_link_key`,
`new_long_term_key_callback` calls `store_longtermkey`, and
`new_irk_callback` calls `store_irk`. So `[LinkKey]`,
`[LongTermKey]`, and `[IdentityResolvingKey]` land at bond completion,
not at shutdown. The `[General]` and `[DeviceID]` metadata is deferred
to a glib idle callback through
`g_idle_add(store_device_info_cb, device)`, so it lands on the next
loop iteration, and it is skipped entirely for temporary devices.

## Who must read it

bluetoothd, at adapter registration, before any controller connects.
It reads the files once and never again. `load_devices()` runs one
time per adapter, from `adapter_register()`, and the tree has no
inotify, no `GFileMonitor`, and no `SIGHUP` handler. A write from
outside while bluetoothd runs changes nothing until the daemon
restarts, or until the controller disappears and returns. So the
restore direction is start-time only, and a bond moved to a second
adapter costs a pod restart.

There is no supported way around the file. No method on
`org.bluez.Adapter1` or `org.bluez.Device1` accepts a link key, an
LTK, or an IRK, so D-Bus offers no import. The kernel's management
interface does offer Load Link Keys, Load Long Term Keys, and Load
Identity Resolving Keys, and a process with `CAP_NET_ADMIN` can open
that socket beside a running bluetoothd. Each of those commands
replaces the whole list, because the kernel clears the existing keys
before it adds the new ones, so a load from outside would drop every
key bluetoothd had loaded and bluetoothd would report nothing. That
leaves writing the files before bluetoothd starts as the only path
that works.

## What identifies it

The adapter's own MAC address. It is not the replica ordinal and it is
not the node name. BlueZ stores each bond under the adapter's address,
so a bond written against one radio means nothing to another radio.

## What today's storage costs

Two costs follow from storing adapter-keyed state under an
ordinal-keyed name.

**The bonds follow the ordinal and the adapter does not.** Three facts
combine. The `volumeClaimTemplates` key each volume by replica
ordinal, so replica 0 always mounts `bonds-bluetooth-operator-0`,
whatever hardware that pod ends up holding. The adapter comes from a
ResourceClaimTemplate, so each pod allocates an adapter afresh when it
is created: the scheduler picks a machine that has a free adapter,
nothing in the claim says which one, and nothing carries the previous
allocation forward. So a recreated ordinal 0 can allocate a different
machine's radio than the one its bonds were written for. The bonds it
mounts are for the old radio, the new radio has no bonds at all, and
every controller paired to that machine has to be paired again by
hand.

A cluster with one adapter never sees this, because there is only one
allocation to make. The README's paragraph on more than one adapter,
under "Deploying it", says to raise `replicas` to the number of
machines. That instruction is correct about placement and silent about
this.

A fix has to tie the volume to the allocation, not to the ordinal. The
Kubernetes objects in play offer no way to say that. A
`volumeClaimTemplate` keys on the ordinal, a ResourceClaimTemplate
allocates per pod, and neither one reads the other. No fix inside
those two objects is known today, so a fix means moving the storage or
moving the allocation.

**The volume pins the replica to a node.** `deploy/operator.yaml`
records this on the `volumeClaimTemplates` itself: most default
storage classes bind a volume to the node that first used it, so an
adapter that moves to a different machine leaves its replica
Unschedulable until a person runs
`kubectl delete pvc bonds-bluetooth-operator-0 -n liken-system`. That
costs one re-pairing of each controller on that adapter, and nothing
else.

## The claim still needs delete-before-create

An adapter allocates to one claim at a time. A second pod that claims
the same adapter parks Pending until the first pod releases the radio,
and an update that creates before it deletes never finishes. That
property belongs to the hardware and not to the storage, so it holds
for every candidate below.

Four shapes provide it, and each candidate names which ones it
permits.

- **A StatefulSet.** Its default update deletes a pod before it
  creates the replacement. This is what the current design gets for
  free.
- **A Deployment with `strategy: Recreate`.** It deletes every pod
  before it creates any replacement, which satisfies the claim.
  Kubernetes states the order for upgrades only: "all Pods of the old
  revision will be terminated immediately. Successful removal is
  awaited before any Pod of the new revision is created", and a pod
  deleted by hand is replaced immediately by the ReplicaSet. It also
  takes every adapter in the fleet down for the length of one update,
  instead of one adapter at a time.
- **A Deployment with `RollingUpdate` and `maxSurge: 0`.** It keeps
  the update per-pod. `maxSurge` is the number of pods that can exist
  over the desired count, so 0 holds the total at the desired count
  and no new pod can start before an old one goes. Kubernetes does not
  say that in those words, so read this as following from the stated
  ceiling. Whether the scheduler reallocates a released adapter fast
  enough for this to make progress has not been tried.
- **A DaemonSet.** One adapter per machine is the real topology, and
  the operator now documents it: a machine with two adapters serves
  one. A DaemonSet states that topology directly. There is no
  `replicas` to keep in step with the hardware count, and the pod
  lands on the machine that has the radio because it lands on every
  machine. Its default update strategy is `RollingUpdate` with
  `maxUnavailable: 1` and `maxSurge: 0`, and Kubernetes states the
  order directly: the update stops the old pods and "then brings up
  new DaemonSet pods in their place". The cost is a pod on every
  machine with no adapter, which the current design avoids, because a
  replica past the adapter count parks Pending and costs nothing.

The Kubernetes behavior above is verified in
[Deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/),
[Perform a Rolling Update on a DaemonSet](https://kubernetes.io/docs/tasks/manage-daemon/update-daemon-set/),
and the
[DaemonSet API reference](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/daemon-set-v1/).

## The candidates

Nothing is chosen here.

### A PVC per replica, which is today

Storage keyed by the replica ordinal, in a format that stays opaque to
this repository, on a volume that most default storage classes pin to
one node. The claim requests 64Mi. Each replica claims a different
adapter on a different machine and needs its own keys, and
`volumeClaimTemplates` is the only thing in the Deployment and
StatefulSet vocabulary that gives each replica a volume of its own.
One shared `ReadWriteOnce` claim would pin every replica to one node,
which is the opposite of what the adapter claims are for.

What it costs is the section above: the ordinal disagrees with the
allocation, and the volume pins the replica to a node. On liken the
pinning is not a property of one default class that a different class
could replace.
[liken's machine reference](https://liken.sh/docs/reference/machine/)
names `podStorage` as "Durable
storage that pods claim by name: the PersistentVolumeClaim pool. The
local-path provisioner supplies volumes from this pool." A local-path
volume lives on the machine that provisioned it, so on liken the
volume is node-local by construction.

What it is worth is the format. A volume needs no knowledge of the
layout at all. It carries whatever bluetoothd wrote, in whatever
layout that release uses, and a BlueZ upgrade that changes the layout
changes nothing about the volume. This is the strongest argument
against every candidate that has to parse `info`.

Workload shape: a StatefulSet, and only a StatefulSet.
`volumeClaimTemplates` exists on no other object, so this candidate
picks the workload. Delete-before-create comes with the StatefulSet
update.

One variant keeps this storage and changes the other end. Take the
allocation off the pod: give each adapter a named claim that a person
writes once, so replica 0 always allocates the same radio. That means
somebody writes down which machine has which adapter, which is the
thing the raw ResourceClaimTemplate exists to avoid.

### One shared volume keyed by the adapter address

Take the bonds off the ordinal without leaving the filesystem. Put
every adapter's bonds in one place, in a directory named for the
adapter address, on storage that any replica can mount. The identity
then matches, and no format knowledge is needed. It also answers the
fourth event in "What it must survive": an adapter that moves to a
different machine finds its own directory from the new machine.

The cost is a `ReadWriteMany` requirement, and the question of what
happens when two replicas land on the same directory. liken supplies
volumes from the `podStorage` role through the local-path provisioner,
which serves one machine's disk, so a `ReadWriteMany` class has to
come from outside liken: an NFS server, or a network filesystem the
cluster already runs. That is a dependency the operator does not have
today, and it puts the bonds behind the network at the moment
bluetoothd starts.

Workload shape: any of the four. A volume that every pod mounts is an
ordinary `volumes:` entry, not a `volumeClaimTemplate`, so a
Deployment with `Recreate` or with `maxSurge: 0`, or a DaemonSet, all
provide delete-before-create as listed above.

### A cluster object keyed by the adapter address

The shape is this. The operator reads the adapter's own MAC address
after it claims the adapter. It loads that adapter's keys from a
cluster object named for that address, writes them into an `emptyDir`
mounted at `/var/lib/bluetooth` before bluetoothd starts, and persists
later changes back to the same object.

If that works, the pod needs no PVC. No volume binds to a node, and
the bonds follow the radio rather than the ordinal, so both costs in
"What today's storage costs" stop applying.

Two facts about the current pod say the write cannot happen where the
sketch puts it. The `operator` container starts after bluetoothd,
because bluetoothd is a sidecar and the kubelet starts a sidecar
first. The `operator` container does not mount the `bonds` volume at
all; only the `bluetoothd` container does. So the load would be a
third container, a plain init container that runs before the
bluetoothd sidecar, and that container has to find the adapter's
address without a bus to ask. Reading it from sysfs is the obvious
answer and nobody has tried it. The persist half can stay in the
`operator` container, which would have to mount `/var/lib/bluetooth`
as well.

Workload shape: any of the four. This candidate is the one that frees
the shape completely, because nothing in the pod is storage any more.
Delete-before-create still applies, from whichever shape is picked.

Three questions have to be answered before this is a design.

#### What object holds them

A Secret is the obvious carrier. A link key is a secret in the plain
sense: it authenticates a controller to a radio, and anybody who holds
it holds the bond. Kubernetes has a type for that, the operator
already has an API client, and a Secret needs no schema.

That puts the keys in etcd. Whether etcd is encrypted at rest is a
property of the cluster the operator is installed into. The operator
does not control it, and it cannot check it on behalf of the person
installing it. On a cluster with no encryption at rest, the keys sit
in the datastore in the clear, and any read of the datastore or of its
backups reads them.

A CRD is the other candidate. It would carry structure, a status, and
a place to record which machine last held the adapter. It has the same
etcd question, because the material is the same material, and it adds
a schema to maintain across BlueZ releases. Neither one is chosen
here.

#### How a change gets back

bluetoothd writes its own files under `/var/lib/bluetooth`, and the
operator has to detect that a file changed.

The signal side is mostly built already. `watchBlueZ` subscribes to
`org.freedesktop.DBus.ObjectManager` signals from `org.bluez` and to
`PropertiesChanged` on `org.bluez.Device1`, and `controllersFrom`
reads each device's `Paired` property.

The precise signal for a new bond is `PropertiesChanged` with
`Bonded` set true, and not `InterfacesAdded`. BlueZ creates the device
object in `device_new()` at discovery, for any discoverable device, so
`InterfacesAdded` says a radio was heard and says nothing about a
bond. `org.bluez.Device1` carries both `Paired` and `Bonded` as
readonly booleans, and the doc's own words for `Bonded` are that "the
information exchanged on pairing process has been stored and will be
persisted". Two behaviors go with that. `Paired` is deferred until
service discovery finishes, and `Bonded` is not. gdbus suppresses
`PropertiesChanged` for an interface it has not yet published, so a
device first seen at pairing time carries its `Bonded` value inside
the `InterfacesAdded` dictionary and emits no separate change. A
watcher that only counts `InterfacesAdded` as a pairing reads
discovery as a bond. This is verified in BlueZ's `src/device.c`,
`src/adapter.c`, and `gdbus/object.c`, and in
[`doc/org.bluez.Device.rst`](https://git.kernel.org/pub/scm/bluetooth/bluez.git/tree/doc/org.bluez.Device.rst).

Whether the file write happens before or after the `Bonded` signal
reaches the operator is not verified. A design that reads the file on
the signal has to handle reading it one iteration early, because the
`[General]` section arrives through `g_idle_add` while the keys are
already on disk.

The alternatives to the signal are an inotify watch on
`/var/lib/bluetooth` or a re-read on an interval. Both work without
knowing when BlueZ writes. Both cost a read of the whole directory on
every wake, which is kilobytes, and neither has been tried here.

#### Whether it composes with the pairing CRD

[Who owns the pairing UX](who-owns-the-pairing-ux.md) already floats a
pairing-request CRD: a person creates a resource that names the
adapter and a duration, the operator opens a pairing window, and the
status reports which controller paired.

A resource that records a pairing request and a resource that stores
the resulting bond are plausibly the same feature seen from two ends.
The request names an adapter and produces a bond. The store holds
bonds keyed by adapter. A design that builds them apart ends with two
objects that name the same adapter and two code paths that write the
same key material, and no rule for which one is right when the two
disagree. They should be designed together, or this one should wait
until the pairing CRD is built.

### A hostPath keyed by the adapter address

Mount a directory from the machine's own state, named for the adapter
address, at `/var/lib/bluetooth`. The bonds then stay with the machine
that has the radio, the identity matches, and no code parses BlueZ's
format. Nothing new has to be written or read by this repository.

The pod already mounts three host paths, so a hostPath is not foreign
to this deployment. `deploy/operator.yaml` mounts
`/var/lib/kubelet/plugins/bluetooth.liken.sh`,
`/var/lib/kubelet/plugins_registry`, and `/var/run/cdi`, all with
`type: DirectoryOrCreate`, because a node that has never run a DRA
driver has none of these paths.

The first cost is that the state is node-local. It does not survive
the adapter moving machines, which is the fourth event in "What it
must survive". A dongle carried to another machine arrives with no
bonds, and the bonds it left behind stay on a machine with no radio.

The second cost is that liken has no documented directory for this.
Two facts from liken's own documentation decide it. The machine runs a
read-only squashfs image, and
[How liken works](https://liken.sh/docs/concepts/how-liken-works/)
states it: "The image is a read-only squashfs file, built by the
project and published in every release. Nothing on a machine edits
it." What survives a reboot is only the storage roles a machine
declares on a disk, and
[the machine reference](https://liken.sh/docs/reference/machine/) says
what happens to everything else: "The machine's RAM root backs each
role that the spec does not declare." A role backed by `Memory` keeps
its directory on that RAM root.

So a hostPath outside a declared role is RAM, and a bond written there
is gone at the next reboot. liken names an absolute path for two roles
only, `/tmp` for `machineEphemeral` and `/var/log/pods` inside
`podEphemeral`. It names no path for `machineState`, for
`clusterState`, or for `podStorage`, and the word `hostPath` does not
appear in liken's documentation at all. The devices pages state the
opposite direction, that a device claim "grants no privilege, mounts
no host path, and loads no kernel module for the pod". That is a
statement about claims and not a policy on hostPath, so it neither
permits this nor forbids it.

This candidate therefore needs something liken does not offer today: a
named, documented, durable host directory for a workload's own state.
Asking liken for one is a change to liken, not to this operator.

Workload shape: any of the four, and a DaemonSet fits it best, because
a hostPath is per-machine state and a DaemonSet is a per-machine
workload. The two agree on identity for the first time.

### No persistence, and pair again

Keep nothing. Mount an `emptyDir` at `/var/lib/bluetooth` and let the
keys die with the pod. This is the baseline the other four are
measured against.

It costs a person one re-pairing of every controller, at every pod
restart, every operator upgrade, and every node reboot. Each one means
holding Create and PS on the controller and pairing it again. Today a
person pairs by running `bluetoothctl` inside the operator's pod. A
DualSense's first pairing also needs `BLUETOOTH_CLASSIC_BONDED_ONLY`
off, which is a `kubectl set env` on the workload, so a re-pairing is
not one step.

Workload shape: any of the four, with no storage constraint at all.
Delete-before-create still applies, because the adapter claim is
exclusive whatever the storage does.

## Two adapters on one machine

Two adapters on one machine hit a second problem that this one does
not cause. `sliceName` names the ResourceSlice for the node and the
driver alone, so two replicas on the same machine write to the same
object and each pass removes the other's devices. The audio operator
has the same naming and the same collision, and its
[The claim takes any sound card, and a node serves only one](https://github.com/liken-sh/audio-operator/blob/main/plans/open-problems/the-claim-takes-any-sound-card-and-a-node-serves-only-one.md)
works it through for both operators.

## What nobody has measured

- Whether the ordinal problem happens often enough to be worth a
  redesign. This waits on a drill with more than one adapter. A QEMU
  lab with two emulated adapters would size the problem without
  hardware: delete the pods, watch which claim allocates which
  adapter, and record how often the allocation changes. Until that
  runs, the size of the problem is a guess.
- What one adapter's bonds weigh. The volume requests 64Mi and the
  README calls the content kilobytes. Nobody has read the directory.
  [A Secret is limited to 1 MiB](https://kubernetes.io/docs/concepts/configuration/secret/),
  so the cluster-object candidate needs this number before it is a
  design.
- Whether an init container can read the adapter's address from sysfs
  before bluetoothd exists to be asked.
- What a `ReadWriteMany` class would cost this cluster, and what two
  replicas do to one shared directory. liken supplies none, so the
  answer starts with a filesystem the cluster does not run for this
  today.
- How often an adapter actually moves between machines. The PVC and
  the hostPath both cost a re-pairing when it happens, the shared
  volume and the cluster object do not, and nobody has counted it.
