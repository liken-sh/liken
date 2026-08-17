# A Secret for each adapter

Plan 03, Proposed. Nothing here is built.

It moves the link keys off the PersistentVolumeClaim and into one
Kubernetes Secret for each adapter, named for the adapter's own
address. A plain init container loads that Secret into an `emptyDir`
before bluetoothd starts, and the operator writes later changes back.
The PVC, the `volumeClaimTemplates`, and the StatefulSet all go away.

This plan replaces the open problem "Where a bond lives when the
adapter moves", which is deleted in the same commit. What that document
established and this design keeps is in
[What the storage has to do](#what-the-storage-has-to-do) and
[What was considered and set aside](#what-was-considered-and-set-aside).

Two kinds of claim appear below and they are marked. A claim about
BlueZ or the kernel was read in the source that is named beside it. A
claim marked "measured" was run: on Kubernetes v1.36.3+k3s1 for the
pod behavior, in a container on a machine with a real adapter for the
address read, and on liken-1 with a real DualSense for
[What to store, and what not to](#what-to-store-and-what-not-to). The
BlueZ reading was done in master `bd898962`, which calls itself 5.87.
The `bluetoothd` image builds 5.82, so a reader who checks these lines
against the shipped image reads an older tree.

## The problem

Link keys live on a PVC that a StatefulSet keys by replica ordinal. A
bond belongs to an adapter's MAC address. Those are two different
identities, and three costs follow from storing one under the other.

On a fleet with several adapters, a recreated pod can mount the wrong
machine's bonds. The `volumeClaimTemplates` key each volume by replica
ordinal, so replica 0 always mounts `bonds-bluetooth-operator-0`. The
adapter comes from a ResourceClaimTemplate, which allocates afresh for
each pod, so a recreated replica 0 can hold a different machine's
radio than the one its bonds were written for. Every controller paired
to that machine then has to be paired again by hand.

Most default storage classes pin the volume to the node that first used
it. On liken this is not a property of one class that a different class
could replace. liken supplies PVCs from the `podStorage` role through
the local-path provisioner, and a local-path volume lives on the
machine that provisioned it. So an adapter that moves to a different
machine leaves its replica Unschedulable until a person runs
`kubectl delete pvc bonds-bluetooth-operator-0 -n liken-system`.

The StatefulSet exists only because `volumeClaimTemplates` is the only
way to give each replica its own volume. `volumeClaimTemplates` exists
on no other object, so the storage decision picks the workload shape.

## The design

* **One Secret for each adapter**, named for the adapter's BD_ADDR, in
  the operator's namespace. It holds two files for each paired device
  and nothing else. A Secret's name must be a DNS subdomain name and a
  colon is not permitted in one, so the name is `bluetooth-bonds-`
  plus the address in lowercase with dashes: the adapter in plan 02's
  drill, `04:4A:69:66:92:27`, gives
  `bluetooth-bonds-04-4a-69-66-92-27`. The prefix names the domain
  because several operators share the namespace. The data keys
  take the same form with a suffix that names the file,
  `7c-66-ef-22-e7-80.info` and `7c-66-ef-22-e7-80.cache`, because a
  Secret's keys accept only letters, digits, `-`, `_`, and `.`. Each
  value is the file, byte for byte. A device with no cache entry yet
  has one key and not two.
* **A plain init container** runs before the bluetoothd sidecar. It
  reads the adapter's BD_ADDR from the kernel, reads that adapter's
  Secret from the API server, writes the files into an `emptyDir`
  mounted at `/var/lib/bluetooth`, and exits.
* **The operator writes changes back.** It watches BlueZ over D-Bus,
  reads the adapter's directory after a settle window, and updates the
  Secret when the tree differs from it. The `operator` container mounts
  the same `emptyDir` at `/var/lib/bluetooth`, which it does not mount
  today.
* **The PVC, the `volumeClaimTemplates`, and the StatefulSet go away.**
  The workload becomes a DaemonSet. See
  [The workload shape](#the-workload-shape).

The Secret carries files and not fields. Nothing in this design parses
either file, so the layout changes BlueZ has made inside 5.x cost
nothing here: `[SlaveLongTermKey]` in 5.15, `[PeripheralLongTermKey]`
in 5.63, `PreferredBearer` in 5.80, `LastUsedBearer` and
`CablePairing` in 5.83. That property is the strongest thing the PVC
had, and this design keeps it. The one thing the design does have to
know is which files to carry, and
[What to store, and what not to](#what-to-store-and-what-not-to) is
where that knowledge lives.

## What the storage has to do

The deleted document set five facts about the state, and they still
hold.

**What it is.** bluetoothd keeps its link keys and its device cache
under `/var/lib/bluetooth`. That is one adapter's bonds, and it is
kilobytes. It is also the difference between pressing the PS button and
holding Create and PS to pair again.

**What it must survive.** Four events. This design is the first
candidate that answers all four.

- A pod restart. The init container loads the Secret again, and the
  `emptyDir` survives a container restart inside the pod sandbox
  anyway.
- An operator upgrade. The Secret is named for the adapter, so a
  replacement pod under any name loads the same bonds.
- A node reboot. The bonds are in the datastore, not on the node. See
  [Durability](#durability).
- The adapter moving to a different machine. The Secret is one object
  in one namespace, so a dongle carried to another machine finds its
  own bonds from the new machine.

**Who writes it.** bluetoothd, in its own format, on its own schedule.
BlueZ documents the layout in
[`doc/settings-storage.txt`](https://git.kernel.org/pub/scm/bluetooth/bluez.git/tree/doc/settings-storage.txt),
and the same file says the documentation "is intended as reference for
developers. Direct access to the storage outside from bluetoothd is
highly discouraged." That sentence has been in the tree since 2012, in
commit `bc2e9b815`. This design still writes into that directory before
bluetoothd starts, which is direct access. What it does not do is
interpret the contents.

**Who must read it.** bluetoothd, at adapter registration, before any
controller connects. It reads the files one time and never again, which
is the next section. So the restore direction is start-time only: a
Secret updated while bluetoothd runs changes nothing until the pod
restarts.

**What identifies it.** The adapter's own MAC address. Not the replica
ordinal, and not the node name.

## Why an init container and not the operator

BlueZ reads its bonds exactly once per adapter, inside
`adapter_register()`. There is no inotify, no `GFileMonitor`, and no
`SIGHUP` handler. `load_devices` has exactly one call site, and it is
`adapter_register` in `src/adapter.c`. So the files have to exist
before bluetoothd starts.

The operator container cannot put them there. bluetoothd is a sidecar,
and the kubelet starts a sidecar before the app containers, so the
operator starts second. That leaves a plain init container, which the
kubelet runs to completion before it starts the sidecar.

Nothing else writes a bond into a running daemon. No method on
`org.bluez.Adapter1` or `org.bluez.Device1` accepts a link key, an LTK,
or an IRK, so D-Bus offers no import. The kernel's management interface
does offer Load Link Keys, Load Long Term Keys, and Load Identity
Resolving Keys, and each of those commands replaces the whole list,
because the kernel clears the existing keys before it adds the new
ones. A load from outside would therefore drop every key bluetoothd had
loaded, and bluetoothd would report nothing about it. Writing the files
before the daemon starts is the only path that works.

## Reading the adapter address

The kernel does not expose the BD_ADDR in sysfs.
`/sys/class/bluetooth/hci0/` holds `device`, `hci0:1`, `power`, `reset`,
`rfkill0`, `subsystem`, and `uevent`, and the uevent is `DEVTYPE=host`.
The USB serial is a vendor placeholder, `00e04c000001` on the Realtek
adapter in the lab and `000000000` on a MediaTek one, so it identifies
nothing.

The address comes from the `HCIGETDEVINFO` ioctl, request `0x800448d3`,
on an unbound `AF_BLUETOOTH` / `SOCK_RAW` / `BTPROTO_HCI` socket. The
`bdaddr` field sits at offset 10 of `struct hci_dev_info`.

**The privilege, measured.** The read works as uid 65534, with
`--cap-drop ALL`, a read-only root filesystem, and `no-new-privileges`.
It fails with `EAFNOSUPPORT` in a private network namespace even when
the container is privileged. So the requirement is `hostNetwork` alone,
which the pod already sets, and the init container needs no capability
and does not run as root.

**The ioctl, not the management API.** `mgmt`'s index list filters
adapters flagged `HCI_UNCONFIGURED`, and `MGMT_OP_READ_INFO` returns
`INVALID_INDEX` for them. Realtek adapters are the family that
registers that way, through `HCI_QUIRK_INVALID_BDADDR`, and the lab
adapter is a Realtek `0bda:c821`. `hci_get_dev_info` applies no filter,
so the ioctl answers for an adapter the management API hides.

**There is a race, and the init container retries.** `hci_register_dev`
returns before the kernel's queued `power_on` work runs, and until that
work runs `hdev->bdaddr` is all zeros. For a USB dongle that is roughly
a second after enumeration. So the init container reads in a loop until
the address is non-zero. The address survives the kernel powering the
adapter back down afterwards, so a machine that has never run
bluetoothd still reports a real address.

**More than one adapter on a machine.** Map the allocated claim to the
`hciN` index by resolving `/sys/class/bluetooth/hciN` to its real path
and walking up to the first ancestor that has an `idVendor` file. That
half needs `/sys`. The address read itself needs neither `/sys` nor
`/dev`.

## Init container ordering

Measured on Kubernetes v1.36.3+k3s1.

A plain init container listed **before** a sidecar completes first. The
sidecar started 0.92 seconds after the fetch container exited, and it
read the file the fetch container had written. Listed **after** the
sidecar, the same container ran concurrently with it and the sidecar
read nothing. List order is load-bearing, and the pod spec has to say
why.

The rule behind both runs: the kubelet starts the next entry in
`initContainers` when a plain init container **exits 0**, and when a
sidecar's `started` status becomes true.

A sidecar that crashes and restarts on its own does **not** re-run the
init container. That costs nothing here, because an `emptyDir` survives
container restarts within the pod sandbox, so bluetoothd re-reads the
same files it read the first time. This behavior is correct for this
design and needs no mitigation. If a future need arises,
`restartPolicyRules` with `action: RestartAllContainers` exists and was
proved to work on this cluster, at the cost of restarting the main
container as well.

## A plain init container can hold the DRA claim

Measured, and undocumented upstream. The Kubernetes documentation does
not discuss init containers and DRA in either direction, so this is
recorded here rather than cited.

Three things held. The API server accepts `resources.claims` on a plain
init container. The kubelet injects the device into that container, and
into no container that does not declare the claim. The claim is
allocated before the init container runs: the allocation timestamp was
03:50:22Z and the init container started at 03:50:23Z.

That is what lets the init container name the same adapter claim the
`bluetoothd` container names, which is how it maps an allocation to an
`hciN` index on a machine with more than one adapter.

## What to store, and what not to

Store two files for each paired device: `<adapter>/<device>/info` and
`<adapter>/cache/<device>`. The rule is the device's own directory. A
device that has one is paired to this adapter, and its cache entry
travels with it. A cache entry with no device directory beside it is a
device the radio has only seen, and it is skipped.

**`info` is the bond.** It holds the link key, the class, the DeviceID,
and the `[General] Services` list.

**`cache/<device>` is required.** It holds the SDP records under
`[ServiceRecords]`, and for a BR/EDR HID device the report descriptor
exists in no other file. `read_device_records` in `src/device.c` is the
only reader. It builds the path `/%s/cache/%s` under
`/var/lib/bluetooth` and reads the `ServiceRecords` group. Its one caller is
`btd_device_get_record`, which the input profile calls through
`input_device_update_rec` to fill `idev->rec`. `extract_hid_record`
returns `-ENOENT` when that pointer is null, and `hidp_add_connection`
logs `Could not parse HID SDP record` and tears both L2CAP channels
down.

Nothing regenerates the file on the path a controller reconnects by.
`load_info` in `src/device.c` does check for the cache file and sets
`bredr_state.svc_resolved = false` when it is absent, but that flag
only arms a discovery in `device_bonding_complete` and
`device_wait_for_svc_complete`. An incoming ACL from a device that is
already bonded reaches `device_add_connection`, which arms no
discovery, and the HID accept path in `profiles/input/server.c` calls
`hidp_add_connection` directly. So the missing file clears the
flag, and the path a controller reconnects by never reads it. An
outgoing `Device1.Connect()` does run the discovery, which is the
repair in [What is left open](#what-is-left-open).

**Measured, 2026-08-17.** On liken-1, with a real DualSense, `info`
restored and `cache/` absent, bluetoothd 5.82 logged:

    src/device.c:read_device_records() Unable to load key file from /var/lib/bluetooth/04:4A:69:66:92:27/cache/7C:66:EF:22:E7:80: (No such file or directory)
    profiles/input/device.c:hidp_add_connection() Could not parse HID SDP record: No such file or directory (2)

The bond itself restored correctly, `Trusted=true` and all. The
controller connected and dropped again, repeatedly.

**The privacy reason for the old rule still holds, and the device
directory is what enforces it.** `device_store_cached_name` writes a
cache entry for any device whose name bluetoothd resolves, paired or
not, and no code sweeps the stale ones. On a home machine that
directory is the neighbours' phones. Unpairing does not remove the
file either: `device_remove_stored` strips `[ServiceRecords]`,
`[Attributes]`, and `[Endpoints]` and keeps the rest. So a cache entry
alone never travels, and an unpaired device's leftover entry stops
travelling the moment its directory goes.

**Skip `settings`.** It is adapter preferences, and it was zero bytes
on the lab machine. It carries the adapter's powered and pairable
state, which the bluetoothd image already states as `AutoEnable=true`
in `main.conf`.

**Skip `attributes` when it is empty.** `create_file` creates it
unconditionally, and it holds only the legacy primary-services list.

For a BLE device the GATT database lives in `cache/<device>` under
`[Attributes]`, and the A2DP endpoint cache lives in the same file
under `[Endpoints]`. Both belong to a paired device, so both travel
now, as groups inside a file this design does not parse.

Nobody has weighed a real `/var/lib/bluetooth`. The one number that
matters is the ceiling:
[a Secret is limited to 1 MiB](https://kubernetes.io/docs/concepts/configuration/secret/).
This design stores the subset a restore needs and nothing else, so it
is the subset most likely to stay under that.

## Writing changes back

All of this was read in BlueZ master `bd898962`.

**Watch `Bonded`, not `Paired`.** `org.bluez.Device1` carries both as
readonly booleans. `Bonded` means the keys are stored. `Paired` is
deferred until service discovery finishes and can lag by seconds.

**Also watch `InterfacesAdded`.** When a device object is created and
bonded in the same main-loop iteration, GDBus discards the
property-changed signal. The guard is in `gdbus/object.c`, which does
not emit a property change for an interface it has not yet published.
Only `InterfacesAdded` fires in that case, and it carries `Bonded=true`
in its dictionary. Watching one signal alone misses those pairings.

**Debounce, and do not snapshot inline.** The key material is written
synchronously in the management callback: `store_link_key`,
`store_longtermkey`, and `store_irk`, all in `src/adapter.c`. Those
writes go through `g_file_set_contents`, which renames atomically, so a
reader never sees a torn file. But `[General] AddressType` is written
on a deferred `g_idle_add` path, and on restore `load_devices` reads
`AddressType` first and uses it to interpret the rest of the file. A
snapshot taken too early loses that key, and a BLE device with a static
random identity address then loads as BR/EDR and never reconnects. So
the operator waits a short settle window, reads the whole adapter tree,
compares it against the Secret, and writes only on a difference. The
loop already has a settle stage for its ResourceSlice writes, at 1500
ms with a 10 second limit, and this reuses that shape.

**Deletion keys on `Paired` going false, or on `InterfacesRemoved`.**
`Bonded` never returns to false:
`g_dbus_emit_property_changed(..., "Bonded")` appears exactly once in
`src/device.c`.

**An inotify watch on the adapter directory is optional, and
debounced the same way.** Every write lands through `rename()`, so
`IN_MOVED_TO` is a clean trigger with no torn read, and it catches a
`[General]`-only update that carries no D-Bus signal at all.

## Durability

BlueZ never calls `fsync`. GLib's `g_file_set_contents` skips its fsync
when the destination file is zero bytes, and BlueZ calls `create_file`
immediately before each write, so **the first write of a new bond gets
no fsync at all**. The containing directory is never fsynced in any
case.

A machine that loses power right after a pairing can therefore lose
that bond from the volume today, with nothing else wrong. Persisting to
the API is a durability improvement over the PVC, not a regression.

## What the failure modes cost

The owner's stated position, and it shapes the design: these are
low-stakes values, and the worst case is that somebody pairs a
controller again.

So the operator does not have to guarantee that a write landed. There
is no retry with escalation, no taint for an unpersisted bond, and no
second volume kept as a cache. A failed write is retried on the next
pass, the same as a failed slice write is today, and a bond lost
between the two costs one re-pairing.

## Storing the keys in a Secret

A link key authenticates a controller to a radio, and anybody who holds
it holds the bond. Kubernetes has a type for that, the operator already
has an API client, and a Secret needs no schema.

Two consequences are accepted rather than open.

**The keys are not encrypted at rest.** liken sets no k3s
encryption-at-rest flags, so a Secret is base64 in the datastore under
the `clusterState` role. That same store already holds k3s's TLS
material and every other Secret in the cluster, so this adds a value to
a store that is already the cluster's secret store, and it does not
create a new exposure of a different kind.

**The operator needs Secret access, which it does not have today.**
`deploy/rbac.yaml` grants `resourceslices`, `resourceclaims`, and
`nodes`, and nothing else. This design adds `get`, `create`, and
`update` on Secrets in the operator's own namespace. Because the
Secrets are namespaced to `liken-system`, that grant is a Role and a
RoleBinding there, not a widening of the ClusterRole. The init
container reads through the same service account: the ServiceAccount
admission controller mounts the projected token into init containers as
well as containers.

## The workload shape

An adapter allocates to one claim at a time. A second pod that claims
the same adapter parks Pending until the first pod releases the radio,
and an update that creates before it deletes never finishes. That
property belongs to the hardware, so it survives this plan intact, and
whatever replaces the StatefulSet has to delete before it creates.

The operator runs as a DaemonSet. Two other shapes were considered,
and this section states why each was set aside.

**A Deployment with `strategy: Recreate`.** It deletes every pod before
it creates any replacement, which satisfies the claim. Kubernetes states
the order for upgrades only: "all Pods of the old revision will be
terminated immediately. Successful removal is awaited before any Pod of
the new revision is created". A pod deleted by hand is replaced
immediately by the ReplicaSet. The cost is that every adapter in the
fleet goes down for the length of one update, instead of one at a time.

**A DaemonSet.** One adapter for each machine is the real topology, and
the operator already documents it: a machine with two adapters serves
one, because both pods would write the same ResourceSlice. A DaemonSet
states that topology directly. There is no `replicas` to keep in step
with the hardware count. Its default update strategy is `RollingUpdate`
with `maxUnavailable: 1` and `maxSurge: 0`, and Kubernetes states the
order directly: the update stops the old pods and "then brings up new
DaemonSet pods in their place". The cost is a pod on every machine that
has no adapter. That pod parks Pending, because its claim matches no
device, and a drill on liken-1 proved the Pending pod does not crash
and costs nothing.

A third shape stays on the shelf. A Deployment with `RollingUpdate` and
`maxSurge: 0` would keep the update per-pod, because `maxSurge` is the
number of pods permitted over the desired count and 0 holds the total
there. Kubernetes does not state the resulting order in those words, and
whether the scheduler reallocates a released adapter fast enough for the
update to make progress has not been tried.

The Kubernetes behavior above is verified in
[Deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/),
[Perform a Rolling Update on a DaemonSet](https://kubernetes.io/docs/tasks/manage-daemon/update-daemon-set/),
and the
[DaemonSet API reference](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/daemon-set-v1/).

The README's pairing section names the DaemonSet in its
`kubectl set env` and `kubectl exec` commands, and `deploy/operator.yaml`
carries the DaemonSet and its update strategy.

## This is not the pairing CRD

[Who owns the pairing UX](open-problems/who-owns-the-pairing-ux.md)
proposes a pairing-request resource: a person creates an object that
names an adapter and a duration, the operator opens a pairing window,
and the status reports which controller paired.

The two are separate, and the deleted document's suggestion that they
might be one feature is not carried forward. The Secret is where the
keys live: it is storage, keyed by adapter address, written by the
operator, and read once at start. The pairing CRD is about how a person
requests a pairing. One is the result and the other is the request.
They meet only in that a successful pairing writes a bond, which this
design persists whatever opened the window.

## What was considered and set aside

Each of these was priced in the deleted document. The pricing stands.

* **A PVC for each replica, which is today.** Its one real strength is
  that a volume needs no knowledge of BlueZ's layout at all. This
  design keeps most of that strength by copying whole files byte for
  byte, and pays the rest: it has to know which files a restore needs,
  and it got that set wrong once already. What the PVC costs is the
  whole of [The problem](#the-problem).
* **One shared volume keyed by the adapter address.** It gets the
  identity right without leaving the filesystem, and it needs
  `ReadWriteMany`. liken supplies volumes from the `podStorage` role
  through the local-path provisioner, which serves one machine's disk,
  so a `ReadWriteMany` class has to come from outside liken. That puts
  the bonds behind the network at the moment bluetoothd starts, for a
  dependency the operator does not have today.
* **A hostPath keyed by the adapter address.** It costs no format
  knowledge and no new code, and it fails on two counts. The state is
  node-local, so it does not survive the adapter moving machines. And
  liken has no documented durable host directory for a workload's own
  state: the machine runs a read-only squashfs image, what survives a
  reboot is only the storage roles a machine declares on a disk, and
  "the machine's RAM root backs each role that the spec does not
  declare". A bond written outside a declared role is gone at the next
  reboot. Asking liken for such a directory is a change to liken.
* **No persistence, and pair again.** The baseline. It costs a person
  one re-pairing of every controller at every pod restart, every
  operator upgrade, and every node reboot, and a DualSense's first
  pairing also needs `BLUETOOTH_CLASSIC_BONDED_ONLY` off, so a
  re-pairing is not one step.

One measurement the deleted document asked for is no longer needed. It
proposed a QEMU lab with two emulated adapters to count how often a
recreated replica allocates a different radio than its bonds were
written for, and to decide from that whether a redesign was worth it.
This plan is that redesign, and the count does not change what it
costs.

## What is left open

* **Migrating the bonds that exist on the PVC today.** Whether that is
  a one-time manual step, or something the operator does when it finds
  a populated `/var/lib/bluetooth` and an absent Secret.
* **What owns a Secret's lifetime.** A Secret named for an adapter that
  is retired stays in the namespace, and nothing collects it. Nothing
  in this design creates or deletes on behalf of an adapter that has
  left the fleet.
* **`attributes` and `ccc` are not stored.** Both are BLE state, and
  `ccc` is written only by the converters that read a BlueZ 4.x tree.
  If `attributes` turns out to matter, a person would see it as a BLE
  device that reconnects but does not deliver notifications until
  something rediscovers its services, and they would see it only after
  a pod restart, never on the first pairing. The BLE GATT database
  itself is in `cache/<device>` under `[Attributes]`, which does
  travel, so `attributes` is the legacy flat copy alone.
* **Migrating the Secrets written before `cache/<device>` travelled.**
  The reader takes both layouts, so nothing has to be done by hand,
  and the operator rewrites each Secret on its first pass that sees a
  difference. What is not decided is when the old layout stops being
  read. A Secret in the old layout restores a bond with no SDP records,
  so the first reconnect of a BR/EDR HID device after such a restore
  fails the way the drill did. Read and not measured: an outgoing
  `Device1.Connect()` should repair it without a new pairing, because
  `btd_device_connect_services` in `src/device.c` runs
  `device_discover_services` when `svc_resolved` is false, and the SDP
  browse writes the cache entry. Pressing PS does not, because that
  path never reaches `btd_device_connect_services`.
* **How often an adapter really moves between machines.** Nobody has
  counted it. This design makes the move free, so the count now sets
  how much the design is worth, and not whether it is right.
* **Two adapters on one machine.** `sliceName` names the ResourceSlice
  for the node and the driver alone, so two pods on one machine
  overwrite each other's devices. This plan does not touch that. The
  audio operator has the same naming and the same collision, and its
  [The claim takes any sound card, and a node serves only one](https://github.com/liken-sh/audio-operator/blob/main/plans/open-problems/the-claim-takes-any-sound-card-and-a-node-serves-only-one.md)
  works it through for both operators.
* **What one adapter's bonds weigh at scale.** One paired device's
  `info` measured 343 bytes on the lab adapter, so the 1 MiB ceiling on
  a Secret holds a few thousand devices and no house reaches it. What
  is unmeasured is whether any `info` grows much larger for a device
  richer than a game controller, and what `cache/<device>` weighs. The
  cache entry is the larger of the two: it carries whole SDP records
  hex-encoded, and for a BLE device it carries the GATT database as
  well.
