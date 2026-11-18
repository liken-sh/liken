---
aliases: [/docs/reference/how-a-reservation-runs/]
title: How a reservation runs
weight: 30
---

# How a reservation runs

A `Reservation` gives one holder, the person or program that
`spec.holder` names, the use of one `Telescope`. It starts
at `spec.start`, or when you create it if it has no start. It ends at
`spec.end`, or when you delete it if it has no end. When it starts, the
operator starts each device's INDI driver, connects the devices,
configures them, and runs the activation procedures. When it ends, the
operator runs the deactivation procedures, such as parking the mount
and warming the cameras, stops everything it started in reverse order,
and reports when the telescope is safe to power off. The observatory's
own devices keep running while a reservation of another telescope in
that observatory is active.

The operator runs the steps below one at a time, in this order. A step
starts only when the step before it is `Done` or `Skipped`, and it
records what it did in its summary. A step with nothing to do is
`Skipped`. When a device's driver lacks a property, such as a guide
camera with no cooler, the summary names the device and the step goes
on.

| Step | What it does | Deadline |
|---|---|---|
| `Wait` | Waits until `spec.start` has passed, the telescope exists, and neither the telescope nor its observatory is being deleted. It also waits for each condition in the `spec.requires` of the `Observatory` and of the devices on its server, and until no other reservation holds the telescope. | none, but [dawn](#requirements-and-dawn) ends it |
| `StartSite` | Starts the observatory's server and its devices, connects them, and writes the dome's `DOME_SHUTTER_PARK_POLICY` and `MOUNT_POLICY`. Another reservation in the observatory may have started them already. Fails when two devices of the observatory use the same driver. | 10 min |
| `Requires` | Waits for each condition in the `spec.requires` of the `Telescope` and of the devices on its server, such as a person's `SetUp` or a `WeatherStation`'s `Safe`. Nothing of the telescope runs yet. `Skipped` when nothing is required. | none, but [dawn](#requirements-and-dawn) ends it |
| `PowerOn` | Starts the telescope's server, starts and connects its `Switch` devices, and switches on each output that a device's `spec.power` names. Fails when two devices of the telescope use the same driver, and fails before it starts the server when the `PersistentVolumeClaim` in `spec.outbox` does not exist. | 10 min |
| `StartDevices` | Starts the pod of every other device, and waits until each pod is Ready and its driver defines its device on the server. A device on real hardware waits here for its claim. While no node can run a pod, the summary gives the scheduler's message. | 10 min |
| `Connect` | Connects the mount, the GPS, the polar aligner, the focusers, the filter wheels, the rotators, the dust caps, the flat panels, the receivers, and the cameras, in that order. A device that another resource's driver serves connects with that resource. Then it checks that each device defines the properties of its resource's interface. A device that does not is `Error`, and the others go on. | 2 min |
| `Configure` | Writes each setting in a device's spec, such as a camera's gain and offset and the filter names. It also writes the observatory's location and `DOME_POLICY` to the mount, the location to the GPS, each camera's `ACTIVE_DEVICES` from its train, the tube's focal length and aperture, the upload settings of each camera that mounts the [outbox](#the-outbox), and the [FITS keywords](/docs/concepts/fits-keywords/) of each imaging camera. Then it copies the dome's park state to the mount, before a procedure unparks it. | 2 min |
| `Activation` | Runs the activation procedures from the top down: those of the `Observatory` and its devices, unless another reservation in the observatory already ran them, then those of the `Telescope` and its devices, then those of the devices of its trains. [Procedures](/docs/concepts/procedures/) describes them. | none, but each action has a timeout |
| `StartGuider` | Starts the guider's pod, waits for PHD2's event server, sends `set_connected`, and waits until PHD2 reports its camera and mount connected. `Skipped` for a telescope with no `Guider`. | 10 min |
| `StopWork` | Stops whatever the equipment is still doing: PHD2's exposures and guiding with `stop_capture`, then each exposure, then the mount if it moves. `Skipped` when nothing runs. | 2 min |
| `Deactivation` | Runs the deactivation procedures from the bottom up: those of the devices of the telescope's trains, then those of the `Telescope`'s own devices and the `Telescope`. When the last active telescope in the observatory ends, it then runs those of the observatory's devices and the `Observatory`. Every device is still connected. | none, but each action has a timeout |
| `StopGuider` | Deletes the guider's pod, `Service`, and `ConfigMap`, while its camera and mount are still connected. | 2 min |
| `Disconnect` | Disconnects the devices in the reverse order of `Connect`. | 2 min |
| `StopDevices` | Stops the drivers of every device except the `Switch` devices, and deletes their pods, including the pods of devices that a person deleted. | 2 min |
| `PowerOff` | Switches the outputs off, except an output that a device on another server still uses, then stops the `Switch` pods and the telescope's server. It reads the outputs from the reservation's `status.session`, so it also switches off the output of a device that a person deleted. | 5 min |
| `StopSite` | Only when no reservation of another telescope in the observatory is active: disconnects the observatory's devices, switches their outputs off, except an output that a device on another server still uses, and stops the observatory's server. | 5 min |

Deactivation starts when `spec.end` passes, when a person deletes the
reservation, or when a person deletes its `Telescope` or that
telescope's `Observatory`, as [Deleting a running
resource](#deleting-a-running-resource) describes. The finalizer
`observatory.liken.sh/deactivate` keeps a deleted reservation until
deactivation is done. A reservation that reaches `spec.end` stays in
the cluster as `Released` until you delete it.

When a step passes its deadline, or a device sets a property to Alert
after a change, the step fails and the reservation is `Failed`. The
`Ready` condition names the step and the device. After a failed
activation step, the operator leaves the telescope as it is, so you can
look at it. Deleting the reservation then runs deactivation from
`StopWork`. After a failed deactivation step, the finalizer stays,
because the devices may not be safe to power off. In both cases, fix
the cause and then run the failed step again with this command:

```sh
kubectl annotate reservation east-tonight -n observatory observatory.liken.sh/retry=1
```

An activation step often fails because of a spec, such as a driver that
no image contains. So before a retry of a failed activation step, the
operator records the session in `status.session` again from the
current specs, and the steps run on the corrected spec. Devices that
another reservation's session started on the observatory's server stay
in the observatory's part of the record, and stay out of the
telescope's part.

The retry then stops what the old record started and the new record
leaves out. If you moved a device to another telescope, its
`deactivation` runs if the telescope is `Active`, its driver stops, its
pod is deleted, and its `PodDeleted` Event says that the retry removed
it from the session. An output that the new record does not need is
switched off, unless another device uses it. Then the steps run again
from the first step that reads a part of the record that changed:

- `StartSite` for the observatory's devices.
- `PowerOn` for the telescope's devices and the outbox.
- `Configure` for the trains, the tubes, and the FITS keywords.
- `StartGuider` for the `Guider`.

The `Retry` Event names the first step that runs again. A retry of a
failed deactivation step records nothing new, because deactivation
stops only what the session started.

A procedure run of a trigger that failed, such as a dome park that the
driver refused, does not run again for the same transition. After you
fix the cause, put the same annotation on the resource:

```sh
kubectl annotate dome lab -n observatory observatory.liken.sh/retry=1
```

The operator runs each `Failed` run of the resource's `spec.triggers`
again if its condition still has the same transition time, skips the
actions that are `Done`, and then removes the annotation. A run whose
condition has changed since then stays `Failed`. On a device, the
annotation also runs again a `Failed` `activation` that ran after the
`Activation` step, such as when the device defined its interface's
properties late. To run any other `activation` or `deactivation`
again, retry its reservation.

A telescope serves one reservation at a time. A second reservation of
the same telescope waits in `Wait`, and its summary names the
reservation it waits for. Waiting reservations get the telescope in the
order of their `spec.start`, or in the order of their creation when
they have no start.

While a reservation is `Ready`, the operator creates each deleted pod
again, including the guider's pod. While a pod is missing, its device
is `Starting`, and the `Ready` message of the device or its `Guider`
reads `Creating pod <name>`. When the operator creates the pod, it
posts a `PodCreated` Event on the device, the `Guider`, or the
`Telescope` or `Observatory` whose INDI server the pod runs. A new INDI
server starts each driver disconnected, so the operator connects each
device and writes its settings again. It does the same for a driver
that restarts. A device that a person disconnects in KStars stays
disconnected. A new PHD2 starts idle and not calibrated. The operator
connects its camera and mount once, and the holder must calibrate and
start guiding again. During activation, before its step creates the
pod, the device's message reads `Waiting for activation to create pod
<name>`.

INDI names a device after its model, so two devices with the same
driver on one server would define one INDI device, and the second
driver would break the first. So `StartSite` or `PowerOn` fails, and
its message names both devices and the driver.

Each device, `Telescope`, `Observatory`, `Guider`, `OpticalTrain`, and
`OpticalTube` posts an Event each time a condition first appears or
changes its status or its reason. The Event has the condition's reason
and message. The transition is a `Warning` Event when `ParentFound` is
`False`, `Ready` is `False` with the reason `Error`, `StepFailed`, or
`StepTimedOut`, `Safe` or `HorizonFound` is `False`, or
`AttentionNeeded` is `True`. `kubectl describe` lists Events for an
hour. Your own write of `SetUp` posts no Event, and its expiry posts a
`Normal` Event with the reason `Expired`.

## Requirements and dawn

A resource with procedures can list conditions in `spec.requires` that
must be true before a session starts it, much as a pod's
`readinessGates` must be true before the pod is ready. A portable
telescope requires the `SetUp` condition, which a person writes after
the telescope is carried out and polar aligned. An observatory can
require a `WeatherStation`'s `Safe`, so nothing powers on in the rain.
[Set up a portable
telescope](/docs/guides/set-up-a-portable-telescope/) gives the steps.

The operator checks the requirements in two steps, because the two INDI
servers start in different steps. `Wait` checks the requirements of the
`Observatory` and of the devices on its server, before the reservation
gets its telescope, so the telescope and its devices stay `Idle`.
`Requires` checks the requirements of the `Telescope` and of its
devices, after `StartSite`. So a telescope can require the `Safe` of a
weather station on the observatory's server, because the station
reports `Safe` only after `StartSite` connects it. The same requirement
on the `Observatory` would wait until dawn, because the station would
never start. The summary of each step lists every condition it waits
for, such as `Waiting for Telescope east SetUp=True, WeatherStation lab
Safe=True`. A requirement on a resource that does not exist is listed
too, such as `spec.requires[0] of Telescope east: no WeatherStation
roof`.

A requirement controls only whether a session starts. When a condition
turns `False` while the equipment runs, a
[trigger](/docs/concepts/procedures/#triggers) can act on it, such as
parking the dome in bad weather.

A reservation that is still waiting in either step at the first
astronomical dawn after its wait began ends. Astronomical dawn is when
the sun rises through 18 degrees below the horizon at the
`Observatory`'s `spec.location`. The wait in `Wait` begins at
`spec.start`, or when the step starts if that is later. The wait in
`Requires` begins when the step starts. At dawn the step is `Skipped`
with a summary such as `Dawn came at 2026-10-13T08:44:52Z while waiting
for Telescope east SetUp=True, so the session did not start`. The
reservation deactivates and becomes `Released`, and the telescope's
equipment never powers on. The `Deactivating` and `Released` Events
give the same reason. Where the sun stays above 18 degrees below the
horizon all night, as in a high-latitude summer, the night ends at the
sun's lowest point. In a polar night, where the sun never rises that
far, it ends at the sun's highest point.

## The outbox

A camera's driver saves a frame to a file only when a client asks it
to, and it writes the file to the pod's `/tmp`, which is lost when the
pod stops. To keep the frames, set `spec.outbox` to a
`PersistentVolumeClaim` in the reservation's namespace:

```yaml
spec:
  telescope: east
  holder: desktop
  outbox:
    claimName: east-frames
```

You own the claim, so the frames stay from night to night in the
storage you chose. A volume on the machine at the pier keeps the frames
off the network during the night. A network share also works, but a
slow write to the share can delay the next exposure.

When the claim does not exist, `PowerOn` fails before it creates a pod,
with a summary that names the claim and the namespace. Each `Camera` of
the telescope mounts the claim at `/outbox`, except a camera that a
`Guider` uses, because that camera sends its frames to PHD2 and writes
no file. The driver runs as user and group 1000, and a new volume is
often owned by root, so the camera's pod sets `fsGroup: 1000` with
`fsGroupChangePolicy: OnRootMismatch`. The kubelet then gives group
1000 write access to the volume, and changes ownership across the
volume only when its root directory has the wrong owner. When the
camera's node cannot mount the claim, such as a `ReadWriteOnce` volume
that is bound on another node, the camera's pod stays `Pending`, and
the summary of `StartDevices` gives the scheduler's message.

The claim's access mode decides which pods can mount it at once. Every
pod on the volume's node can mount a `ReadWriteOnce` claim. Only one
pod in the cluster can mount a `ReadWriteOncePod` claim, so it works
only for a telescope with one imaging camera and a holder that mounts
nothing: a second imaging camera's pod stays `Pending`. When
`astrophotography-operator` runs the session, its worker pod also
mounts the outbox, and a `ReadWriteOncePod` claim leaves the worker
`Pending`. Use `ReadWriteOnce` or `ReadWriteMany`, as [Image a
target](https://liken.sh/astrophotography/docs/guides/image-a-target/)
does.

`Configure` sets the driver's `UPLOAD_DIR` to a directory named after
the camera inside the outbox, such as `/outbox/east-main`, and sets
`UPLOAD_MODE` to `UPLOAD_BOTH`, so each frame is saved in the outbox
and KStars still receives it. The driver creates the directory when it
saves the first frame. A driver numbers its files from the files it
finds in its directory, and a `Camera`'s name is unique in its
namespace, so two cameras that write to one claim never choose the same
file name. `Configure` leaves `UPLOAD_PREFIX` at the driver's value.
The holder can change the mode and the directory afterward: Ekos writes
its own upload settings when it captures, and
`astrophotography-operator` chooses a directory for each target. With
no outbox, the operator does not change the upload settings.

An edit of `spec.outbox` takes effect at the next session, because a
new camera pod in the middle of an exposure would lose the exposure.

## Editing devices during a session

From `PowerOn` until the reservation is `Released`, the telescope's
server runs the devices that it started with, and no others. The
observatory's server works the same way from `StartSite`, while a
reservation of any of its telescopes holds it. The operator accepts
every edit, but an edit that would change which devices a server runs
takes effect when the session ends:

- A device that a person moves to another telescope, another train, or
  out of every parent keeps running where it is. Its driver, its pod, and its power do not change, and
  it stays connected. Its `ParentFound` condition is `True` with the
  reason `MovePending`, and a message such as `Moves to OpticalTrain
  west-imaging when Reservation east-tonight ends`. At the end of the
  session the operator stops it with the session's other devices: its
  `deactivation` runs, it disconnects, `PowerOff` switches its output
  off, and its pod is deleted. The next session of its new telescope
  starts it there. The devices of a train that a person deletes keep
  running in the same way.
- Any other edit of a device that the session started, such as of its
  `spec.power`, its driver, or its claim, also waits for the end of the
  session. So does an edit of the `Telescope`'s `spec.observatory`, of
  its `OpticalTrain`s, of the tubes of those trains, of its `Guider`,
  of the reservation's `spec.outbox`, and of `spec.fitsKeywords` on any
  resource. When a session starts a server, the operator records in the
  reservation's `status.session`:
  - each device's parents, driver, power, and claim, and whether a
    `Guider` uses it
  - the telescope's observatory, trains, tubes, and `Guider`s
  - the reservation's outbox
  - the merged FITS keywords of each imaging camera

  `StartSite` records the observatory and the devices of its server,
  and `PowerOn` records the rest. Only the retry of a failed activation
  step records them again. Until the session ends, the operator acts on
  the record and not on the spec. So deactivation switches off the
  outputs that the session switched on, a camera whose driver restarts
  gets the `SCOPE_INFO` of the tube that the session started with, and
  PHD2 keeps its pod and its calibration. A tube's status still shows
  the lengths in its spec.
- The reservation's `EditsPending` condition shows when an edit of the
  outbox or of a FITS keyword waits for the next session. It is `False`
  with the reason `NoEditPending` while the record matches the specs.
  An edit of `spec.outbox`, or an edit of `spec.fitsKeywords` that
  changes an imaging camera's merged keywords, sets it `True` with the
  reason `EditPending`. Its message lists each change, such as
  `spec.outbox.claimName changes from frames to nightly at the next
  session`. Each change of its status posts an Event. An edit of a
  keyword that a more specific resource overrides changes no merged
  keywords, so the condition stays `False`.
- A device's settings, such as a camera's gain, the `Observatory`'s
  location, and the procedures of every resource are not in the record,
  because you may change them during a session on purpose. The operator
  writes the settings and the location again from the spec when a
  device's driver restarts.
- A device that you add to the telescope, a train, or the observatory
  during a session, by creating it or by setting its parent, does not
  start until the next session. This includes the first device of an
  observatory that had none. The device is `Idle`, and its `Ready`
  message reads `Starts at the next reservation, after Reservation
  east-tonight ends`. A resource that another resource's driver serves,
  such as the `FilterWheel` of a camera, and a new `Guider` of the
  telescope wait in the same way. The record identifies each object by
  its UID, so a device, train, tube, or `Guider` that you delete and
  create again with the same name also waits.
- A device that you delete while the reservation is `Ready` leaves at
  once, as [Deleting a running resource](#deleting-a-running-resource)
  describes. A deleted `OpticalTrain`, `OpticalTube`, or `Guider` is
  also gone at once. A `Guider` whose train is gone is `Error`, and a
  camera whose train or tube is gone gets no `SCOPE_INFO` when its
  driver restarts.

When an eviction deletes some or all of a server's pods, the operator
creates them again from the record, with the drivers, power, and claims
that the session started with. A device added during the session still
waits.

The reservations of several telescopes in one observatory share the
observatory's server. Each reservation records the observatory's
devices when its `StartSite` begins, and a reservation that starts
while another holds the server copies the other's record. So the
observatory's server keeps the same devices while any of those
reservations holds it, in whatever order they end. The record of a
`Released` reservation changes nothing.

## Deleting a running resource

The operator adds the finalizer `observatory.liken.sh/deactivate` to
each resource that it runs something for, so a delete waits until the
operator has stopped it:

| Resource | The finalizer stays |
|---|---|
| `Reservation` | from its first step until its deactivation steps are done |
| A device | from just before the operator creates its pod until the pod is gone and no server that a reservation holds runs the device |
| `Telescope` | from when a reservation gets it in `Wait` until that reservation is `Released` |
| `Observatory` | while a reservation holds any of its telescopes |

A device that you delete while a reservation is `Ready` leaves its
server, and the server and the other devices on it keep running. When
the device's `Telescope` or `Observatory` is `Active` and the device
ran its `activation`, the operator first runs the device's
`deactivation`, so a dust cap closes and a dome parks. A `deactivation`
that fails or times out posts a `ProcedureFailed` Warning, and the
delete goes on. A device that is not connected skips its actions. Then
the operator stops the device's driver on the running server and posts
a `DriverStopped` Event on the `Telescope` or the `Observatory`. It
deletes the device's pod, `Service`, and `ResourceClaimTemplate`, and
switches off the output in the device's `spec.power`, unless a device
on a running server uses the same output. The `PodDeleted` Event on the
device says whether the output was switched off. Then the operator
removes the finalizer. A device deleted while the reservation is
activating or `Failed` is removed at a retry, when the reservation
becomes `Ready`, or when it is released. A device deleted before the
`Activation` step ran its `activation` is removed with no procedure.

When you delete a reservation together with its devices or the
resources that place them, as `kubectl delete -f` of a whole inventory
does, the deactivation steps find each device on the server where its
driver's pod still runs, not through its parents. A device that sets
`spec.opticalTrain` deactivates with the train's devices, and any other
device with the devices of its telescope or observatory. An
`OpticalTrain` has no finalizer, so it is gone at once, but its dust
cap still closes, its flat panel still goes dark, and its camera still
aborts its exposure, warms, and disconnects in order, whether you
deleted those devices or not. A deleted `Dome` parks in the same way
and disconnects in `StopSite`, before the observatory's server stops.
`StopDevices` stops the driver of a deleted device but leaves its pod,
which the finalizer keeps. `PowerOff` then switches off the output that
the record names, and deletes the pod before the server's pod. A
deleted `Switch` switches its outputs off in the same way, and so does
a `Switch` that you moved to another telescope during the session. A
`Guider` has no finalizer either, so it is gone at once, and `StopWork`
finds its PHD2 through the guider's pod on the telescope's server.

Deleting a `Telescope` during a session ends the reservation that holds
it, as `spec.end` does. The deactivation steps run on the telescope and
its devices, starting at `StopWork`, while the finalizer keeps the
telescope. The summary of `StopWork` begins with `Telescope east was
deleted`, and the `Deactivating` Event on the reservation ends with the
same words. When the reservation is `Released`, the operator removes
the finalizer and the telescope is gone. Deleting an `Observatory` ends
the reservation of each of its telescopes in the same way, and the
observatory is gone when the last reservation is `Released`. The
devices of a deleted telescope or observatory are not deleted.

A new reservation of a telescope that is being deleted, or whose
observatory is being deleted, waits in `Wait` with the summary
`Telescope east is being deleted`. When the telescope is gone, the
summary reads `Missing Telescope east`, as for a telescope that never
existed.

A resource that runs nothing has no finalizer, and a delete removes it
at once. That is a device that is not installed, a device of a telescope with no
reservation or with a `Released` one, a `Telescope` that no reservation
holds, and an `Observatory` none of whose telescopes a reservation
holds.

While the operator is down, a delete of a running resource waits, and
the operator finishes it when it starts again. You can remove the
finalizer by hand:

```sh
kubectl patch dustcap east -n observatory --type=merge -p '{"metadata":{"finalizers":null}}'
```

This skips the device's `deactivation`. Kubernetes garbage collection
deletes the device's pod, its `Service`, its `ResourceClaimTemplate`,
and the pod's `ResourceClaim`, and then the operator stops the device's
driver on the running server. The same patch on a `Telescope` that a
reservation holds deletes it at once, and Kubernetes deletes its
server's pod and `Service`. The reservation runs until it ends and
then finds the telescope missing, so its `Deactivation` step skips
every procedure. The operator then deletes the pods of the
observatory's server with no procedure, once no reservation holds that
server. The same patch on an `Observatory` deletes it at once, and the
reservation runs until it ends. Its `Deactivation` step then finds the
observatory missing and skips every procedure.
