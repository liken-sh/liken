---
aliases: [/docs/reference/how-a-reservation-runs/]
title: How a reservation runs
weight: 30
---

# How a reservation runs

A `Reservation` gives one holder the use of one `Telescope`, from
`spec.start`, or from its creation when it has no start, until
`spec.end`, or until it is deleted when it has no end. When it starts,
the operator brings the equipment up: it starts each device's INDI
driver, connects the devices, configures them, and runs the activation
procedures. When it ends, the operator runs the deactivation
procedures, such as parking the mount and warming the cameras, stops
what it started in reverse order, and reports when the telescope is
safe to power off. The observatory's own devices keep running while
another telescope's reservation there is active.

The operator runs one step at a time, in a fixed order, and a step
starts only when the step before it is `Done` or `Skipped`. Each step
reads what the cluster and the devices report before it changes
anything, and records what it did in its summary. A step with nothing
to do is `Skipped`. A device whose driver lacks a property, such as a
guide camera with no cooler, is named in the summary, and the step
goes on.

| Step | What it does | Deadline |
|---|---|---|
| `Wait` | Waits for `spec.start`, for the telescope to exist with no delete pending on it or its observatory, and for no other reservation to hold the telescope. | none |
| `StartSite` | Starts the observatory's server and its devices, connects them, and writes the dome's `DOME_SHUTTER_PARK_POLICY` and its `MOUNT_POLICY`. Another reservation in the observatory may have started them already. Fails when two devices of the observatory name one driver. | 10 min |
| `PowerOn` | Starts the telescope's server with a link to every device, starts and connects its `Switch` devices, and switches on each output that a device's `spec.power` names. Fails when two devices of the telescope name one driver, and fails before it starts the server when the `PersistentVolumeClaim` that `spec.outbox` names does not exist. | 10 min |
| `StartDevices` | Starts the pod of every other device, and waits until each pod is Ready and its driver defines its device on the server. A device on real hardware waits here for its claim. While no node fits a pod, the summary gives the scheduler's message. | 10 min |
| `Connect` | Connects the mount, the GPS, the polar aligner, the focusers, the filter wheels, the rotators, the dust caps, the flat panels, the receivers, and the cameras, in that order. A device that another resource serves connects with that resource. Then it checks that each device defines the properties of its resource's interface, and a resource whose device does not reports `Error` while the others go on. | 2 min |
| `Configure` | Writes each setting that a device's spec states, such as a camera's gain and offset and the filter names, once for the reservation. It also writes the settings that the tree decides: the observatory's location and the `DOME_POLICY` to the mount, the location to the GPS, each camera's `ACTIVE_DEVICES` from its train, the tube's focal length and aperture, and the upload settings of each camera that mounts the [outbox](#the-outbox). It then relays the dome's park state to the mount, before a procedure unparks it. | 2 min |
| `Activation` | Runs the activation procedures, from the top of the tree down: the `Observatory`'s and its devices', unless another reservation in the observatory ran them, then the `Telescope`'s and its devices', then those of the devices of its trains. [Procedures](/docs/concepts/procedures/) states what they do. | none: each action's timeout |
| `StartGuider` | Starts the guider's pod, waits for PHD2's event server, sends `set_connected`, and waits until PHD2 reports its camera and mount connected. A telescope with no `Guider` skips it. | 10 min |
| `Abort` | Stops PHD2's exposures and guiding with `stop_capture`, then ends each exposure and stops the mount if it moves. | 2 min |
| `Deactivation` | Runs the deactivation procedures, from the bottom of the tree up: those of the devices of the telescope's trains, then the `Telescope`'s own devices' and its own. When the last telescope in the observatory ends, it then runs the observatory's devices' and the `Observatory`'s own. Every device is still connected. | none: each action's timeout |
| `StopGuider` | Deletes the guider's pod, `Service`, and `ConfigMap`, while its camera and mount are still connected. | 2 min |
| `Disconnect` | Disconnects the devices in the reverse order of `Connect`. | 2 min |
| `StopDevices` | Stops the drivers of the devices that are not `Switch` devices, and deletes their pods, the pods of the devices that a person deleted included. | 2 min |
| `PowerOff` | Switches the outputs off, except an output that a device on another server still draws from, then stops the `Switch` pods and the telescope's server. It reads each output from the reservation's `status.session`, so a device that a person deleted has its output switched off after its object is gone. | 5 min |
| `StopSite` | Disconnects the observatory's devices, switches their outputs off, except an output that a device on another server still draws from, and stops its server, unless a reservation of another telescope in the observatory is active. | 5 min |

Deactivation begins at `spec.end`, when a person deletes the
reservation, or when a person deletes its `Telescope` or that
telescope's `Observatory`, as [Deleting a running resource](#deleting-a-running-resource) describes. The finalizer `observatory.liken.sh/deactivate` holds a
deleted reservation until deactivation is done. A reservation that
reaches `spec.end` stays, `Released`, until a person deletes it.

A step that passes its deadline, or a device that answers a change with
Alert, fails the step, and the reservation is `Failed`. The `Ready`
condition names the step and the device. After a failed activation
step, the telescope stays as the steps left it, so a person can look;
deleting the reservation runs deactivation from `Abort`. After a failed
deactivation step, the finalizer stays, because the devices may not be
safe to power off. In both cases, this runs the failed step again:

```sh
kubectl annotate reservation east-tonight -n observatory observatory.liken.sh/retry=1
```

A retry is how you say that you fixed the cause. A failed activation
step often fails on a spec that the session recorded, such as a
driver that no image holds, so before the step runs again, the
operator records the session again in `status.session` from the specs
as they are then. Then the step runs on the fixed spec, and it
replaces each pod that the old record built. The observatory's part
of the record still holds the devices that another reservation's
running session recorded there, and the telescope's part leaves out a
device that another session started on the observatory's server.

The retry also undoes what the old record started and the new record
does not, before any step runs again. A device that the new record
leaves out, such as one you moved to another telescope, leaves the
session: its `deactivation` runs if the telescope is `Active`, its
driver stops, its pod goes, and its `PodDeleted` Event says that the
retry took it out of the session. An output that the old record
switched on and the new one does not goes off, unless another device
draws from it. Then the steps run again from the first step that reads
a part of the record that changed: `StartSite` for the observatory's
devices, `PowerOn` for the telescope's devices and the outbox,
`Configure` for the trains and tubes, and `StartGuider` for the `Guider`. So a power output
that you changed is switched on, and the `Retry` Event names the first
step that runs again. A retry of a failed deactivation step records
nothing, because the release stops what the session started.

A trigger's run that failed, such as a dome park that the driver
refused, is not run again for the same transition. The same annotation
on the resource runs it again, once the cause is fixed:

```sh
kubectl annotate dome lab -n observatory observatory.liken.sh/retry=1
```

The operator runs again each `Failed` run of the resource's
`spec.triggers` whose condition still holds with the same transition
time, skips the actions that are `Done`, and removes the annotation.
A run whose condition changed since stays `Failed`. The annotation on
a device also runs again its `Failed` `activation` that ran after the
`Activation` step, such as after the device defined its interface's
properties late. Any other run of `activation` or `deactivation` runs
again through the retry annotation of its reservation.

One telescope serves one reservation at a time. A second reservation
of the telescope waits in `Wait`, and its summary names the reservation
it waits for. Waiting reservations take the telescope in the order of
their `spec.start`, and of their creation when they have none.

While a reservation is `Ready`, the operator creates again each pod
that is deleted. When a device's driver comes back on the server
disconnected, after its pod or the server restarted, the operator
connects it and writes its settings again. A device that a person
disconnects in KStars stays disconnected. The operator also creates
the guider's pod again, and connects the camera and the mount of each
new PHD2 once.

INDI names a device after its model, so two devices on one server
with the same driver define one device, and the second driver breaks
the first. Activation refuses such a pair: `StartSite` or `PowerOn`
fails, and its message names both devices and the driver, so a
reservation never becomes `Ready` without its imaging camera.

While such a pod is gone, its device is `Starting`, and the `Ready`
message of the device or its `Guider` reads `Creating pod <name>`.
During activation, before the steps create the pod, the device's
message reads `Waiting for activation to create pod <name>`.
When the operator creates the pod, it records a `PodCreated` Event on
the device, the `Guider`, or the `Telescope` or `Observatory` whose
INDI server the pod runs, and writes one line to its log. A new PHD2
starts idle and not calibrated, so the holder calibrates and starts
guiding again. A new INDI server starts each driver disconnected, and
the operator connects each device again.

Each resource except a `Reservation` posts an Event each time a condition first
appears or changes its status or its reason, with the condition's
reason and message. A `ParentFound` that is `False`, a `Ready` whose
reason is `Error`, and a `Safe` that is `False` are `Warning`s. `kubectl describe` lists them
for an hour.

## The outbox

A camera's driver writes a frame to a file only when a client asks it
to keep frames, and it writes the file to the pod's `/tmp`, which the
pod loses when it stops. To keep the frames, name a
`PersistentVolumeClaim` of the reservation's namespace in
`spec.outbox`:

```yaml
spec:
  telescope: east
  holder: desktop
  outbox:
    claimName: east-frames
```

The claim is yours, so the frames stay from night to night in the
place you chose. A volume on the machine at the pier keeps the frames
off the network while the night runs. A network share works too, but a
frame written straight to a share can make the next exposure wait,
and the wait repeats all night.

`PowerOn` reads the claim before it creates a pod, and fails with a
summary that names the claim and the namespace when the claim does
not exist. Each `Camera` of the telescope mounts the claim at
`/outbox`, except a camera that a `Guider` guides with, because that
camera sends its frames to PHD2 and writes no file. The driver runs as
user and group 1000, and a new volume is often owned by root, so the
camera's pod sets `fsGroup: 1000` with `fsGroupChangePolicy:
OnRootMismatch`. The kubelet then gives the driver's group write
access, and walks a large volume only when the owner of its root does
not match. A claim that exists and that the camera's node cannot
mount, such as a `ReadWriteOnce` volume bound on another node, leaves
the camera's pod `Pending`, and the summary of `StartDevices` gives
the scheduler's message.

`Configure` then sets the driver's `UPLOAD_DIR` to `/outbox` and its
`UPLOAD_MODE` to `UPLOAD_BOTH`, so each frame lands in the outbox and
KStars still receives it. It leaves `UPLOAD_PREFIX` as the driver has
it. The holder can change the mode and the directory afterward: Ekos
writes its own upload settings when it captures, and
`astrophotography-operator` chooses a directory for each target. With
no outbox, the operator does not touch the upload settings.

The session records the outbox, so an edit of `spec.outbox` takes
effect at the next session. A new pod in the middle of an exposure
would lose the exposure. Two telescopes whose reservations name the
same claim both write to its root, and two drivers that write at the
same moment can choose the same file name, so give each telescope its
own claim.

## Editing devices during a session

From the step that starts a telescope's INDI server, `PowerOn`, until
the reservation is `Released`, the server runs the devices that the
tree placed on it when it started, and no others. The observatory's
server does the same from `StartSite`, while a reservation of any of
its telescopes holds it. The operator refuses no edit, and an edit
that would change which devices a server runs takes effect when the
session ends:

- A device that a person moves to another telescope, another train,
  or the shelf stays where it runs. Its driver, its pod, and its power
  do not change, and it stays connected. Its `ParentFound` condition
  is `True` with the reason `MovePending` and a message such as
  `Moves to OpticalTrain west-imaging when Reservation east-tonight
  ends`, and the condition's Event records the edit. The release
  stops the device as one of its own: its `deactivation` runs, it
  disconnects, `PowerOff` switches its output off, and its pod goes.
  The next session of the telescope that the device names starts it
  there. A train that a person deletes leaves its devices on the
  server the same way.
- Any other edit of a device that the session started, such as of its
  `spec.power`, its driver, or its claim, waits for the end of the
  session too. So does an edit of the `Telescope`'s
  `spec.observatory`, of its `OpticalTrain`s, of the `OpticalTube`s of
  those trains, of its `Guider`, and of the reservation's
  `spec.outbox`. Before the session starts a
  server, the operator records in the reservation's `status.session`
  what it acts on: each device's parents, driver, power, and claim,
  whether a `Guider` guides with it, the telescope's observatory,
  trains, tubes, and `Guider`s, and the reservation's outbox. `StartSite` records the observatory
  and the devices of its server, and `PowerOn` records the rest. Each
  step writes its part once, and only the retry of a failed activation
  step writes it again, from the specs as they are then. Until
  the session ends, the operator reads the record and not the spec. So the release switches off the output that the
  session switched on, a camera whose driver comes back gets the
  `SCOPE_INFO` of the tube that the session started with, and PHD2
  keeps its pod and its calibration. A tube's status still shows the
  lengths in its spec.
- A device's settings, such as a camera's gain, the `Observatory`'s
  location, and the procedures of every resource are not in the
  record, because a person changes them during a session on purpose.
  The operator writes the settings and the location from the spec
  when a device's driver comes back.
- A device that a person adds to the telescope, a train, or the
  observatory of a session, by creating it or by naming its parent,
  does not start. That includes an observatory that had no devices
  when the session started. It is `Idle`, and its `Ready` message reads
  `Starts at the next reservation, after Reservation east-tonight
  ends`. The next session starts it. A resource that another
  resource's driver serves, such as the `FilterWheel` of a camera,
  waits the same way, and so does a `Guider` that a person creates
  for the telescope. The record names each object by its UID, so a
  device, a train, a tube, or a `Guider` that a person deletes and
  creates again with the same name is a new object, and it waits too.
- A device that a person deletes while the reservation is `Ready`
  leaves at once, as
  [Deleting a running resource](#deleting-a-running-resource)
  describes. An `OpticalTrain`, an `OpticalTube`, or a `Guider` that a
  person deletes is gone at once too: a `Guider` whose train is gone
  turns to `Error`, and a camera whose train or tube is gone gets no
  `SCOPE_INFO` when its driver comes back.

An eviction does not change the set either. The record is in the
reservation and not on the pods, so when an eviction deletes one pod
of the server or every pod, the operator creates the pods again from
the record. They run the drivers, the power, and the claims that the
session started with, and a device added during the session still
waits.

The reservations of several telescopes in one observatory share its
server, and each reservation writes only its own status. So each one
records the observatory's devices when its `StartSite` begins. A
reservation that starts while another holds the server records the
devices that the other recorded, and the operator reads the union of
the records of every reservation that holds its telescope. The record
of the observatory's server stays while any of them holds it, in
whatever order they end. The operator reads a reservation's record
only while the reservation holds its telescope, so the record of a
`Released` reservation changes nothing.

So each device runs on one server at a time. A device that moved
between two running servers would need one server to release it while
the other started it, and the steps of both reservations to agree on
where it was at each moment. A device on a telescope with no active
reservation changes nothing that runs.

## Deleting a running resource

The operator adds the finalizer `observatory.liken.sh/deactivate` to
each resource that it runs something for, so a delete waits until the
operator stopped it:

| Resource | Holds the finalizer |
|---|---|
| `Reservation` | from its first step until its deactivation steps are done |
| A device | from just before the operator creates its pod until the pod is gone and no held server runs the device |
| `Telescope` | from when a reservation takes it in `Wait` until that reservation is `Released` |
| `Observatory` | while a reservation holds any of its telescopes |

A device that a person deletes while a reservation is `Ready` leaves
its server, and the server and the other devices on it keep running.
When its `Telescope` or `Observatory` is `Active`, and the device ran
its `activation`, the operator first runs its `deactivation` through
its driver, so a dust cap closes and a dome parks. A `deactivation`
that fails or times out posts a `ProcedureFailed` Warning, and the
delete goes on. A device that is not connected skips its actions.
Then the operator stops the device's driver on the running server and
posts a `DriverStopped` Event on the `Telescope` or the `Observatory`.
It deletes the device's pod, its `Service`, and its `ResourceClaim`,
and switches off the output that the device's `spec.power` names,
unless another device that a running server runs names the same
output. The `PodDeleted` Event on the device says whether the output
went off. Then the operator removes the finalizer, and the device is
gone. A device deleted while the reservation activates or is `Failed`
goes at a retry, when the reservation is `Ready`, or in its release. A
device deleted before the `Activation` step ran its `activation` goes
with no procedure.

When a person deletes a reservation together with devices or the
resources that place them, as `kubectl delete -f` of a whole inventory
does, the reservation's deactivation steps find each device on the
server where its driver's pod still runs, and not through its parents.
This holds for a deleted device and for a device that stays. A device
that names a `spec.opticalTrain` runs in the train tier, and any other
device runs in the tier of its telescope or observatory. An
`OpticalTrain` carries no finalizer, so it is gone at once. Its dust
cap still closes, its flat panel still goes dark, and its camera still
aborts its exposure, warms, and disconnects in order, whether a person
deleted those devices too or not. A deleted `Dome` parks the same way.
The server's record names each such device, and the finalizer keeps a
deleted one while its pod runs, so `StopDevices` stops the driver of a
deleted device and leaves its pod. `PowerOff` then switches off the
output that the record names, and deletes the pod before the server's
pod. A deleted `Switch` switches
its outputs off the same way, and so does a `Switch` that a person
moves to another telescope during the session, because its pod still
runs on the server it left. A deleted `Dome` disconnects in
`StopSite` before the observatory's server stops. A `Guider`
carries no finalizer either, so it is gone at once, and `Abort` finds
its PHD2 by the guider's pod on the telescope's server, and stops it
there.

A `Telescope` that a person deletes during a session ends the
reservation that holds it, as `spec.end` does. The deactivation steps
run, from `Abort`, on the telescope and its devices, which stay while
the finalizer holds them. The summary of `Abort` begins with
`Telescope east was deleted`, and the `Deactivating` Event on the
reservation ends with the same words. When the reservation is
`Released`, the operator removes the finalizer, and the telescope is
gone. A deleted `Observatory` ends the reservation of each of its
telescopes the same way, and goes when the last one is `Released`.
Each device of a deleted telescope or observatory stays: only the
resource that a person deleted goes.

A new reservation of a telescope that is being deleted, or whose
observatory is being deleted, waits in `Wait`, and its summary reads
`Telescope east is being deleted`. When the telescope is gone, the
summary reads `Missing Telescope east`, as for a telescope that never
existed.

A device with no pod, such as one on the shelf, one of a telescope
with no reservation, or one whose reservation is `Released`, carries
no finalizer, and a delete removes it at once. So do a `Telescope`
that no reservation holds and an `Observatory` with no held
telescope.

While the operator is down, a delete of a running resource waits for
it, and the operator does the work when it returns. A person can
remove the finalizer by hand:

```sh
kubectl patch dustcap east -n observatory --type=merge -p '{"metadata":{"finalizers":null}}'
```

That skips the device's `deactivation`. Kubernetes then deletes its
pod, its `Service`, and its `ResourceClaim` by garbage collection,
before the operator stops its driver on the running server. The same
patch on a held `Telescope` lets it go at once, and Kubernetes deletes
its server's pod and `Service`. The reservation runs on until it
ends, and then reads a missing telescope: its `Deactivation` step
skips every procedure, and
`StopSite` leaves the observatory's server to the operator's sweep,
which stops it with no procedure. On an `Observatory`, the patch lets
it go at once, and the reservation runs on until it ends. Its
`Deactivation` step then reads a missing observatory and skips every
procedure.
