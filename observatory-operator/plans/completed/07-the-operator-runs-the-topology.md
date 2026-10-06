# 07, The operator runs the topology

Proposed on 2026-10-05. Built on 2026-10-05, apart from the two parts
that "Not built yet" names, and tested against the fake API server and
the recorded transcripts. Drilled the same day on the two-node test
cluster of plan 03, with the simulators of `examples/simulators.yaml`.
"What the test cluster measured" gives the results. The drill found
one defect, a park sent again after an operator restart, and its fix
is in the operator that the drill ran last. Plan 09 holds the placement
of the pods, and the fifo is an open problem.

## The problem

Plan 03 runs the observatory from static manifests. A person who adds
a focuser has to write a pod, a `Service`, and a new shim argument for
the server, and has to restart the server to add it.

## The requirement

The operator turns the resources of plan 06 into the topology of plan
03: one pod and one `Service` for each device, with its DRA claim, and
one server pod for each `Telescope` with one shim for each of its
devices. The `Observatory` has one more server pod for the devices that
no telescope owns. The manifests of plan 03 become the operator's
output, and the operator's tests compare against them.

The operator starts a telescope's pods only while a `Reservation` for
it is active, and runs activation and deactivation in the order that
plan 06 gives. Activation waits for each device's DRA device to appear,
because a person powers the devices on by hand, or a `Switch` output
switches them on.

A device added after the server starts reaches the server without a
server restart. `indiserver` reads `start` and `stop` commands for
drivers from its `-f` fifo, so the operator can add and remove shims
on a running server. Each device is a symbolic link to the shim of
plan 03, so adding a device is a new link and a `start` command. How
the operator reaches the fifo in the server's pod is open.

The operator also places the pods. On the test cluster of plan 03, the
link between two nodes carried 76 Mbit/s, and a guide-sized frame took
430 ms for each hop it crossed. So the guide camera's pod and the
server's pod run on one node when the link between nodes is slow. The
server's pod stops with a grace period of 1 second, because
`indiserver` runs as process 1 and ignores `SIGTERM`, and a longer
period leaves every device offline for its whole length.

The operator watches through client-go's reflector, as every operator
in the repository does. The `operators` skill holds the rules.

## The resources it reads and writes

Plan 06 built the resources. The package `observatory` holds their Go
types, and the CRDs in `deploy/` are in the category `astro`. Each
device's spec embeds `DeviceSpec`: `driver.name`, `driver.image`,
`power.switch`, `power.output`, and `claim`, a `ResourceClaimSpec` that
the operator turns into a `ResourceClaim` for the device's pod. Each
device's status embeds `DeviceStatus`: `phase`, `conditions`,
`observedGeneration`, `indiDevice`, `driver`, `image`, `pod`, `node`,
and `properties`, beside a `readings` block of the kind. `Parent()` on
any device's spec names its parent's kind and name, and each CRD
declares the parent fields as `selectableFields`.

A deleted `Reservation` is held by the finalizer
`observatory.liken.sh/deactivate` until its deactivation steps are
done. `status.steps` lists the steps of `observatory.ActivationSteps`
from the start, and those of `observatory.DeactivationSteps` from
when deactivation begins. Each step is `Pending`, `Running`, `Done`,
`Failed`, or `Skipped`, and `status.step` names the one that runs.
The steps were proposed in this order:

| Step | What it does |
|---|---|
| `Wait` | Waits for `spec.start`. |
| `StartSite` | Starts the observatory's server and devices, unless another reservation started them. |
| `PowerOn` | Starts the telescope's server and its `Switch` devices, connects them, and switches on each output that a device's `spec.power` names. |
| `StartDevices` | Creates the device pods and waits until they are ready. For real hardware, waits for each DRA device to appear. |
| `Connect` | Connects the devices in a fixed order: mount, GPS, polar aligner, focusers, filter wheels, rotators, dust caps, flat panels, receivers, and the cameras last, because a camera snoops the others. |
| `Configure` | Writes the location to the mount and the GPS, each camera's `ACTIVE_DEVICES` from its train, the filter names, the camera's gain and offset, and the dome policies for one mount. |
| `Prepare` | Opens the dust caps, cools each camera to its setpoint within a tolerance and a timeout, and unparks the mount. |
| `Abort` | Aborts the exposures and stops the mount. |
| `Secure` | Switches the flat panels off, closes the dust caps, parks the mount, and switches the coolers off, then waits with a timeout while the cameras warm. |
| `Disconnect` | Disconnects the devices in the reverse order of `Connect`. |
| `StopDevices` | Deletes the device pods. |
| `PowerOff` | Switches the outputs off, then stops the `Switch` pods and the telescope's server. |
| `StopSite` | Stops the observatory's server, unless another reservation is active. |

Note added on 2026-10-05: `Prepare` does not switch tracking on,
because the holder, KStars in mode 1 or a `Session` in mode 2, aligns
and calibrates the mount first.

The server stops in `PowerOff`, after the outputs switch off. The
operator reaches a `Switch` through the telescope's server, so a
server that stopped in `StopDevices` would leave the outputs on. Each
step has a timeout. A step that times out fails the reservation, and
the `Ready` condition's message names the step and the device. The
same inputs run the same steps in the same order, and an operator that
restarts resumes from `status.steps` and what it observes.

## How we test it

Applying the resources of plan 06 brings up the simulators, and the
plan 03 measurements give the same results. Adding a device and
deleting one leave the other devices connected.

## What was built

The operator is the `package main` of `observatory-operator/`, with its
image `ghcr.io/liken-sh/observatory-operator` and its RBAC and
`Deployment` in `deploy/`. It watches the 20 kinds, and its own pods
and `Service`s, through client-go's reflector in `kubernetes/informer`.
One runner for each `Reservation` runs its steps, and a status writer
writes the status of every other resource.

These points settle what the requirement left open or stated
otherwise. The ones that change what a person reads are marked for
review.

- **The reconciler runs in the operator.** The design put a reconciler
  in each server's pod. The operator instead holds one INDI client for
  each server, which dials the server's `Service` when the server's pod
  is Ready, and does the connect, configure, and status work there. One
  process holds the logs. After a restart, the operator connects again
  and reads the whole state again. A connection that ends while the pod
  stays Ready is opened again after a pause that doubles from 1 second
  to 30 seconds, a clock for the end that no pod event reports.
- **The server starts with every device.** The server's arguments link
  every device of its telescope, as in plan 03. A shim whose device pod
  does not exist yet dials until the pod appears. The operator orders
  the devices at the INDI level, by when it creates each device's pod
  and when it sets `CONNECTION`. A change to the set of devices while a
  reservation is active replaces the server's pod, because the digest
  of its spec changes. The `-f` fifo is not used, and stays future
  work.
- **Pods, not `Deployment`s.** Each device and each server is a bare
  pod, as media-operator's are. The kubelet restarts a container that
  exits, and the operator creates a pod again that is deleted while the
  reservation is `Ready`. A device pod has no readiness probe: `socat`
  serves one connection, and a probe's connection would start the
  driver and end it. The server's pod has a TCP probe on port 7624.
- **Names (review).** A device's pod and `Service` are named
  `<kind>-<name>`, such as `camera-east-main`, and a server's
  `telescope-<name>` or `observatory-<name>`, because two kinds can
  hold resources of one name. A name that is not a DNS label of 63
  characters or fewer fails the step that needs it.
- **Which device is which.** The operator finds a resource's INDI
  device by `DRIVER_INFO.DRIVER_EXEC`, which is the program's name and
  so the resource's `spec.driver.name`. Two resources on one server
  that run one driver fail with a message, because INDI names both
  devices after the model.
- **The second reservation waits (review).** A second reservation of a
  telescope waits in `Wait`, with a message that names the holder.
  Waiting reservations take the telescope in the order of `spec.start`,
  then of creation, then of name. The operator keeps the holders in
  memory and reads them from each reservation's status at start: a
  reservation whose `Wait` is `Done` holds its telescope until it is
  `Released`. So the operator runs as one replica with `Recreate`, as
  people-operator does, and takes no `Lease`.
- **Failure and retry (review).** A failed activation step leaves the
  telescope as it is, and a delete then runs deactivation from `Abort`.
  A failed deactivation step keeps the finalizer, and the
  `SafeToPowerOff` condition names the step. The annotation
  `observatory.liken.sh/retry` runs the failed step again, and the
  operator removes it. A write that the API server refuses inside a
  step is tried again every 5 seconds until the step's deadline.
- **The steps against the simulators.** `Abort` sends an abort only to
  a camera whose `CCD_EXPOSURE` is Busy and to a mount whose
  `EQUATORIAL_EOD_COORD` is Busy: the simulators answer an abort of
  nothing with no update at all. `Prepare` waits for the camera's
  temperature within 0.5 °C of its setpoint, not for the property's
  state. `Secure` warms a cooled camera to 5 °C for up to 10 minutes
  and then switches the cooler off, wherever the sensor is: a cooler
  cannot warm a sensor above the air around it. `StopSite` also parks
  the dome. `Configure` also writes the tube's focal length and
  aperture to the camera's `SCOPE_INFO`, and the mount's
  `ACTIVE_GPS`. A property that a driver lacks is named in the step's
  message, and the step goes on; a step with nothing to do is
  `Skipped`.
- **The lock policies (review).** `domeLocksMount` and
  `mountLocksDome` are not written: the dome runs on the observatory's
  server and each mount on its telescope's, and a driver snoops only
  devices on its own server. `Configure` says so in its message. The
  shutter policies need no snoop, and `StartSite` writes them to each
  dome.
- **Deadlines.** `StartSite`, `PowerOn`, and `StartDevices` 10
  minutes, for an image pull; `Connect`, `Configure`, `Abort`,
  `Disconnect`, and `StopDevices` 2 minutes; `Prepare` and `Secure` 20
  minutes; `PowerOff` 5 minutes; `StopSite` 10 minutes. A deadline
  counts from the start time in the step's record, so it holds across
  an operator restart.
- **Status.** The status writer composes every status at most once a
  second and writes only what changed. A device's status names its INDI
  device, its pod and node, the typed readings of its kind, and every
  property, from its server's store.
- **The image map.** `drivers/generated.go` maps each third-party
  driver in `indi/images/` to its family's image, at the tag of
  `indi/package.toml`, and a test fails when it is stale. A simulator
  resolves to `indi-simulators`, and every other driver to `indi`,
  which holds libindi's whole `indi-bin`.

## What the tests showed

The tests run the operator in a `testing/synctest` bubble against a
fake API server and fake INDI servers built from the transcripts of
plan 04, with `examples/simulators.yaml` as the inventory. They cover
the topology against plan 03's manifests, the 13 steps in order with
their messages, each step's deadline, a refusal by a device in each
step, the finalizer, an operator restart in the middle of `Prepare`
and of `Secure`, the second reservation, `spec.start` and `spec.end`,
the retry, a device and a server that restart while `Ready`, a device
added while `Ready`, and the status of every kind.

A harness on a workstation, not kept in the repository, ran the same
operator against the real simulators of the `20261005-3` image in
Docker, each pod as a container. Activation took 24.9 seconds:
`StartSite` 2 s, `PowerOn` 2 s, `StartDevices` 2 s, and `Prepare` 19 s
while the CCD simulator cooled from 0 °C to -10 °C at 0.5 °C a second.
Deactivation took 84.7 seconds: `Secure` 49 s, with the camera warmed
to 5 °C, and `StopSite` 16 s while the dome turned to park. After a
restart of the camera's container, the camera was connected again,
with its gain written again, 1.9 seconds later.

## What the test cluster measured

The operator ran from its published development build, applied by
Flux from the deploy artifact, on the two-node test cluster of plan 03.
The inventory was `examples/simulators.yaml` without its
`Reservation`. Each drill created and deleted reservations by hand,
and read `kubectl get rsv -w`, the step times in `status.steps`, and
the Events. Step times are to the second, from `status.steps`, and
the times below a second come from the watch.

| Drill | Result | Timings |
|---|---|---|
| Inventory | Passed. With no `Reservation`, each of the 21 resources with a phase reported `Inventory`, and only the operator's pod ran. | none |
| Activation, images not yet on the nodes | Passed. `kubectl wait --for=condition=Ready` returned, and `status.endpoint` named `telescope-east.observatory.svc:7624`. | 2 min 2 s: `StartSite` 39 s and `StartDevices` 61 s, each while a node pulled the 281 MB `indi-simulators` image in 57 s |
| Activation, images on the nodes | Passed. | 33 s: `StartSite` 7 s, `PowerOn` 4 s, `StartDevices` 3 s, `Connect` and `Configure` under 1 s, `Prepare` 19 s |
| A device pod deleted while `Ready` | Passed. The camera came back connected with its gain, offset, and `ACTIVE_DEVICES`. The mount's status read `Starting` 1.1 s after the delete. | camera connected 4 s after the delete, mount 4.2 s |
| The server's pod deleted while `Ready` | Passed. Every device came back connected. | new pod running 4.2 s after the delete, mount connected 7.2 s after it |
| The operator's pod deleted while `Ready` | Passed. No pod changed, no step ran, and no Event was written. | status writes resumed within 2 s |
| A second reservation of `east` | Passed. It waited in `Wait` with the message "waiting for the Reservation east-tonight to release the Telescope east", and took the telescope when the first was `Released`. | 0.1 s from the first's release to the second's `Wait` |
| A reservation of `west` while `east` was `Ready` | Passed. `west` got its own server, and the observatory's server pod kept its UID and had no restart. `StopSite` of each earlier release was `Skipped` and named the reservation that still held the site. | `Ready` in 7.3 s |
| The operator restarted during `Prepare` | Passed. `Prepare` kept its start time and finished as an uninterrupted `Prepare` does. | 18 s for the step |
| The operator restarted during `Secure` | Failed, then passed after the fix. The new operator sent `TELESCOPE_PARK` while the mount moved to park, the simulator aborted the park and answered Alert, and the reservation was `Failed`. The retry annotation ran `Secure` again, and deactivation finished. With the fix, the new operator waited for the park and sent nothing. | 75 s from the delete to the release, with the restart |
| Deactivation of the last reservation | Passed. The flat panel switched off, the dust cap closed, the mount parked, the camera warmed to 5 °C and its cooler switched off, and then the steps stopped the devices, the outputs, the server, and the site. | 74 s: `Abort` 0 s, `Secure` 50 s (16 s for the park, 29 s to warm from -10 °C), `Disconnect` 0.3 s, `StopDevices` 1.8 s, `PowerOff` 2.8 s, `StopSite` 19 s (17 s for the dome to park) |
| A reservation that reached `spec.end` | Passed. It stayed `Released`, and `SafeToPowerOff` was `True`. Only the operator's pod ran. | `SafeToPowerOff` 40 s after `spec.end` |

The cold activation took 2 minutes, against 24.9 seconds in Docker,
because each node pulled the simulators' image. With the image on the
nodes, the 33 seconds are close to the Docker result, and `Prepare`'s
cooling is most of them in both.

The operator places no pod, and the scheduler put the guide camera's
pod on the other node from the telescope's server. On this cluster's
76 Mbit/s link, that costs the 430 ms for each frame that plan 03
measured.

The drill also found two faults in the status. Each
`WeatherStation` and the `Observatory` reported the weather as
`Unknown`, because the weather simulator sets the state of
`SAFETY_STATUS` and leaves its `SAFETY` light Idle. A `Prepare` with
nothing to change read "no mount to unpark" for a mount that was
unparked already. Both are fixed in the build that the drill ran last.

## Not built yet

- The placement of the pods. The guide camera's pod and the server's
  pod belong on one node when the link between nodes is slow, and the
  operator places no pod. [Plan 09](../09-the-guider.md) places them
  with the guider's pod. Built later on 2026-10-05, ahead of the rest
  of plan 09: the camera of the `OpticalTrain` that a `Guider` names
  has a required pod affinity to its telescope's server, unless the
  camera has a claim. Plan 09 gives the reasons.
- Adding a device to a running server through the `-f` fifo. A change
  to the devices restarts the server, which costs 3 seconds.
  [Adding a device restarts the server](../open-problems/adding-a-device-restarts-the-server.md)
  holds it.

## Upstream issues

- [indi#2365](https://github.com/indilib/indi/pull/2365) and
  [indi#2340](https://github.com/indilib/indi/issues/2340): ZWO cameras
  reset their USB connection after frames, and the driver's hot-plug
  handling destroyed the device during the reset. A device pod has to
  survive the device leaving and returning on the bus.

## References

- The fifo: `indiserver/Fifo.cpp` in `indilib/indi`, and the `-f`
  option in `indiserver`'s usage text
- The `operators` skill under `.agents/skills`
