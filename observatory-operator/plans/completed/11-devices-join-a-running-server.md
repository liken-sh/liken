# 11, Devices join a running server

Proposed and built on 2026-10-06, and tested against the fake API
server, the fake INDI servers, and a fake `indiserver` for the shim.
A local run of the real `indiserver` from the `indi` image confirmed
the fifo's commands. The drill ran on the two-node test cluster on
2026-10-06, on the development build `2026.10.04-004-dev-077-4a3db556`.
"What the test cluster measured" gives the results: no server
restarted, and the exposure finished. The drill found three defects,
which that section lists and this plan leaves open. This plan closes
the open problem "Adding a device restarts the server", which plan 07
recorded.

## The problem

A server pod's spec named every device of its server: an init
container made one link for each device, and `indiserver`'s arguments
named each link. A device that joined or left a telescope while a
reservation was `Ready` changed the digest of that spec, and the
operator replaced the server's pod. Every other device on the server
restarted and came back disconnected, and an exposure or a guide loop
in progress was lost. In plan 03 the restart took 3 seconds, and on
the test cluster the mount was connected again 7.2 seconds after the
server's pod was deleted. The operator posted a `ServerReplaced`
Warning for each such restart.

## The requirement

- A device that joins or leaves a telescope or the observatory while a
  reservation is `Ready` leaves the server and every other device
  connected: no restart of the server, and no lost exposure.
- The server pod's spec does not change when its devices change.
- The operator runs no command in the pod: `pods/exec` needs a heavy
  client and a broad grant.
- A joining device gets its pod, then its link and its `start`, then
  the connect and the settings that every device gets. A leaving
  device is stopped on the server, and its pod, `Service`, and claim
  are deleted.
- A server pod that fails, such as one that a node's eviction deleted,
  is still created again with every device.

## The design

### The fifo

`indiserver -f <fifo>` reads one command on each line of the fifo
(`indiserver/Fifo.cpp` at INDI 2.2.5, the release that `indi`
20261005 holds). `start <driver>` starts a driver, and `stop <driver>`
stops the running driver whose name is the same string. A driver from
the command line and one from `start` take their name from the same
string, and `indiserver` starts each with `execlp(name, name)`. So
both commands name the link's full path, such as
`start /run/indi/drivers/east-mount:7625`. A line that holds `@` names
a remote driver, and a `Service` address holds none. A `stop` sets the
driver's restart flag to false, sends `delProperty` for its devices to
every client, and kills the driver. With `-f`, a server whose last
driver stops keeps running. `indiserver` opens the fifo at start and
exits if the fifo does not exist, and it closes and opens the fifo
again each time a writer closes it.

### The shim runs the server

`indi-shim serve <drivers file> <links dir> <fifo> <indiserver> [arg...]`
is the server container's command. The shim:

1. removes every entry in the links directory, which is an `emptyDir`
   that keeps the links of a container that the kubelet started
   before;
2. makes the fifo, and opens it for reading and writing, which Linux
   allows with no wait for a reader, so no command is lost while
   `indiserver` opens its end again;
3. opens an inotify watch on the drivers file's directory, then starts
   `indiserver` with `-f <fifo>` added, then reads the file, so a
   change during the read wakes another read;
4. on each change, writes `stop` and removes the link for each device
   that left, and makes the link and writes `start` for each device
   that joined.

The shim is `indiserver`'s parent and exits when `indiserver` exits.
The kubelet then starts the container again, and the new server starts
with every driver of the file. So the shim's record of the running
drivers always describes the server it started. The shim is process 1
of the container and handles `SIGTERM`, which ends `indiserver`. Before,
`indiserver` was process 1 and ignored `SIGTERM`. The pod keeps its
grace period of 1 second.

### The operator writes an annotation

The operator writes the server's devices in the annotation
`observatory.liken.sh/drivers` of the server's pod, one address on each
line, such as `east-mount:7625`. The pod mounts the annotation as the
file `/etc/indi/devices/drivers` through a downward API volume. The
annotation is metadata, and the digest covers only the spec, so a
change of the devices replaces no pod. The operator changes the
annotation with a merge patch, which needs `patch` on `pods`.

A ConfigMap volume was the other choice. The kubelet writes a
ConfigMap volume again only at its next sync of the pod, up to about a
minute after the change. It writes a downward API volume again on each
update of its pod: `WaitForAttachAndMount` calls `ReprocessPod`, whose
comment names the downward API as the reason, and the populator and
the reconciler each loop every 100 ms (`pkg/kubelet/volumemanager` at
Kubernetes 1.34.0). So the file follows the annotation within about a
second of the kubelet's watch delivering the update. That figure is
reasoned from the source and not measured; the drill measures it.

A server whose pod is created holds every device in the annotation it
is created with. `driversMemo` records the annotation that the
operator wrote last on each pod, by the pod's UID. A store's copy can
be older than the patch, and a pass that read it would patch again
and post its Events again.

### The order of a pass

A `Ready` reservation's runner keeps each of its two servers, the
telescope's and the observatory's, in this order (`keepServer`):

1. create the server's pod if it is gone;
2. create the pod of each device that is gone;
3. patch the annotation to the devices on the server;
4. delete the pod, the `Service`, and the claim of each device that
   left.

So a joining device's pod exists before its shim dials it, and a
leaving device's driver stops before its pod goes. If the pod went
first, the shim's connection would end, and `indiserver` would start
it again until the `stop` arrived.

### The Events

The `ServerReplaced` Warning is removed. The runner posts a Normal
Event on the `Telescope` or the `Observatory` for each driver it
starts or stops on a running server: `DriverStarted`, "Started the
driver of SkyQualityMeter east on server east-telescope while
Reservation east-tonight is Ready. The server did not restart.", and
`DriverStopped` in the same form. A device that left still gets
`PodDeleted`, and a server pod that the runner creates again still
posts `PodCreated`.

## What was built

- `indi/shim`: the `serve` mode (`serve.go`, `watch.go`). The `link`
  mode and the init container that ran it are removed. The `indi`
  images are revision 5, `20261005-5`.
- `observatory-operator`: the server pod with one container and the
  downward API volume (`pods.go`), the patch, the memo, and the Events
  (`serverdrivers.go`), the order of a pass (`steady.go`), and the
  grant of `patch` on `pods`. The driver map pins `20261005-5`.

## What the tests showed

The shim's tests run the `serve` mode against a fake `indiserver`: the
test binary, run again as a child, which reads the fifo and prints
each line. The drivers file is written the way the kubelet writes a
downward API volume, with a rename of `..data`. The tests show a
`start` for each device at start, a `stop` and a `start` for only the
devices that changed, a stale link removed at start, a server with no
devices that starts the first one that joins, and an exit when
`indiserver` exits. Each test takes under a second.

The operator's tests run in a `synctest` bubble. The fake INDI server
now reads each server's drivers from the annotation, and keeps the
drivers it has across a change. With the camera's `CCD_EXPOSURE`
`Busy`:

| Test | Result |
|---|---|
| A `SkyQualityMeter` joins `east` while `Ready` | The server pod's UID did not change, the meter was connected with the 12 other devices, the exposure stayed `Busy`, one `DriverStarted`, and no Warning |
| The `Focuser` leaves `east` for the shelf while `Ready` | The pod, `Service`, and claim were deleted, the server's UID did not change, the exposure stayed `Busy`, `DriverStopped` and `PodDeleted` |
| The `SkyQualityMeter` leaves the observatory while `Ready` | The observatory's server kept its UID and the dome stayed connected, one `DriverStopped` on `Observatory` `lab` |
| The server pod is deleted while `Ready` | A new pod with every device in its annotation, and no `DriverStarted` |

The fake applies an annotation at once, so each change took no time on
the bubble's clock, and each test ran in 0.12 s. The time on a cluster
is the kubelet's delay plus a driver's start, which the drill
measures.

### The local run

On the laptop, the `indi` image `20261005-5` ran `indi-shim serve`
with the restricted settings of the pod: user 1000, a read-only root
filesystem, and no capabilities. Two `indi-simulators` containers
served the telescope and the focuser simulators through `socat`, and
a host directory held the drivers file in the kubelet's layout:

- The server started the telescope from the file, and a client
  connected it.
- A rename that added the focuser wrote `start`. `indiserver` logged
  "Starting driver", and the focuser appeared, disconnected. The
  telescope stayed `CONNECT=On`.
- A rename that removed the focuser wrote `stop`. `indiserver` logged
  "Shutting down driver" and sent `delProperty` for the focuser. The
  telescope stayed `CONNECT=On`.
- The container restarted 0 times, and `docker stop` ended it in 84 ms.
- The image's smoke check, `indi/smoke/indi.sh`, passed.

## What the test cluster measured

The operator ran from `2026.10.04-004-dev-077-4a3db556`, applied by
Flux from the deploy artifact, with `examples/simulators.yaml` as the
inventory and both lock policies set. The drill reserved `east` by
hand, and it was `Ready` in 2 min 33 s, most of it image pulls of the
`indi` revision 5. A port-forward to `east-telescope` carried a small
INDI client that timestamped every `CONNECTION`, `CCD_EXPOSURE`, and
`delProperty`, and a second one did the same for `lab-observatory`.
Each edit was a `kubectl patch` of `spec.opticalTrain` or
`spec.observatory`, timestamped on the laptop. The shim's `start` and
`stop` times are from the server's log, and the `Event` times from
`kubectl get events -w`.

A 300 s exposure on `Camera east-main` started before the first edit.
It counted down once a second through every edit and ended `Ok`
301 s after it started. The `east-telescope` pod kept its UID and a
restart count of 0, and each device that the edit did not name stayed
`CONNECT=On` in the client's view.

| Edit | Edit to the shim's line | Edit to the device's definition | Edit to `CONNECT=On` |
|---|---|---|---|
| `Focuser spare` joins `east-imaging` | 0.34 s, `start` | 2.62 s | 2.63 s |
| `Rotator east` joins `east-imaging` | 0.42 s, `start` | 1.47 s | 1.48 s |
| `Focuser east` joins `east-imaging` | 0.31 s, `start` | 2.61 s | 2.62 s |
| `SkyQualityMeter lab` joins `lab` | 0.32 s, `start` | 1.35 s | 1.35 s |
| `Focuser east` leaves | 0.22 s, `stop` | | |
| `Rotator east` leaves | 0.17 s, `stop` | | |
| `Focuser spare` leaves | 0.15 s and 0.26 s, `stop` | | |
| `SkyQualityMeter lab` leaves | 0.20 s, `stop` | | |

So the kubelet wrote the downward API file within 0.15 s to 0.42 s of
the patch, under the estimate of about a second. The rest of a join is
the device's pod starting. The `DriverStarted` and `DriverStopped`
`Event`s posted within 0.1 s of each patch, and each leaving device's
pod, `Service`, and claim were gone within 1.7 s. The
`lab-observatory` pod kept its UID and a restart count of 0, and the
dome and the weather station stayed connected.

Deleting the `east-telescope` pod created a new one 0.55 s later with
12 drivers in its annotation, and the server started all 12. Every
device was `Connected` 3.2 s after the delete. The runner posted
`PodCreated` on the `Telescope`, and no `DriverStarted`.

### The defects the drill found

- **A leaving device's pod goes before its driver stops.** The runner
  patches the annotation and then deletes the pod at once, so the
  order is of the API calls, not of their effect. In three of the five
  departures, the pod's connection ended 50 ms to 75 ms before the
  shim wrote `stop`. `indiserver` logged `read EOF` and `restart #0`,
  started the driver again, and the `stop` then ended it. No other
  device was affected. A fix needs a signal that the driver stopped,
  such as the server's `delProperty` for the device, and a bound on
  the wait.
- **A second device with the same driver breaks the first.** The
  first edit put `Focuser spare` into `east-guiding` while `Focuser
  east` ran on the same server. The runner started the second driver,
  which defined `Focuser Simulator` again, disconnected. Both
  `Focuser`s then reported `Error` with "same driver as another device
  on this server". Putting `spare` back on the shelf stopped its
  driver, and `indiserver`'s `delProperty` for `Focuser Simulator`
  removed the running `Focuser east` from every client's view. The
  operator's client kept `Focuser east` in `Starting` until another
  client's `getProperties` made its driver define the device again,
  52 s later. The check for a shared driver runs only when the
  operator looks up a device's INDI name, after the driver started.
- **A new server leaves PHD2 disconnected.** After the server pod was
  replaced, PHD2 reported "INDI server disconnected" and stayed
  `Stopped`, and the `Guider` stayed `Activating`, "Waiting for PHD2 to
  connect its camera and mount", when the drill checked it 74 s later. The
  runner connects PHD2's equipment once for each guider pod, and the
  guider pod did not change.

The example's comment on the spare focuser still says that setting
`opticalTrain` restarts the telescope's INDI server, and installing it
on `east` beside `Focuser east` hits the second defect.

## The drill

On the two-node test cluster, with a reservation of `east` `Ready` and
the camera exposing:

1. Add a `SkyQualityMeter` to `east`. Measure the time from the edit
   to `Connected`, read the server pod's UID and restart count, and
   check that the exposure finished.
2. Put the `Focuser` on the shelf. Check its pod, `Service`, and claim,
   and the Events on the `Telescope`.
3. Repeat both with a device of the `Observatory`.
4. Delete the server's pod, and check that the new pod starts every
   driver and that the operator connects each device again.

## References

- `indiserver/Fifo.cpp`, `indiserver/DvrInfo.cpp`, and
  `indiserver/LocalDvrInfo.cpp` in `indilib/indi` at `v2.2.5`
- `pkg/kubelet/volumemanager/volume_manager.go` and
  `populator/desired_state_of_world_populator.go` in
  `kubernetes/kubernetes` at `v1.34.0`
- [Plan 03](03-the-topology-by-hand.md), the shim, and [plan
  07](07-the-operator-runs-the-topology.md), "Not built yet"
