# 04, An API for pairing

Built, and drilled on liken-1 on 2026-08-17.

This plan answers the open problem "Who owns the pairing UX", and this
document replaces it.

## The problem

Pairing today is three commands: a `kubectl set env` that restarts the
workload, a `kubectl exec` into the pod for an interactive
`bluetoothctl` session, and a second `set env` to put the guard back.
The grant that flow needs, exec into `liken-system`, is broader than
pairing. The flow leaves no record of who paired what. Forgetting the
third command leaves `ClassicBondedOnly` off with no error and no sign,
which reopens CVE-2023-45866.

Unpairing has the same shape and no documentation at all: another exec,
`bluetoothctl remove`.

Two open questions from earlier plans land here as well. Plan 03 left
open what owns a bond Secret's lifetime, so a Secret for a retired
adapter stays in the namespace forever. The restore-set open problem
notes that no Secret stores the adapter's own identity file.

## The design

Three objects in `bluetooth.liken.sh/v1alpha1`. One rule decides what
becomes an object and what stays status: spec is desired state, status
is observed state, and a session is never an object. Radio observations
appear only in the status of an object a person created.

### Adapter

Cluster-scoped, created by the operator, named by the adapter's MAC in
DNS-label form (`04-4a-69-66-92-27`). It is the root of the ownership
tree: Pairings belong to it, and each Pairing owns its bond Secret, so
retiring a dead radio is one delete that collects every bond keyed to
it.

```yaml
apiVersion: bluetooth.liken.sh/v1alpha1
kind: Adapter
metadata:
  name: 04-4a-69-66-92-27
spec:
  alias: liken-1-living-room
status:
  address: "04:4A:69:66:92:27"
  node: liken-1
  powered: true
```

`spec.alias` reconciles into BlueZ's `Adapter1.Alias`, which is the
name the radio broadcasts about itself. During a discoverable window,
other devices see the adapter under this name.

The Adapter is not owned by the Node and not owned by liken's Machine.
An owner reference binds to one UID, a reinstall re-registers the Node
under a new UID, and garbage collection would then sweep the Adapter
and every bond under it as a side effect of reinstalling the OS. The
adapter's lifetime is also not the machine's: a dongle carried to
another machine takes its Adapter, its Pairings, and their Secrets with
it, because nothing in the tree names a machine. Where the adapter is
right now is status.

A finalizer refuses deletion while the radio is present and claimed, so
`kubectl delete adapter` on live hardware is an error instead of a mass
unpair. Deleting the Adapter of a radio that is gone removes its
Pairings and their Secrets through the owner references, which is the
intended cleanup for retired hardware.

The Adapter is also where the adapter's own identity Secret will
belong, the IRK that the restore-set open problem names. This plan does
not build that Secret. It only states where it will belong.

### Pairing

Cluster-scoped, created by the operator when a pairing succeeds, owned
by its Adapter, named for the device the way the ResourceSlice names it
(`a0-ab-51-33-b7-12`). It is the durable fact: this device holds a bond
with this adapter. It owns the Secret that carries that one bond.

```yaml
apiVersion: bluetooth.liken.sh/v1alpha1
kind: Pairing
metadata:
  name: a0-ab-51-33-b7-12
  ownerReferences: [{kind: Adapter, name: 04-4a-69-66-92-27, ...}]
spec:
  alias: player-one-pad
  trusted: true
status:
  address: "A0:AB:51:33:B7:12"
  deviceName: "DualSense Wireless Controller"
  adapter: "04:4A:69:66:92:27"
  connected: true
  secret: liken-system/bluetooth-bond-a0-ab-51-33-b7-12
  pairedAt: "2026-08-17T17:30:41Z"
  request: liken-system/new-gamepad
```

`spec.trusted` reconciles into `Device1.Trusted`, which is what lets
the device reconnect on its own. `spec.alias` reconciles into
`Device1.Alias`; bluetoothd stores the alias in the bond's own info
file, so the per-bond Secret carries the name with the keys.

Deleting a Pairing is the unpair API. A finalizer runs the ordered
teardown: disconnect the device, let the slice taints and the claim
eviction run, retire the slice device only after its claim releases,
remove the bond from bluetoothd, and let the Secret collect through its
owner reference. A device a claim still names never leaves the slice,
which is the rule every device operator here follows.

A Pairing whose bond disappears from bluetoothd is not deleted by the
operator. Deletion means unpair, so the operator only reports the state
in status and a person decides.

### PairingRequest

Namespaced, created by a person, short-lived. It is the act, and it
doubles as discovery: the scan and the pairing window are one radio
session, so the address a person approves is one the radio is seeing
now.

```yaml
apiVersion: bluetooth.liken.sh/v1alpha1
kind: PairingRequest
metadata:
  name: new-gamepad
  namespace: liken-system
spec:
  adapter: 04-4a-69-66-92-27
  windowSeconds: 180
  device: ""            # empty: scan and report. set: pair this one.
  ttlSecondsAfterFinished: 86400
status:
  phase: Open           # then Paired or Expired
  windowClosesAt: "2026-08-17T17:31:24Z"
  seen:
    - address: "A0:AB:51:33:B7:12"
      name: "DualSense Wireless Controller"
      firstSeen: "2026-08-17T17:29:02Z"
  pairing: a0-ab-51-33-b7-12
```

The flow for a new device: create the request, put the controller in
pairing mode, watch `status.seen`, and approve by writing the address
into `spec.device`. Writing spec is the approval, the same way editing
a Deployment approves a rollout. An empty `spec.device` never pairs
anything, because pairing whatever responds first is the one behavior
that can bond a stranger's device.

Re-pairing a known device is the same request with `spec.device` set
from the start. Setting the address at creation approves it in
advance, so no second write is needed.

A finished request stays long enough to read the next morning and is
collected after `ttlSecondsAfterFinished`, the same convention a Job
uses. The Pairing's status records which request created it and when,
so that record outlives the request.

`status.seen` is written from radio observations, so it is bounded: the
list caps at 16 entries and names truncate at 64 characters, the same
limit the ResourceSlice puts on a string attribute. Requests are
namespaced so that RBAC can grant "may create PairingRequests" without
granting exec, which is the narrower grant the open problem asked for.
Approval is not a separate privilege: custom resources carry only the
status and scale subresources, so whoever can update a request can
approve it. For this fleet that is acceptable, and the split would be a
second object nobody needs yet.

## Adoption

On every startup the operator reconciles the objects from what
bluetoothd holds, in both directions. A bond with no Pairing gets one
created, with its per-bond Secret. A radio with no Adapter gets one. A
Pairing with no bond keeps its object and reports the gap in status.

Adoption is also the whole migration. The existing DualSense bond and
the per-adapter Secret from plan 03 become a Pairing and a per-bond
Secret on the first startup of the new operator, and the migration code
is the same code every later startup runs.

## What this changes in plan 03

One Secret per bond replaces one Secret per adapter. Each Secret is
named `bluetooth-bond-<device>` in the operator's namespace, holds that
device's `info` and `cache` files, carries a label naming its adapter,
and lists its Pairing as owner. `bondfetch` lists Secrets by the
adapter label instead of reading one Secret by name. Everything else in
plan 03, which files are stored and why, is unchanged.

## ClassicBondedOnly stays on

The guard stays at its secure default and the operator never touches
it. The interactive `bluetoothctl` flow raced the controller's input
connection against the bond, and the guard rejected the input
connection. The operator initiates pairing itself over D-Bus, scan then
`Device1.Pair()` with its own agent, which is the same sequence a
desktop GUI runs. In that sequence the bond registers before any input
channel opens. The first drill pairs with the guard on to prove this on
this hardware. If the drill fails, the flip returns as an internal step
of the window, and the API does not move.

The operator's agent registers the NoInputNoOutput capability, which
pairs a DualSense. A device that needs a passkey displayed and typed is out
of scope for v1alpha1, and the request's status is where a passkey
would go when that day comes.

## What was considered and set aside

* **One permanent object with a reopenable window.** A first pairing
  cannot name its device, because the address is what the scan
  discovers, so the object's identity would live in a field the user
  never wrote. Separate objects give the act and the fact each a spec
  that its author really wrote.
* **A Connection object.** Sessions come and go on their own schedule
  and carry no desired state. Connection state lives in the
  ResourceSlice and in Pairing status.
* **Device-initiated pairing.** A DualSense in pairing mode goes
  discoverable and waits to be found; it never initiates. The paths
  that do exist require an always-pairable adapter, which is the
  exposure the window exists to bound.
* **Adapter owned by the Node or the Machine.** The UID sweep on
  reinstall, the portability of a dongle, and the operator's
  no-liken-interface contract all argue against it. Stated above.
* **A separate Scan object.** It splits one radio session into two
  windows that must stay aligned, and the device's pairing mode times
  out between them. Letting a request expire unapproved is scan-only
  already.
* **Automatic deletion of a Pairing whose bond vanished.** Deletion is
  unpair, an act with a finalizer and consequences. The operator
  reports; a person deletes.

## What the drill showed

All four drills ran on liken-1 on 2026-08-17, against the release
2026.08.17-020 and a DualSense.

1. **A first pairing, guard on, passed.** The window's scan reported
   three devices in `status.seen`: the flashing DualSense and two
   strangers in radio range. The empty `spec.device` paired none of
   them. Approval by patch reached `Paired` in about ten seconds, the
   Pairing and its Secret appeared, the slice published the
   controller, and the consumer that was parked on the taint started
   on its own. `ClassicBondedOnly` stayed at `true` throughout, so
   the fallback flip was never needed.
2. **Adoption passed.** On the release's first roll, the pre-existing
   bond became a Pairing and a per-bond Secret with the Pairing as
   owner, and the DualSense reconnected on its own ten to twenty
   seconds after bluetoothd restarted.
3. **Unpair while claimed passed.** Deleting the Pairing while a pod
   held the prepared claim ran the ordered teardown in about 95
   seconds: disconnect, taints, eviction at the 30 second toleration,
   unprepare, the device retired from the slice, the bond removed,
   and the Secret collected with the object. The same drill taught a
   consumer lesson: a consumer under a Deployment must claim through
   a ResourceClaimTemplate, because a standing claim keeps its
   allocation across the eviction, the NoSchedule taint blocks only
   new allocations, and the ReplicaSet loops through pods that
   schedule and are evicted at once.
4. **TTL passed.** The finished request was collected on its
   `ttlSecondsAfterFinished` schedule.
