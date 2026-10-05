# 03, The topology by hand

Proposed on 2026-10-05. Not built.

## The problem

Root plan 74 tested its architecture with containers on one Docker
bridge. Pods on a cluster add the pod network, `Service` names, and the
kubelet's restarts, and none of those were tested. The guide camera's
frames cross the network twice on their way to PHD2, and the latency
across nodes is the last unknown of the architecture.

## The topology

The manifests in `observatory-operator/topology/` run the simulators
with the architecture of plan 74, with no operator code:

- One pod for each device, from the image of
  [plan 02](02-driver-and-simulator-images.md) that holds its driver.
  The pod runs `socat TCP-LISTEN:7625,reuseaddr EXEC:<driver>,pipes`,
  and a `Service` names it.
- One `indiserver` pod from the `indi` image, with one shim for each
  device.
- The device pods run on other nodes than the server pod, so frames
  cross real network hops.

A script connects each device and applies its settings. It stands in
for the reconciler of plan 08.

## The shim

`indiserver` starts each driver as a child process, with
`execlp(path, name)` and no arguments (`indiserver/LocalDvrInfo.cpp`).
So the server cannot start `socat` with an address. In Docker the shim
was a shell script, and the `indi` image has no shell.

The shim is a small Go program, built static, in the `indi` image. Each
device is a symbolic link to it, named for the device's `Service` and
port, and the shim reads its target from its own name in `argv[0]`. It
does three things:

1. It connects to the target. While the connection is refused, it
   waits a fixed interval and tries again, up to a deadline. At the
   deadline it exits.
2. It copies bytes between its stdin and stdout and the connection, in
   both directions.
3. When either side closes, it exits.

It parses no INDI message and holds no state.

The server's pod has no shell to make the links, so the shim makes them
too: `indi-shim link <dir> <host:port>...` writes one link for each
device, and an init container runs it into an `emptyDir` that the
server's container mounts. Without the wait in step
1, a shim whose device pod is down would exit at once, `indiserver`
would restart it at once, and the two would loop as fast as the CPU
allows.

`indiserver` does the rest. When a shim exits, the server sends
`delProperty` for the device's properties to every client, starts the
shim again, and sends the new driver `getProperties`. The new driver
defines its properties again and sends its own snoop requests.

### The restart count

`indiserver` restarts a driver at most `-r` times, 10 by default, and
the count never resets: each restarted driver copies it from the one
before (`indiserver/DvrInfo.cpp`). Each exit of a shim spends one
restart. The server runs with `-r 2147483647`, the largest value its
`atoi` parse holds (`indiserver/indiserver.cpp`). A device pod that
restarts spends one. A device pod that stays down spends one for each
deadline of step 1, so with a deadline of 60 seconds the count lasts
about 4,000 years. A restart of the server pod resets every count.

### Rejected: a shim that reconnects

A shim could stay up through a device pod's restart: send `delProperty`
for the device itself, reconnect, and replay the server's first
`getProperties` to the new driver. The server would then spend no
restarts. It was rejected because the restart count does not need it,
and it would put protocol state in the shim that can drift from what
`indiserver` does.

## How we test it

- The time from the end of an exposure to the BLOB at a client, for a
  guide-sized frame, compared with the 13 ms median that the Docker
  bridge gave.
- A restart of each device pod during an exposure on another device,
  and a restart of the server pod. In Docker, the other devices kept
  working through a device's restart, and both devices were back on
  the server 1 second after a server restart.
- A server pod that starts before its device pods. A `Service` name
  resolves before its pods exist, so each shim waits in step 1 and
  connects when its pod is ready.
- A device pod that stays down for longer than the shim's deadline.
  The server's log shows one restart for each deadline, and nothing
  faster.

## Upstream issues

- [indi#1927](https://github.com/indilib/indi/issues/1927): a server
  exits when a server it chains stops. That is the reason plan 74
  rejected chaining.
- [indi#1499](https://github.com/indilib/indi/issues/1499): drivers
  that serve several devices congested their pipe to the server. One
  device in each pod avoids that.
- [indi#2330](https://github.com/indilib/indi/issues/2330): a driver
  run as root under systemd restarted in a loop, and running it as a
  user fixed it. The device pods run their drivers as a user.

## References

- A driver chooses descriptor passing for BLOBs in `is_unix_io()` in
  `libs/indibase/indidriverio.c`. A pipe makes it send base64, which is
  why the listener needs `pipes`.
- [Root plan 74](../../plans/74-astrophotography.md), "The rig" and
  "Evidence"
