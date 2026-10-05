# 07, The operator runs the topology

Proposed on 2026-10-05. Not built.

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
