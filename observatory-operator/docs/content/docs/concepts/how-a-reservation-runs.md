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
| `PowerOn` | Starts the telescope's server with a link to every device, starts and connects its `Switch` devices, and switches on each output that a device's `spec.power` names. Fails when two devices of the telescope name one driver. | 10 min |
| `StartDevices` | Starts the pod of every other device, and waits until each pod is Ready and its driver defines its device on the server. A device on real hardware waits here for its claim. | 10 min |
| `Connect` | Connects the mount, the GPS, the polar aligner, the focusers, the filter wheels, the rotators, the dust caps, the flat panels, the sky quality meters, the receivers, and the cameras, in that order. | 2 min |
| `Configure` | Writes the observatory's location and the `DOME_POLICY` to the mount, the location to the GPS, each camera's `ACTIVE_DEVICES` from its train, the camera's gain and offset, the tube's focal length and aperture, and the filter names. It then relays the dome's park state to the mount, before a procedure unparks it. | 2 min |
| `Activation` | Runs the activation procedures, from the top of the tree down: the `Observatory`'s and its devices', unless another reservation in the observatory ran them, then the `Telescope`'s and its devices', then those of the devices of its trains. [Procedures](/docs/concepts/procedures/) states what they do. | none: each action's timeout |
| `StartGuider` | Starts the guider's pod, waits for PHD2's event server, sends `set_connected`, and waits until PHD2 reports its camera and mount connected. A telescope with no `Guider` skips it. | 10 min |
| `Abort` | Stops PHD2's exposures and guiding with `stop_capture`, then ends each exposure and stops the mount if it moves. | 2 min |
| `Deactivation` | Runs the deactivation procedures, from the bottom of the tree up: those of the devices of the telescope's trains, then the `Telescope`'s own devices' and its own. When the last telescope in the observatory ends, it then runs the observatory's devices' and the `Observatory`'s own. Every device is still connected. | none: each action's timeout |
| `StopGuider` | Deletes the guider's pod, `Service`, and `ConfigMap`, while its camera and mount are still connected. | 2 min |
| `Disconnect` | Disconnects the devices in the reverse order of `Connect`. | 2 min |
| `StopDevices` | Deletes the device pods. | 2 min |
| `PowerOff` | Switches the outputs off, then stops the `Switch` pods and the telescope's server. | 5 min |
| `StopSite` | Disconnects the observatory's devices, switches their outputs off, and stops its server, unless a reservation of another telescope in the observatory is active. | 5 min |

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
a device also runs again its `Failed` `activation` after the device
joined an `Active` parent. Any other run of `activation` or
`deactivation` runs again through the retry annotation of its
reservation.

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

A device that joins or leaves a telescope or the observatory during a
reservation is an ordinary edit, and the operator refuses no change.
While the reservation is `Ready`, the server keeps running, and the
other devices on it stay connected. A device that joins gets its pod,
and then its driver starts on the running server, and the operator
connects it, writes its settings, and runs its `activation`. A device
that leaves, to the shelf or to another telescope, first runs its
`deactivation` through the driver on the server it leaves, within the
timeouts of its actions. Then its driver stops on the server, also
after a `deactivation` that failed with a `ProcedureFailed` Warning,
and the device loses its pod, its `Service`, and its `ResourceClaim`.
A device that a person deletes leaves the same way, as [Deleting a running
resource](#deleting-a-running-resource) describes. The
operator posts a `DriverStarted` or a `DriverStopped` Event on the
`Telescope` or the `Observatory`, and the device that left gets a
`PodDeleted` Event. A device that leaves during activation keeps its
pod until the reservation is `Ready`, or until deactivation's
`StopDevices`. A device on a telescope with no active reservation
changes nothing that runs.

INDI names a device after its model, so two devices on one server
with the same driver define one device, and the second driver breaks
the first. Activation refuses such a pair: `StartSite` or `PowerOn`
fails, and its message names both devices and the driver, so a
reservation never becomes `Ready` without its imaging camera. A device
that joins a running server during a `Ready` reservation, with a
driver that a running device holds, never starts. It reports `Error`
with the name of the device that runs the driver, and the running
device stays connected.

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

A device that a person deletes during a session leaves its server.
When its `Telescope` or `Observatory` is `Active`, and the device ran
its `activation`, the operator first runs its `deactivation` through
its driver, so a dust cap closes and a dome parks. A `deactivation`
that fails or times out posts a `ProcedureFailed` Warning, and the
delete goes on. A device that is not connected skips its actions.
Then the operator stops the device's driver, deletes its pod, its
`Service`, and its `ResourceClaim`, and posts a `PodDeleted` Event on
the device. Then it removes the finalizer, and the device is gone. A
device deleted during activation goes when the reservation is `Ready`,
with no procedure, because the `Activation` step did not run its
`activation`.

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
