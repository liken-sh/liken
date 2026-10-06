# 08, The reconciler

Proposed on 2026-10-05. Built on 2026-10-05, with plan 07, in the
operator and not in each server's pod. Closed on 2026-10-06. The
restart drills ran on the two-node test cluster of plan 03 on
2026-10-05, and "What the test cluster measured" gives the results.
The lock policies between the dome and the mounts are not built, and
moved to the open problem "The dome and mount locks cross two
servers", which [plan 12](12-the-dome-and-mount-locks-across-servers.md)
closed.

## The problem

A driver that restarts comes back disconnected, with its settings lost.
In root plan 74's tests, every restart left `CONNECTION.CONNECT=Off`
and lost the focal length, the upload mode and directory, and the
mount's position. INDI writes settings to `~/.indi/*_config.xml` in
the driver's own filesystem. A container restart deletes them.

## The requirement

A reconciler in the server's pod connects each device and applies the
settings from its resource when the device appears on the server. It
applies them only then. A reconciler that applies settings on every
change would undo what KStars changes, such as `UPLOAD_MODE`.

It also writes each camera's `ACTIVE_DEVICES` from the camera's
`OpticalTrain`, and the mount and dome policies that the `Observatory`
configures for one mount, as plan 06 describes.

The reconciler also writes each device's state to its resource's
status from the INDI updates, through the client of plan 04. It reads
every update on one connection, and opens no timer to read state
again.

Whether the reconciler keeps INDI's own configuration files on a
volume, or applies everything from the resource each time, is open.

## How we test it

The restart drills of plan 03, now with no script: each device returns
to its configured state after its pod restarts, and after the server
pod restarts.

## References

- The configuration a driver saves: the `CONFIG_PROCESS` property that
  every driver defines, and `DefaultDevice::saveConfigItems` in
  `libs/indibase/defaultdevice.cpp`
- [Root plan 74](../../plans/74-astrophotography.md), "The reconciler"

## What was built

Plan 07 put the reconciler in the operator. The operator holds one
INDI client for each server, through the server's `Service`, and the
work below runs there.

- **Built: a device that appears is set up.** While a reservation is
  `Ready`, the operator connects each device whose driver defines
  `CONNECTION` and reports it disconnected, and writes its settings
  from its resource: the location, `ACTIVE_DEVICES`, the gain, the
  offset, `SCOPE_INFO`, the filter names, the cooler's setpoint, and a
  `Switch`'s outputs. It acts only when the driver defines `CONNECTION`,
  which a driver does when it starts and on each new connection of the
  operator. A device that KStars disconnects defines nothing, and stays
  disconnected. A failure is shown in the device's status, and the
  reservation stays `Ready`.
- **Built: each camera's `ACTIVE_DEVICES` from its train.** The camera
  snoops the mount, and the focuser, the filter wheel, and the rotator
  of its own train. A member with no device is written empty.
- **Built: status from the INDI updates.** The operator reads every
  update on its one connection to each server, and writes each
  device's phase, readings, and properties at most once a second.
- **Settled: no configuration files on a volume.** The operator writes
  the settings from the resource each time a device appears. The pod's
  `/tmp` is an `emptyDir`, and `HOME` is `/tmp` in the images, so a
  driver's own `~/.indi/*_config.xml` survives a restart of its
  container and is lost with its pod.
- **Not built: the policies between the dome and the mounts.**
  `DOME_POLICY` and `MOUNT_POLICY` rely on a snoop between the dome and
  the mount, and the dome runs on the observatory's server and each
  mount on its telescope's. The shutter policies need no snoop, and the
  operator writes them. Enforcing the lock policies across servers is
  work for the operator. The open problem "The dome and mount locks
  cross two servers" held it, and [plan
  12](12-the-dome-and-mount-locks-across-servers.md) closed it.

The unit tests restart a device's pod and the server's pod while a
reservation is `Ready`, and each device came back connected with its
settings. A harness on a workstation, not kept in the repository,
restarted the CCD simulator's container behind a real `indiserver`,
and the camera was connected with its gain written again 1.9 seconds
later.

## What the test cluster measured

Plan 07's drill ran the restart drills of plan 03 against the
operator's published build, with no script. While the reservation was
`Ready`:

| Restart | Result | Time from the delete |
|---|---|---|
| The camera's pod | Connected again, with its gain, offset, and `ACTIVE_DEVICES` written again | 4 s |
| The mount's pod | Connected again; its status read `Starting` 1.1 s after the delete | 4.2 s |
| The telescope's server pod | Every device connected again, with its settings | 7.2 s for the mount |
| The operator's pod | No device changed, and the status writes resumed | under 2 s |

The status from the INDI updates showed each device's gap and return.
A mount that does not track writes its status about once a second,
because its right ascension changes with each update.
