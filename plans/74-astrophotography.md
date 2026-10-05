# 74, Astrophotography on a `liken` cluster

Not built. This plan records the design of an astrophotography rig that
a `liken` cluster runs. The architecture was tested on 2026-10-04 with
the INDI simulators in Docker on one workstation, and the evidence
section gives what each test measured. Nothing in this plan ran on a
cluster. The facts about upstream projects come from their
documentation and source as of September and October 2026, and a fact
that was not checked is marked as such.

## The idea

A telescope rig is a set of USB and serial devices on one machine next
to the mount: the mount, a main camera, a filter wheel, a focuser, and a
guide camera. Today a small computer runs the rig, usually a Raspberry
Pi with a distribution such as StellarMate or AstroArch, and a person
drives it from a desktop application.

On a `liken` cluster, a small x86-64 machine at the mount joins the
cluster as one more node, because `liken` does not run on ARM. A rig
can also span two machines at the pier, for example one for the
cameras and one for the mount. Machines elsewhere can run a weather
station or a roll-off roof. Each device runs in its own pod with its
own DRA claim. The rest of the cluster stacks the frames, and a screen
in the house shows the session.

The rig in this plan does not travel. A rig at a remote dark site has
no control plane, and a one-node cluster adds little over the
distributions above.

## Two modes and two components

The design has two modes, and the second mode is built on the first.

1. **The rig as a service.** The cluster runs the devices and serves
   them on one INDI endpoint. A person connects KStars to that endpoint
   and drives the night with Ekos, by schedule or by hand.
2. **The orchestrated night.** An operator plans the night from
   declared targets and drives the same endpoint as its client. This is
   the end goal.

The two modes split into two components, with dependencies that go in
one direction, the same way library-operator depends on media-operator.

- **`observatory-operator`** is the hardware control layer, with the
  API group `observatory.liken.sh`. It owns the device pods, the INDI
  server, the guider, and the reconciler that configures each device.
  It has no information about targets or nights. Mode 1 is this
  component alone. It is named for the observatory, not the telescope,
  because it also controls the devices that several telescopes share:
  a weather station, a cloud sensor, a safety monitor, and a roof.
- **`astrophotography-operator`** drives imaging. It owns `Target`, the
  planner, and the frame rows in the `Catalog`. It depends on
  `observatory-operator`, and `observatory-operator` does not depend
  on it.

library-operator starts playback through a Kubernetes resource, a
`Play`. `astrophotography-operator` cannot drive the rig that way, because an
exposure, a dither, and a guide pulse happen too often to be objects in
the API. So the contract between the two components is a resource that
names a network endpoint: the rig's INDI server is at this `Service`.
`status.idle` hands a delegate its bus topics in the same way. A field
on the rig's resource records which client drives the rig, so a person
can take the rig from the operator with KStars and give it back.

## What stays upstream

The components speak to existing programs and replace none of them.

- **INDI** is the device layer. Release 2.2.3 shipped on 2026-08-01
  ([releases](https://github.com/indilib/indi/releases)). The core
  drivers and the simulators ship with the library. Vendors maintain
  their drivers in `indi-3rdparty`. Some camera drivers link closed
  vendor SDKs, for example ZWO's `libASICamera2`.
- **PHD2** guides the mount. It is the only guider on Linux that runs
  as a standalone program and has an API: a JSON-RPC event server on
  TCP port 4400. The other guiders are built into larger programs.
  Ekos has an internal guider that descends from lin_guider, and
  CCDciel has its own. INDIGO's guider agent belongs to INDIGO, a
  separate device framework.
- **A plate solver** turns a frame into coordinates. astrometry.net's
  `solve-field` solved a simulated frame in 1.2 seconds. In mode 1,
  KStars solves on the client side with StellarSolver and its own
  index files, so the cluster needs no solver for that mode.

`astrophotography-operator` does not drive the rig through Ekos. The Ekos
Scheduler is scriptable over DBus
([docs](https://kstars-docs.kde.org/en/user_manual/ekos-scheduler.html)),
and it would bring years of focus, solve, and flip logic. It would also
put KStars, a Qt desktop application, inside a pod, and make its DBus
interface the operator's real API.

## The rig: one INDI server, one pod per device

`indiserver` starts each driver as a child process and connects to the
child's stdin and stdout (`indiserver/LocalDvrInfo.cpp`). The server
routes each message by device name. It also delivers the properties
that one driver reads from another, which INDI calls snooping. The CCD
driver snoops the mount's coordinates to write `OBJCTRA` and `OBJCTDEC`
into each FITS header. A dome snoops the mount to follow it.

The executable that `indiserver` starts does not have to be the driver.
In this design it is a shim that connects the server to a driver in
another pod:

```
 pod: eqmod      claims the mount's tty
   socat TCP-LISTEN:7625,reuseaddr EXEC:indi_eqmod_telescope,pipes
 pod: asi-ccd    claims the main camera
   socat TCP-LISTEN:7625,reuseaddr EXEC:indi_asi_ccd,pipes
 pod: efw        claims the filter wheel
   socat TCP-LISTEN:7625,reuseaddr EXEC:indi_asi_wheel,pipes
                        ▲
                        │ the driver's stdin and stdout, over TCP
 pod: indiserver  (no claims)
   indiserver -r 1000 shim-eqmod shim-asi-ccd shim-efw
     each shim: socat STDIO TCP:<device service>:7625,retry=120,interval=1
   reconciler sidecar
                        ▲
                        │ INDI on TCP 7624
        KStars, astrophotography-operator, PHD2
```

Each device has its own pod, its own claim, its own image, and its own
log stream. A device pod that restarts costs only that device: in the
tests, the main camera finished a 15-second exposure while the mount's
pod restarted, and the mount kept tracking while the camera's pod
restarted. Snooping needs no extra configuration, because every driver
is a child of the same server.

The server pod has no hardware. When it restarts, every shim exits, each
driver reads EOF on stdin and exits, and every device pod restarts.
In the test, both devices were on the server again 1 second after a
server restart. The exposure in progress is lost, and so is every
device's configuration.

### Why each device pod does not run its own server

`indiserver` can chain another server: a driver argument of the form
`Device@host:port` forwards that device from a remote server. A chain of
per-device servers was the first design, and three properties of
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
does not fix this. A cycle has no order. And when a server restarts,
the servers that chain it exit at once, before any operator can act.

### Why the devices do not share one pod

One pod with one `indiserver` and every device claim is how a Raspberry
Pi rig runs today. A pod's claims are allocated when the pod is
scheduled, so the pod does not start until every claimed device exists.
An unplugged focuser would keep the camera from running. Separate
device pods keep each device's claim independent.

### The reconciler

Every restart in the tests left its device disconnected
(`CONNECTION.CONNECT=Off`) with its settings gone: the focal length,
the upload mode and directory, and the mount's position. INDI writes
settings to `~/.indi/*_config.xml`, which was in the container's own
filesystem. A real mount keeps its position in its own hardware; the
simulator does not.

A reconciler in the server's pod connects each device and applies the
settings from the rig's resource when the device appears on the
server. It applies them only then. A reconciler that applied the
settings again on every change would undo what KStars changes in mode
1, such as `UPLOAD_MODE`.

## Frames

A 26-megapixel 16-bit frame is about 52 MB of FITS. A camera driver has
three upload modes (`libs/indibase/indiccd.cpp`):

- `UPLOAD_CLIENT` sends the frame to clients as a BLOB, base64 in the
  XML stream.
- `UPLOAD_LOCAL` writes the frame to a directory on the driver's
  filesystem and sends only its path in `CCD_FILE_PATH`.
- `UPLOAD_BOTH` does both.

Each client chooses whether it receives BLOBs with `enableBLOB`, and the
server's default for each client is `Never`. In mode 2 the camera uses
`UPLOAD_LOCAL` or `UPLOAD_BOTH`, and the frame is written to a volume in
the camera's pod. `astrophotography-operator` never sends `enableBLOB`, so it
receives the path and none of the frame's bytes. In mode 1, KStars
enables BLOBs and receives each frame through the server, as with any
remote Ekos setup.

The shim needs the `pipes` option of `socat`. A driver checks whether
its stdout is a Unix socket (`is_unix_io()` in
`libs/indibase/indidriverio.c`). If it is, the driver sends each BLOB as
a file descriptor, not as text. `socat` gives a child a Unix socket pair
by default and drops the descriptor, and the camera driver crashed on
its first BLOB in the test. With `pipes`, the driver writes base64, and
the frame reached a client through the server. Base64 is about 1.37
times the frame's size, so a 52 MB frame is about 71 MB on the pod
network. That traffic exists only while a client receives BLOBs.

### Grading

The planner needs the number of accepted frames for a target, not the
number taken. A frame with a passing cloud, wind shake, a lost guide
star, or drifting focus is rejected. Grading measures each frame's star
count, half-flux radius, and star shape, and compares them with the
other frames of the session.

Grading reads the whole frame, so it runs in the data plane: a grader
in the camera's pod reads each new file from the volume and writes a
row to the `Catalog`. It finds new files with an inotify watch followed
by one look at the directory. `astrophotography-operator` reads the counts
from the `Catalog` and never opens a frame.

Stacking runs later, anywhere: a Siril `Job` on a machine with more
cores reads the accepted frames. A stack of the frames captured so far
can run during the night, to show progress.

## Guiding

PHD2 runs in its own pod as a client of the INDI server, like KStars.
It drives the guide camera and the mount through INDI, and it holds no
device claims. Its guide frames go from the guide camera's pod through
the server to PHD2 as BLOBs. `astrophotography-operator` and KStars both
drive PHD2 the same way, over port 4400, so the guider belongs to
`observatory-operator`.

PHD2 has no headless mode. Its command line takes only an instance
number, a settings file to load or save, a reset, and the version
(`src/phd.cpp`). It is a wxWidgets program on GTK, so it needs a
Wayland compositor. The guider pod runs the `weston` image as a
sidecar with the headless backend, and PHD2 connects to it through a
Wayland socket in an `emptyDir`. The `weston` image builds on Debian
and PHD2 on Ubuntu, so the two stay in separate containers. When a
person wants PHD2 on a monitor, the pod takes a display claim and
PHD2 draws on display-operator's compositor, which also brings that
operator's screenshots and captures.

The headless weston advertises no `wl_seat`, and wxGTK crashes in any
modal dialog without one.
[The compositor advertises no seat](../display-operator/plans/open-problems/the-compositor-advertises-no-seat.md)
records the evidence and the remedies. PHD2 opens two modal dialogs at
startup, and the guider pod prevents both. Which errors PHD2 reports in
a modal dialog during a night was not checked, so PHD2 on weston stays
fragile until the compositor has a seat. Under `cage`, which advertises
a seat, PHD2 guided with a modal dialog open.

PHD2 selects its camera and mount from its profile, and its event API
cannot select them. PHD2 also rewrites its config while it runs: in the
tests, it moved `currentProfile` to a new, empty profile. So the guider
pod writes the whole of `~/.PHDGuidingV2` before each start and keeps
none of it between runs. The file holds these keys:

- `ConfigVersion=2001`. Without it, PHD2 treats the config as new and
  opens its modal first-light wizard (`src/phdconfig.cpp`).
- `/profile/<n>/camera/LastMenuChoice`, set to `INDI Camera [<device>]`,
  and `/profile/<n>/scope/LastMenuChoice`, set to
  `INDI Mount [<device>]`.
- The server's host and port, and the device names, under
  `/profile/<n>/indi/`.

PHD2 writes its process ID to the instance lock `~/phd2.1`. In a
container PHD2 is always process 1, so a lock left by a crash matches
the next PHD2 itself. PHD2 then reports that another instance is
running, in a modal dialog, and quits. So PHD2's container removes the
lock when it writes the profile, before it starts PHD2. An `emptyDir`
alone does not prevent this, because it keeps its files when a
container in the pod restarts.

## Images

observatory-operator plans [01](../observatory-operator/plans/completed/01-the-indi-base-image.md)
and [02](../observatory-operator/plans/completed/02-driver-and-simulator-images.md)
replace the layers below with library closures on `scratch`, measured
on 2026-10-05: a 71 MB base with every core driver, and one image for
each vendor SDK family.

The images build on Ubuntu with the INDI PPA (`ppa:mutlaqja/ppa`), not
on the Debian snapshot that the other bases use. Debian packages INDI
1.9.9 in sid and forky, and 2.2.5 only in experimental. It packages
about 35 third-party drivers, many from 2022 and 2023, and no QHY
driver. A vendor SDK from 2022 does not support a camera released
after it.

The layers follow the bases that the repository already has:

```
 indi-core         Ubuntu 26.04, the PPA's indi-bin and libindi, socat
  ├─ indi-simulators   + gsc and gsc-data, for tests only
  ├─ indi-eqmod        + one driver
  ├─ indi-asi          + one driver and the ZWO SDK
  └─ the server pod    indi-core as it is
```

Every pod on a rig's node shares the `indi-core` layers. The simulators
ship in `indi-bin`, so one image holds all of them. Only the CCD and
guide simulators use `gsc`, to draw catalog stars.

Each driver image is a recipe in this repository, added when a rig
needs that driver. `ci/` builds a component only when its inputs
change, so a recipe that does not change costs nothing in CI. For
Ubuntu 26.04 the PPA publishes all third-party drivers as one package,
`indi-3rdparty-drivers`, so a single-driver image either copies one
driver and its files out of that package in a build stage, or builds
the driver from its directory in `indi-3rdparty`. The test images were
480 MB with `indi-bin` and `gsc`, and 938 MB with all 348 driver
binaries.

The PPA keeps only the newest build of each package, and its version
strings carry build timestamps from the day of the build. A pinned
image cannot be built again from the PPA later. The pinned base keeps
the `.deb` files it installs, or builds from the PPA's source packages.
The base image also needs `ca-certificates` before apt can read the
PPA over HTTPS.

## The INDI client in Go

`astrophotography-operator` and the reconciler are INDI clients in Go that
speak the wire protocol directly, the way `equipment-operator/cec/`
speaks the kernel's CEC API with no libCEC and no cgo. No Go client is
maintained: [`goastro/indiclient`](https://github.com/goastro/indiclient)
last changed in March 2020, and its fork `jnmorley/indiclient` in
October 2023. The protocol is small. The client sends `getProperties`,
and the server answers with a `def*Vector` for each property, which is
the baseline, and then sends a `set*Vector` for each change on the same
connection. That order follows the repository's rule for events.

The client converts between epochs. The mount reports JNow in
`EQUATORIAL_EOD_COORD`, and `solve-field` reports J2000. For the test
target near M42 in 2026, the difference was about 20 arcminutes in
right ascension, so centering without the conversion misses by that
much.

## Plate solving

`solve-field` runs in the camera's pod as a sidecar, because a solve
during centering waits on the result and the frame is already on that
pod's volume.

The index files are a per-node cache from per-node-csi-driver. A pod
that returns to a node finds its files, and a pod on a new node fetches
them again. The driver keeps no size budget, so the reconciler deletes
index files that the rig's optics no longer use. astrometry.net
recommends index files whose quads span about 10% to 100% of the field
width, so a fixed rig needs a few hundred megabytes to a few gigabytes.
That range was not computed for any specific optics. The full set of
2MASS and Tycho-2 index files in the Ubuntu packages is 9.9 GB.

data.astrometry.net serves the `4100` series (Tycho-2, last modified in
2013) and the `4200` series (2MASS). It links the newest series,
`5200`, built from Gaia, to a `temp` directory on `portal.nersc.gov`.
The cluster fetches index files from a mirror in the project's object
storage, next to the release channel that
[plan 26](completed/26-the-public-release-channel.md) set up.

## Devices

Most of a typical rig works with the DRA claims `liken` has today. A
mount or focuser behind an FTDI, PL2303, or CH340 chip publishes a tty.
A ZWO filter wheel is a HID device, so it publishes a device with its
usbfs node.

A ZWO ASI camera has no kernel driver, so `liken` does not publish it.
[Driverless USB devices are not published](../liken/plans/open-problems/driverless-usb-devices-are-not-published.md)
records that problem. Hot-plug has to reach the `ResourceSlice`
promptly, which
[the device inventory waits for the ticker](../liken/plans/open-problems/device-inventory-waits-for-the-ticker.md)
records. Some cameras, QHY among them, load firmware through udev rules
on the host, and those rules do not run inside a container
([indi-docker](https://github.com/seanhoughton/indi-docker)).

display-operator keeps a dark monitor's device in the `ResourceSlice`,
so a reseated cable does not end the pod that claims it. A rig's USB
devices need the same treatment. A replugged USB serial device can also
come back on a new node, such as `/dev/ttyUSB1` in place of
`/dev/ttyUSB0`, and a container's device nodes are set when it starts.
How `liken` injects USB device nodes was not checked for this plan. If
a replug needs a pod restart, it costs only that device. For a cooled
camera, that cost includes several minutes to reach its temperature
again.

## Resources

A first sketch, with names that are not settled. [Plan 06 of
`observatory-operator`](../observatory-operator/plans/completed/06-the-resources.md)
settles the names of the hardware layer.

```yaml
kind: Telescope
spec:
  devices:
    mount: EQMod Mount
    camera: ZWO CCD ASI2600MM Pro
    filterWheel: ZWO EFW
    focuser: Pegasus FocusCube
    guideCamera: ZWO CCD ASI120MM Mini
  optics: {focalLength: 360mm}
---
kind: Target
spec:
  telescope: backyard
  coordinates: {ra: "05h35m17s", dec: "-05°23'28\""}
  rotation: 90deg
  constraints: {minAltitude: 30deg, moonSeparation: 60deg}
  plans:
    - {filter: Ha, exposure: 300s, wanted: 60}
    - {filter: OIII, exposure: 300s, wanted: 60}
status:
  plans:
    - {filter: Ha, taken: 41, accepted: 37}
```

`Telescope` belongs to `observatory-operator` and `Target` to
`astrophotography-operator`.

A frame is not a Kubernetes object. A night produces hundreds of
frames, and each has a path, a grade, a half-flux radius, and a star
count. The frames are rows in a `Catalog`, the SQLite database that
library-operator replicates with Corrosion. `status` carries only the
counts.

## The planner

The planner chooses the next exposure, not the whole night. After each
exposure it filters the targets and scores the rest, the same shape as
kube-scheduler's filter and score plugins. The filters remove a target
that is not up long enough, is too close to the moon, or has no plan
that the current twilight allows. The score favors a target that sets
soonest, a target that is nearly complete, and the target already on
the camera, so the rig does not switch back and forth.

NINA's Target Scheduler plugin uses the same method: it plans one
exposure at a time and plans again after each one
([planning engine](https://tcpalmer.github.io/nina-scheduler/concepts/planning-engine.html)).
The method keeps working when a cloud, a failed solve, or a lost guide
star breaks the plan an hour in. A night planned in advance does not.

The planner still predicts the night. `status` carries the order and the
window the scorer expects for each target, as a preview that the next
choice can replace.

A pass runs on events: an exposure completes, the safety state changes,
or PHD2 reports a lost guide star. Target rise and set times and
twilight are clock times, so a timer for them is correct.

Autofocus is the least certain part of `astrophotography-operator`. It fits a
V-curve to the half-flux radius of the stars, which is simple to state
and slow to tune on real skies. The CCD simulator reads the focuser
simulator's position and seeing to set the size of its stars, so the
fit can be tested before a real night.

## The screen

A screen in the house shows the session: the current target, the
progress of each plan, the last frame, and the latest stack. It runs as
a delegate of a `Player`'s idle screen through
`spec.idle.controller`, which is how the media browser takes a
`Player`'s screen today. It reads the display claim, the draw request,
and the bus topics from `status.idle`.

The screen never talks to INDI. A person's action edits a resource:
skipping a target sets a field on the `Target`, and pausing sets one on
the `Telescope`. The operator stays the only writer to the rig in mode
2, and every action is on record in the API.

A guiding view on the screen draws from PHD2's event API, not from
PHD2's window. The window cannot hide its menu bar or status bar, and
PHD2 has no full-screen mode. The API sends a `GuideStep` event for
every frame, with the error in each axis, the SNR, and the half-flux
diameter. `get_star_image` returns the pixels around the guide star,
and `save_image` writes the current frame to a FITS file. A view built
on those works when nobody shows PHD2's window.

A screen next to the rig must keep night vision, with red colors and a
low backlight. display-operator can already set a panel's brightness.

## Security

INDI has no authentication. Any client that reaches port 7624 can slew
the mount, and any client that reaches port 4400 can command PHD2. The
server and guider pods need a `NetworkPolicy` from the start, and mode
1 needs a deliberate way for KStars to reach the server from outside
the cluster.

## Adjacent work this plan does not cover

The screen shows that some pieces of the media stack serve more than
media. Each needs its own plan, and this plan does not depend on them.

- **The bus.** `media-operator/deploy/bus.yaml` installs the Mosquitto
  broker at `bus.liken-system.svc:1883`. media-operator, the pods it
  starts, and library-operator's screens connect to it. Root plan 77
  took equipment-operator off it, so the bus carries only the media
  domain today. The rig does not use the bus, because INDI carries
  every device message. Only the screen in the house would make an
  astronomy component a client of the bus, and the first one outside
  the media domain. The broker could become its own component.
- **Input.** `Remote`, `Keymap`, and the reader pod in media-operator
  turn a controller's events into presses, and none of that is specific
  to media. They could move to an input operator. The focus mark in
  `media-operator/focus.go` names a `Player`, and it would split into
  two questions: which screen a controller drives, and which program on
  that screen receives the presses.
- **Switching programs on a screen.** A person needs a way to move a
  screen from one program to another, for example from the media
  browser to the astronomy screen, with a press. Today a person edits
  `spec.idle.controller`.

## Evidence

Every test ran on 2026-10-04 on one workstation with Ubuntu 26.04, INDI
2.2.4 and 2.2.5 from the PPA, and Docker. Containers on one Docker
bridge network stood in for pods. `docker run --restart always` stood
in for the kubelet's restarts. No test ran on a cluster or on a real
rig.

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
| PHD2 2.6.14 under `cage` 0.2.1, headless, as a client of the server | At 240 mm, every frame was rejected with `Star lost - low HFD`, because the stars were smaller than a pixel at 4.5 arcseconds per pixel. At 1000 mm: calibration in 38 seconds and 19 steps, then 190 guide steps over 3 minutes with the star found in every frame. One step took 1.06 seconds median and 1.16 seconds at most with 1-second exposures. |
| PHD2 on `ghcr.io/liken-sh/weston:20260928-1`, headless, in a separate container | Two crashes in modal dialogs, with no `wl_seat`: a stale `~/phd2.1` lock, then the first-light wizard. With `ConfigVersion=2001` and no lock: calibration in 19 steps, then 124 guide steps over 2 minutes, 1.07 seconds median and 1.08 seconds at most per step, with the star found in every frame. |
| PHD2 under `cage` with the first-light wizard open | PHD2 calibrated and guided with the modal dialog on the screen. |

The PHD2 run tests the path of frames and pulses, not guiding quality.
The simulators' settings for periodic error and drift were left at
their defaults, and the run did not record those defaults.

## Open questions

- How the pod network changes the timing. The tests ran on one host's
  bridge. A 3.3 MB guide frame takes about 27 ms per hop on 1 GbE, by
  calculation, and the guide frames cross two hops when the camera's
  pod and PHD2 run on different nodes.
- How `liken` injects USB device nodes, and whether a replugged device
  needs its pod restarted.
- Whether the rig's resource states the camera's sensor, or the
  reconciler reads the field of view from `CCD_INFO`. The plate solver's
  index files depend on it.
- How a stacking `Job` on another node reads the frames that the camera
  wrote to a volume in its own pod: shared storage, or a copy from a
  per-node volume.
- Whether a single-driver image copies its driver out of
  `indi-3rdparty-drivers` or builds it from source.
- How calibration frames work: a library of darks by temperature, gain,
  and exposure, and flats that need the rig.
- Where safety comes from: weather, a cloud sensor, and later a roof.
  KStars 3.8.0 added support for the INDI Safety Monitor driver
  ([release](https://knro.blogspot.com/2025/12/kstars-v380-is-released.html)).
- Whether the screen is part of `astrophotography-operator`.
- How an observatory with more than one telescope shares one sky, one
  roof, and one safety state.
