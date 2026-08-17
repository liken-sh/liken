# Whether the bonds belong in the cluster

Open problem. The workload is a StatefulSet because the bonds live on
a volume. If the bonds lived in the API instead, keyed by the
adapter's address, the volume would go and the StatefulSet with it.
Nobody has priced that trade.

## Why it is a StatefulSet today

bluetoothd keeps its link keys and its device cache under
`/var/lib/bluetooth`. That directory is kilobytes, and it is the
difference between pressing the PS button and holding Create and PS to
pair again. A bond that died with the pod would turn every restart
into a re-pairing with a person's hands on the controller.

Each replica claims a different adapter on a different machine, and a
link key binds to the adapter it was made on, so each replica needs
its own keys. In the Deployment and StatefulSet vocabulary,
`volumeClaimTemplates` is the only thing that gives each replica a
volume of its own. One shared `ReadWriteOnce` claim would pin every
replica to one node, which is the opposite of what the adapter claims
are for.

The update behavior comes with it. An adapter allocates to one claim,
so a second pod holding the same ordinal would park Pending until the
first released the radio. A StatefulSet deletes a pod before it
creates the replacement, so the radio is free for the pod that claims
it next.

## What that shape costs

Two costs are already written down.

The volume follows the ordinal and the adapter does not. That is
[Bonds follow the pod's ordinal and adapters do not](bonds-follow-the-ordinal-and-adapters-do-not.md).
A link key binds to the adapter's own MAC address, the
`volumeClaimTemplates` key each volume by replica ordinal, and on a
fleet with more than one adapter a recreated ordinal 0 can allocate a
different machine's radio than the one its bonds were written for.

The volume pins the replica to a node. `deploy/operator.yaml` records
this on the `volumeClaimTemplates` itself: most default storage
classes bind a volume to the node that first used it, so an adapter
that moves to a different machine leaves its replica Unschedulable
until a person runs
`kubectl delete pvc bonds-bluetooth-operator-0 -n liken-system`. That
costs one re-pairing of each controller on that adapter.

## The candidate: bonds in the cluster, keyed by the adapter

The shape is this. The operator reads the adapter's own MAC address
after it claims the adapter. It loads that adapter's keys from a
cluster object named for that address, writes them into an `emptyDir`
mounted at `/var/lib/bluetooth` before bluetoothd starts, and persists
later changes back to the same object.

If that works, the pod needs no PVC. The workload can be a Deployment
or a DaemonSet, no volume binds to a node, and the bonds follow the
radio rather than the ordinal. The sibling document's mechanism stops
applying, because there is no ordinal-keyed storage left to disagree
with the allocation.

Two facts about the current pod say the write cannot happen where the
sketch puts it. The `operator` container starts after bluetoothd,
because bluetoothd is a sidecar and the kubelet starts a sidecar
first. The `operator` container does not mount the `bonds` volume at
all; only the `bluetoothd` container does. So the load would be a
third container, a plain init container that runs before the
bluetoothd sidecar, and that container has to find the adapter's
address without a bus to ask. Reading it from sysfs is the obvious
answer and nobody has tried it.

The persist half can stay in the `operator` container, which would
have to mount `/var/lib/bluetooth` as well.

Five questions have to be answered before this is a design.

### What object holds them

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

### How a change gets back

bluetoothd writes its own files under `/var/lib/bluetooth`, and the
operator has to learn that a file changed.

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

The file timing is verified and it splits in two. The key material is
written synchronously inside the management event callback:
`new_link_key_callback` calls `store_link_key`, `new_long_term_key_callback`
calls `store_longtermkey`, and `new_irk_callback` calls `store_irk`.
So `[LinkKey]`, `[LongTermKey]`, and `[IdentityResolvingKey]` land at
bond completion, not at shutdown. The `[General]` and `[DeviceID]`
metadata is deferred to a glib idle callback through
`g_idle_add(store_device_info_cb, device)`, so it lands on the next
loop iteration and is skipped entirely for temporary devices. Whether
the file write happens before or after the `Bonded` signal reaches the
operator is not verified. A design that reads the file on the signal
has to handle reading it one iteration early.

One fact belongs to the PVC side as much as to this one. BlueZ never
calls `fsync` on these files. It writes through glib's
`g_file_set_contents`, which fsyncs the temporary file only when the
destination already exists and is not empty, and never fsyncs the
containing directory. BlueZ creates a zero-byte `info` first, so the
first write of a new bond gets no fsync at all. A node that loses
power right after a pairing can lose that bond today, on the volume,
with nothing else wrong.

The alternatives to the signal are an inotify watch on
`/var/lib/bluetooth` or a re-read on an interval. Both work without
knowing when BlueZ writes. Both cost a read of the whole directory on
every wake, which is kilobytes, and neither has been tried here.

### What the file format commits us to

Reading and writing the files means reading and writing BlueZ's own
on-disk layout, `/var/lib/bluetooth/<adapter-mac>/<device-mac>/info`,
which is an ini-format file that BlueZ reads and writes with glib's
`GKeyFile`.

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

The 5.63 change is the shape to expect again. BlueZ writes both
groups today and its own commit message says the old term should be
deprecated later, so a reader has to hold both and follow the
deprecation.

This is the strongest argument for the PVC. A volume needs no
knowledge of the format at all. It carries whatever bluetoothd wrote,
in whatever layout that release uses, and a BlueZ upgrade that changes
the layout changes nothing about the volume. The candidate design has
to read and write that layout, so a BlueZ release that changes it
breaks the operator, and the operator's tests have to pin a BlueZ
version to catch it.

The image builds BlueZ 5.82 from source, so this repository picks
which BlueZ it reads. That narrows the risk to the upgrades this
repository makes, and it does not remove it. 5.83 already added two
keys, so the next bump is one of them.

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

### Whether it composes with the pairing CRD

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

### What the workload becomes

The exclusive adapter claim still has to have delete-before-create.
That property is what a StatefulSet update gives for free today, and
it belongs to the hardware and not to the storage, so it survives the
bonds moving.

Three candidates:

- A Deployment with `strategy: Recreate`. It deletes every pod before
  it creates any replacement, which satisfies the claim. It also takes
  every adapter in the fleet down for the length of one update,
  instead of one adapter at a time.
- A Deployment with `RollingUpdate` and `maxSurge: 0`. It deletes
  before it creates and keeps the update per-pod. Whether the
  scheduler reallocates a released adapter fast enough for this to
  make progress has not been tried.
- A DaemonSet. One adapter per machine is the real topology, and a
  DaemonSet states that directly: no `replicas` to keep in step with
  the hardware count, and the pod lands on the machine that has the
  radio because it lands on every machine. Its default update strategy
  deletes a pod before it creates the replacement. The cost is a pod
  on every machine with no adapter, which the current design avoids,
  because a replica past the adapter count parks Pending and costs
  nothing.

## What nobody has measured

- Whether the ordinal problem happens often enough to be worth a
  redesign. The sibling document waits on the same drill: two
  adapters, delete the pods, record how often the allocation changes.
- What one adapter's bonds weigh. The volume requests 64Mi and the
  README calls the content kilobytes. Nobody has read the directory,
  because the bluetoothd image carries no `ls`. That is
  [Inspecting a pod with no tools](inspecting-a-pod-with-no-tools.md),
  and it blocks a measurement this design needs, because a Secret is
  limited to 1 MiB.
- Whether an init container can read the adapter's address from sysfs
  before bluetoothd exists to be asked.
- Whether a bond written after startup can ever take effect without a
  restart. bluetoothd calls `load_devices()` once per adapter, from
  `adapter_register()`, and the tree has no inotify, no
  `GFileMonitor`, and no `SIGHUP` handler. An external write while
  bluetoothd runs changes nothing until the daemon restarts or the
  controller disappears and returns. So the restore direction is
  start-time only, which is what the sketch assumed, and it means a
  bond moved to a second adapter costs a pod restart.
