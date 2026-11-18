# A moved monitor is published on two connectors

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

The link history in `links.go` keeps the last monitor of a connector
that reads disconnected, so a monitor that shows another input keeps
its devices in the slice and its pod keeps running. When the cable
moves from `HDMI-A-1` to `HDMI-A-2` on the same node, `follow` keeps
the monitor on `HDMI-A-1`, and `HDMI-A-2` reads the same monitor. Both
connectors then publish the same `monitor.liken.sh/id` and model, with
no taint on either, until `HDMI-A-1` reads a monitor again. A claim
that selects the monitor matches two devices, and the pod that held
`HDMI-A-1` keeps drawing to a connector with no monitor.

`loadFromDisplays` in the same file skips this case after a restart:
it does not load a monitor that is connected on another connector,
because "publishing it on both connectors would give a claim two
matching devices". `follow` has no such check while the operator
runs.

## Evidence

- `links.go`, `follow`: a disconnected connector returns
  `h.links[output.Connector].monitor` with no look at the other
  connectors.
- `links.go`, `loadFromDisplays`: the `elsewhere` map skips a monitor
  that is connected now on another connector.

This differs from
[two monitors of one model share a Display](two-monitors-of-one-model-share-a-display.md):
there two monitors have one identity, and here one monitor has two
connectors.

## What would confirm it

A test of `withLinks` over two passes: `HDMI-A-1` connected with a
monitor, then `HDMI-A-1` disconnected and `HDMI-A-2` connected with
the same monitor. Read `Remembered` on `HDMI-A-1`. On the lab fleet,
move a cable between two ports and read the node's `ResourceSlice`.

## What a fix has to decide

`withLinks` sees every output of a pass, so it can drop the
remembered monitor of a disconnected connector when that monitor is
connected elsewhere. Then the pod on the old connector loses its
device, and the fix decides whether that pod gets the disconnected
taint at once or after a grace period such as `disconnectGrace`.
