# 08, The reconciler

Proposed on 2026-10-05. Not built.

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
