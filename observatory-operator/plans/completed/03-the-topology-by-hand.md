# 03, The topology by hand

Built on 2026-10-05, and run on a two-node test cluster with the
`20261005-1` images. "What the test cluster measured" gives the results.

## The problem

The Docker tests below tested the architecture with containers on one
Docker bridge. Pods on a cluster add the pod network, `Service` names, and the
kubelet's restarts, and none of those were tested. The guide camera's
frames cross the network twice on their way to PHD2, and the latency
across nodes is the last unknown of the architecture.

## The Docker tests

On 2026-10-04, before this plan, the architecture was tested on one
workstation with Ubuntu 26.04, INDI 2.2.4 and 2.2.5 from the PPA, and
Docker. Containers on one Docker bridge network stood in for pods, and
`docker run --restart always` stood in for the kubelet's restarts. The
tests chose one `indiserver` with a pod for each device over the two
designs below.

### Rejected: a server in each device's pod

`indiserver` can chain another server: a driver argument of the form
`Device@host:port` forwards that device from a remote server. The
server also delivers the properties that one driver reads from
another, which INDI calls snooping: the CCD driver snoops the mount's
coordinates to write `OBJCTRA` and `OBJCTDEC` into each FITS header,
and a dome snoops the mount to follow it. A chain of per-device
servers was the first design, and three properties of
`indiserver` rule it out.

- **Snooping does not cross a hub.** A hub that chains the mount's
  server and the camera's server does not deliver the mount's
  properties to the camera driver. The camera's own server has to chain
  the mount's server too.
- **A lost remote ends the server.** When a remote server closes,
  `indiserver` reconnects at once, with no delay. If that connection is
  refused, `RemoteDvrInfo::openINDIServer` calls `Bye()`, which calls
  `exit(1)`. In the test, removing the mount's container ended both the
  hub and the camera's server, which chained the mount.
- **A cycle never starts.** `indiserver` connects to its remotes before
  it opens its own port. Two servers that chain each other each wait
  for the other to listen. In the test, each restarted 9 times in 45
  seconds and neither started. Real rigs have such pairs: a dome
  follows the mount, and the mount reads the dome's park state.

An operator that starts and restarts the servers in dependency order
does not fix this. A cycle has no order, and when a server restarts,
the servers that chain it exit at once, before any operator can act.

### Rejected: every device in one pod

One pod with one `indiserver` and every device claim is how a Raspberry
Pi rig runs today. A pod's claims are allocated when the pod is
scheduled, so the pod does not start until every claimed device exists.
An unplugged focuser would keep the camera from running. Separate
device pods keep each device's claim independent.

### What Docker measured

| Test | Result |
|---|---|
| The CCD simulator with the mount simulator | Draws catalog stars at the mount's position with `gsc`. A blank frame until `SCOPE_INFO.FOCAL_LENGTH` is set, because its default is 0 and the mount simulator does not supply one. |
| `solve-field` on a simulated frame | Solved in 1.2 seconds with the Ubuntu index packages, about 20 arcminutes from the mount's JNow position, which is the precession since J2000. |
| `UPLOAD_LOCAL` | The driver wrote the FITS file and sent its path in `CCD_FILE_PATH`. |
| A hub chaining the mount's and the camera's servers | No stars and no `OBJCTRA` until the camera's own server also chained the mount. |
| The mount's server removed | The hub and the camera's server both exited with `good bye` in the same second. |
| Two servers that chain each other | 9 restarts each in 45 seconds. Neither started. |
| A `Never` client and an `Also` client on a chained hub | The client that sent `enableBLOB` last set BLOBs for both. A client that never sent `enableBLOB` changed nothing for the other. |
| Shim over a Unix socket without `pipes` | Snooping worked. The camera driver crashed on its first BLOB. |
| Shim over TCP with `pipes`, `UPLOAD_BOTH` | An `Also` client received 3.8 MB per frame. A client that never sent `enableBLOB` received 183 KB of control messages. The frame was also on the camera's volume. |
| The mount's pod restarted during a 15-second exposure | The exposure finished and wrote its frame. The server and the camera were not restarted. The mount came back disconnected. |
| The camera's pod restarted during an exposure | The mount's right ascension did not change. That exposure was lost. |
| The server's pod restarted | Both driver pods restarted with it. Both devices were on the server again after 1 second, disconnected. |
| The server started 8 seconds before the drivers | It converged after the shims restarted a few times. The cause is probably that `socat` retries a refused connection but not a name that does not resolve yet. This was not confirmed. |
| Exposure end to BLOB at the client, 1280 by 960, 16-bit, 3.3 MB on the wire, 15 exposures | One server with the drivers as children: 8 ms median, 8 ms p90, 21 ms max. The shim design: 13 ms median, 14 ms p90, 15 ms max. |

Every restart left its device disconnected (`CONNECTION.CONNECT=Off`)
with its settings gone: the focal length, the upload mode and
directory, and the mount's position. That finding is the reason for
the reconciler of [plan 08](08-the-reconciler.md).

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

## What the test cluster measured

The manifests ran on a test cluster of two nodes, with the server on
one node and the three device pods on the other. Every container ran as
user 1000 on a read-only root filesystem.

- **The server reached every device on the first try**, through the
  shims and the `Service` names, with no shim restart at startup.
- **A frame crossed the cluster intact.** The CCD simulator at 500 mm
  wrote a frame with 337 bright pixels and the mount's `OBJCTRA` in its
  header, and `solve-field` solved it at RA 83.556°, Dec −5.412°, the
  same field as in Docker.
- **The link between the nodes is slow, and it dominates the guide
  path.** Pod to pod, with no INDI involved, the link carried
  76 Mbit/s. A guide-sized frame, 1280 by 960 at 16 bits and 3.34 MB on
  the wire as base64, took from the end of its exposure to the client:

  | Path | Median | p90 | Max |
  |---|---|---|---|
  | Docker bridge on one host | 13 ms | 14 ms | 15 ms |
  | One hop: CCD pod to the server, client on the server's node | 430 ms | 1,056 ms | 1,193 ms |
  | Two hops: CCD pod to the server to a client on the CCD's node | 1,082 ms | 1,590 ms | 1,885 ms |

  At 76 Mbit/s, one hop of 3.34 MB is about 350 ms, so the transfer
  accounts for most of each result. On 1 GbE the same arithmetic gives
  about 27 ms per hop, which was not measured. On a slow link, the
  guide camera's pod, the server, and PHD2 belong on one node, which
  plans 07 and 09 have to provide for.
- **A device's restart costs that device alone.** The mount's pod was
  deleted during a 15-second exposure: the exposure finished and wrote
  its frame, the CCD stayed connected, and the mount came back
  disconnected. The CCD's pod was deleted while the mount tracked: the
  mount's right ascension did not change, and it stayed connected.
- **The server stopped slowly until its grace period was cut.** The
  first restart of the server's pod took 35 seconds, because
  `indiserver` runs as process 1 with no handler for `SIGTERM`, and the
  old pod took 31.2 seconds to stop: the whole grace period, then a
  kill. A device pod, with `socat` as process 1, stopped in 2.1
  seconds. With `terminationGracePeriodSeconds: 1`, all three devices
  were on a new server 3 seconds after the old one was deleted.
- **A device that stays down costs one restart a minute.** With the
  mount scaled to 0 for 200 seconds, the server's log showed the shim
  give up and restart at 16:42:24, 16:43:24, and 16:44:24, as
  `restart #0`, `#1`, and `#2`, and nothing between. The mount was on
  the server 1 second after its pod was ready again.

Every restart left its device disconnected with its settings lost, as
in Docker, so a script configured the devices again. Plan 08's
reconciler replaces it. A server that starts long before its devices
was not tested on the cluster.

## Upstream issues

- [indi#1927](https://github.com/indilib/indi/issues/1927): a server
  exits when a server it chains stops. "Rejected: a server in each
  device's pod" above gives this as one reason against chaining.
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
- [Root plan 74](../../../plans/74-astrophotography.md): the two
  components and the contract between them
