# 09, The guider

Proposed on 2026-10-05. The guide camera's placement was built on
2026-10-05. The rest of the operator's side was built on 2026-10-06 and
tested against the fake API server and a fake PHD2 event server: the
event client in `phd2/`, the profile, the guider's pod, the steps
`StartGuider` and `StopGuider`, the `Guider`'s status, and the example.
The PHD2 image `indi-phd2` is built in `indi/`. Drilled on 2026-10-06
on the two-node test cluster, with the operator's development build
`2026.10.04-004-dev-048-82139946`. "What the test cluster measured"
gives the results. The drill found no defect. Later on 2026-10-06, two
more drills ran on the same cluster and build: a guider pod deleted
while `Ready`, and an operator restart while PHD2 guides. Neither found
a defect.

## The problem

PHD2 guides the mount, and both KStars and `astrophotography-operator`
drive it over its event API on port 4400. PHD2 is a desktop program
with no headless mode, and three properties of it break in a container:
it needs a Wayland compositor, it opens modal dialogs that crash it on
a compositor with no seat, and its instance lock outlives a crash.

## The requirement

A `Guider` belongs to a `Telescope` and names the `OpticalTrain` whose
camera guides, as plan 06 describes. Its pod runs PHD2 from its PPA as
a client of the telescope's INDI server,
with no device claims, and the `weston` image as a headless sidecar.
Before each start, the pod writes PHD2's whole profile, including
`ConfigVersion=2001` and the INDI camera and mount, and removes the
`~/phd2.1` lock. A display claim puts PHD2 on a monitor when a person
wants to watch it. The display claim is not built.

[The compositor advertises no seat](../../../display-operator/plans/open-problems/the-compositor-advertises-no-seat.md)
stays open until display-operator decides on a remedy. Until then, a
modal dialog during a night ends PHD2, and the pod restarts it.

On a slow link, PHD2's guide frames cost a hop each way: plan 03
measured 1,082 ms from the end of an exposure to a client two hops
away, at 76 Mbit/s, against 13 ms on one host. The guider's pod runs on
the node of the server and the guide camera when the link is slow.

The operator of plan 07 places no pod, so this plan places all three:
the telescope's server, the guide camera's pod, and the guider's pod.
On the two-node test cluster, the scheduler put the guide camera's pod
on the other node from the server, so each guide frame crossed the
76 Mbit/s link.

### The guide camera's placement is built

Built on 2026-10-05, before the guider's pod, and tested against the
fake API server in `placement_test.go`. Drilled on 2026-10-06 with the
guider: the guide camera's pod and the guider's pod ran on the node of
the server.

The camera of the `OpticalTrain` that a `Guider` names has a required
pod affinity to its telescope's server pod, on the node label
`kubernetes.io/hostname`. With no `Guider`, no pod has an affinity.

The camera follows the server, not the reverse. `PowerOn` creates the
server's pod before the guide camera's pod exists, and the `Switch`
that can power the camera runs on that server. A server with an
affinity to the camera would wait for a pod that waits for the server.

A guide camera with a `spec.claim` gets no affinity. The node of its
device decides where its pod runs, so an affinity to a server on
another node would hold the pod Pending until the step's deadline. On
real hardware, the server still lands on any node, and a guide frame
crosses the link when the server and the camera are apart. Placing the
server on the node of the claimed devices stays with this plan, with
the guider's pod.

## The design, as settled on 2026-10-06

1. **The split of control.** The operator starts PHD2, connects it to
   its camera and mount, and leaves it idle. It never calibrates,
   loops, or guides. Tracking stays off after `Prepare` until the
   holder aligns the mount, so calibration and guiding belong to the
   holder: a person's KStars, or `astrophotography-operator` later. The
   operator reads PHD2's state back over the event API into the
   `Guider`'s status.
2. **The compositor** is the repository's `weston` image, headless, in
   a native sidecar: an init container with `restartPolicy: Always`,
   whose startup probe runs `wayland-info`, so PHD2 starts only when
   the socket answers. It runs with the arguments and the `weston.ini`
   of `weston/smoke/weston.sh`: `--backend=headless`, `ivi-shell`,
   `liken-layout.so`, and `require-input=false`. The image is the one
   that `display-operator` builds on, at the tag that
   `weston/package.toml` pins, which `drivers/generated.go` copies at
   build time and a test holds equal. So the guider costs no new image
   on a node that runs `display-operator`, and one bump moves both.
   The two containers share the Wayland socket in an `emptyDir`.
3. **The image** is `indi/images/phd2`, built on `indi` at `indi`'s tag.
4. **The profile.** The operator writes PHD2's whole `~/.PHDGuidingV2`
   into the `ConfigMap` `<guider>-guider`: `ConfigVersion=2001`, one
   profile, the INDI host and port 7624, the camera as
   `INDI Camera [<device>]`, the mount as `INDI Mount [<device>]` for
   `spec.pulses: Mount` or `On-camera` for `Camera`, and the guide
   tube's focal length in millimeters. The device names are the ones
   the telescope's server defines. A comment at `guiderProfile` in
   `guiderpod.go` gives the source file of each key. A change to the
   profile replaces the pod, because the pod's digest covers it.
5. **The pod** is `<guider>-guider`, with a `Service` on port 4400, an
   owner reference to the `Guider`, and the same required pod affinity
   to the server's pod as the guide camera. It has no device claim, a
   grace period of 1 second, and a read-only root. `$HOME`, `/tmp`, the
   Wayland directory, and weston's `/etc/weston` are `emptyDir`s.
6. **The lifecycle.** `StartGuider` runs after `Prepare`, with a
   deadline of 10 minutes. `Abort` sends `stop_capture` first.
   `StopGuider` runs after `Secure` and before `Disconnect`, with a
   deadline of 2 minutes. "The step order" below gives the whole
   order.
7. **The event client** is the package `phd2`, one connection for each
   running guider, in the operator.
8. **The status** holds PHD2's state, calibration, pixel scale, RMS,
   guide star, last step, last alert, and endpoint. The printer
   columns are the telescope, the phase, PHD2's state, the total RMS,
   and the age.

### The step order

Activation: `Wait`, `StartSite`, `PowerOn`, `StartDevices`, `Connect`,
`Configure`, `Prepare`, `StartGuider`.

Deactivation: `Abort`, `Secure`, `StopGuider`, `Disconnect`,
`StopDevices`, `PowerOff`, `StopSite`.

`StartGuider` is `Skipped` for a telescope with no `Guider`. It waits
until the server defines the guide camera and the mount, writes the
`ConfigMap`, creates the pod and the `Service`, waits until the pod is
Ready and the operator's connection to PHD2's event server is open,
sends `set_connected true` unless PHD2 reports its equipment connected
already, and waits until `get_connected` answers `true`. Each part reads
what exists first, so the step runs again after an operator restart
with no second `set_connected`. A refusal from PHD2 fails the step with
PHD2's message.

`Abort` sends `stop_capture` when PHD2 loops, calibrates, guides, or is
paused, and waits until PHD2 reports that it stopped. Then it aborts
the exposures and the slew as before, so no guide pulse follows the
mount's stop. `StopGuider` deletes the pod, the `Service`, and the
`ConfigMap` while the camera and the mount are still connected, so
PHD2 sees no device disconnect. The audit below found that PHD2 reports
a lost INDI device in an alert, not a modal dialog; the order costs
nothing and keeps PHD2 out of its reconnect path, which can open the
connect progress dialog.

While the reservation is `Ready`, the runner creates the guider's pod
again when it is gone, and sends `set_connected` to each new PHD2 once,
when PHD2 reports its equipment disconnected and takes no exposures. A
PHD2 whose holder disconnects its equipment on purpose stays
disconnected until its pod changes. A failure there sets the
`Guider`'s phase to `Error` and does not end the reservation.

### The event client

`phd2/` speaks PHD2's event protocol: JSON-RPC 2.0 over TCP, one line
of JSON for each message, ending in CR LF. PHD2 sends its events on the
same connection as the answers to requests, in the order it makes
them. The client follows the repository's event rule. `Run` opens the
connection, which is the subscription, and then sends `get_app_state`,
`get_calibrated`, `get_connected`, and `get_pixel_scale` as the
baseline. Their answers arrive after any event that PHD2 made before it
read them. When the connection ends, the state empties, and the next
`Run` reads a new baseline. The operator opens the connection when the
pod is Ready, and opens it again after a pause that doubles from 1 to
30 seconds, the same clock as the INDI connections.

PHD2 sends `AppState` only to a new connection, among its catch-up
events (`send_catchup_events` in `src/event_server.cpp`). So the client
sets the state from the events that imply one: `StarSelected`,
`LoopingExposures`, `StartCalibration`, `Calibrating`, `StartGuiding`,
`GuideStep`, `StarLost` while guiding, and `Paused`. PHD2 loops in two
states, `Looping` with no star selected and `Selected` with one
(`Guider::GetExposedState`), so `LoopingExposures` keeps `Selected`. A
stop implies no state, because PHD2 can stop guiding and go on looping,
so `GuidingStopped`, `LoopingExposuresStopped`, `LockPositionLost`, and
a `StarLost` outside guiding read `get_app_state` again. PHD2 sends no event when its equipment connects or disconnects,
so an `Alert` or a `ConfigurationChange` reads the calibration, the
equipment, and the pixel scale again. Each of these reads is one
request for one event.

`GuideStep` carries the star's distance from the lock position in
pixels, `RADistanceRaw` and `DECDistanceRaw`, and no RMS. The client
computes the RMS over the last 100 steps since `StartGuiding`, and the
operator multiplies it by the pixel scale for the status. PHD2 answers
`get_pixel_scale` with `null` while it does not know the camera's
pixel size or the focal length, and the status then has no RMS.

Correction, 2026-10-06: the window starts at `StartGuiding` only when
the client's connection was open then. PHD2 sends a new connection no
earlier guide steps, so after an operator restart the window starts at
the first step that the new connection receives ("A guider pod deleted
while `Ready`, and an operator restart" measured this). The status now
gives `rms.since`, the time of the first step in the window.

The client sends only `set_connected`, `stop_capture`, and the four
reads. `phd2/phd2test` is the fake event server that both the
package's tests and the operator's tests use. It answers over
`net.Pipe`, so the tests run in a `synctest` bubble.

### The modal dialogs PHD2 can open

On 2026-10-06, PHD2's source at commit `a6c0272` was read for every
`ShowModal`, `wxMessageBox`, and `wxMessageDialog`, and every wrapper of
one, that PHD2 can reach while it connects through `set_connected`,
loops, calibrates, or guides, or when its INDI camera or mount
disconnects or times out. The seat work in display-operator protects
against these. Line numbers are of that commit.

Looping, calibrating, guiding, a lost star, `stop_capture`, and an INDI
device that disconnects open no modal dialog. Each reports through
`MyFrame::Alert`, which shows a `wxInfoBar` in the main window
(`myframe.cpp:1281`) and sends the `Alert` event (`myframe.cpp:1284`).
`Mount` and `GuideCamera` inherit `wxMessageBoxProxy` (`mount.h:166`,
`camera.h:127`), and no code in `cam_indi.cpp`, `scope_indi.cpp`,
`camera.cpp`, `mount.cpp`, `scope.cpp`, `guider*.cpp`,
`worker_thread.cpp`, `phdcontrol.cpp`, or `event_server.cpp` calls it
on those paths. The calibration sanity dialog is modeless
(`scope.cpp:885`), and only the alert's "Details" button opens it.

| Dialog | Source | Trigger | Reachable through the event API | Prevented by |
|---|---|---|---|---|
| INDI device setup | `cam_indi.cpp:801`, `scope_indi.cpp:249` | `Connect()` of a camera or mount whose device name is the default, "INDI Camera" or "INDI Mount" (`cam_indi.cpp:608`, `scope_indi.cpp:268`) | `set_connected`, with no `Alert` first | The profile names `/indi/INDIcam` and `/indi/INDImount` |
| Camera Change Warning | `gear_dialog.cpp:1145` | A dark library or a defect map exists (`gear_dialog.cpp:1139`), and the camera choice changed or the driver's pixel size differs by 1% or more from `/camera/pixelsize` (`gear_dialog.cpp:1112-1132`) | `set_connected`, with no `Alert` first | A new `$HOME` holds no dark library, and the profile leaves `/camera/pixelsize` out |
| Connect progress | `runinbg.cpp:42`, created at `runinbg.cpp:141` | A `wxProgressDialog` with `wxPD_APP_MODAL`, shown when a connect takes more than 2.5 seconds (`runinbg.cpp:66`). Both INDI connects use it and wait up to 30 seconds (`cam_indi.cpp:627`, `scope_indi.cpp:287`). After an exposure timeout, PHD2 reconnects the camera through it (`cam_indi.cpp:1067`, `camera.cpp:1543`, `myframe.cpp:1397`). | `set_connected`, and the reconnect after an exposure timeout, which follows an `Alert` | Nothing in the profile. The camera and the mount are connected on the server before `StartGuider`, which keeps the connect short. |
| First-light wizard | `phd.cpp:628`, `profile_wizard.cpp:1642` | A config with no `ConfigVersion` (`phdconfig.cpp:249`), or one profile whose camera and mount choices are both "None" (`gear_dialog.cpp:1896`) | At start, with no API call | `ConfigVersion=2001` and the camera and mount choices |
| Instance lock | `phd.cpp:509-511` | A `~/phd2.1` lock from a PHD2 that crashed. PHD2 reports it with `wxLogError`, which the default GUI log target shows in a modal message box. | At start | The image removes the lock before each start |

Not checked: whether the generic GTK `wxProgressDialog` crashes with no
seat. It disables the other windows and calls `Show()`, not
`ShowModal()`, from a reading of wxWidgets that was not verified in its
source. A `wxLogError` would show a modal message box through the
default GUI log target, because PHD2 sends wxWidgets' log to standard
error only on macOS (`phd.cpp:484`). The instance lock is the one such
path found; no other was traced. The
exit confirmation (`myframe.cpp:2159`) needs a close event, and PHD2
installs no handler for `SIGTERM`, so a pod's deletion does not reach
it. Every other modal dialog in PHD2 opens only from a menu or a button
that no event-API method reaches.

### The example's guide camera

Root plan 74's experiment guided with the CCD simulator, whose pixels
are 5.2 µm and whose seeing is 3.5 arc-seconds: at a 240 mm focal
length that is 4.5 arc-seconds per pixel, and every star was smaller
than a pixel. The example's guide camera runs `indi_simulator_guide`,
the guide camera simulator. Its defaults in INDI 2.2.5 are 2.4 µm
pixels and 6 arc-seconds of seeing (`drivers/ccd/guide_simulator.cpp`).
Behind the example's 50 mm guide scope of 200 mm, that is 2.5
arc-seconds per pixel and a star about 2.4 pixels across, a common
guide setup. So the example keeps its guide scope. The drill confirms
that PHD2 finds and keeps a star.

## How we test it

The tests run on the Go toolchain alone. `phd2/client_test.go` holds
the client to PHD2's protocol against `phd2test`, and the operator's
`guider_test.go` runs the steps, the status, a refused connect, a
guider pod that is deleted while `Ready`, and an operator restart
against the fake API server, the INDI transcripts, and one fake PHD2
for each guider pod. `phd2/testdata/` holds one session of a real
PHD2 2.6.14 on its event API: connect, the baseline, `set_connected`,
a loop, a selected star, calibration, 33 guide steps, and
`stop_capture`. `replay_test.go` serves it to the client, and the
client's state matches what PHD2 answered at each point. The fake in
`phd2test` follows it too: `ConfigurationChange` before the answer to
`set_connected`, no pixel scale before the camera connects, and
`GuidingStopped` before `LoopingExposuresStopped`. The 33 recorded
steps give an RMS of 0.50 pixels, 0.62 arcseconds, at 1.24 arcseconds
per pixel.

The drill on the test cluster: a `Reservation` activates the east
telescope with the `Guider` `Ready`. Then, acting as the holder, turn
tracking on, and through the event API, port-forwarded to the
`Service` `east-guider`, loop, select a star, calibrate, and guide for
a few minutes, while the `Guider`'s status shows the state and the RMS
change. Deactivation then stops PHD2 and deletes its pod before
`Disconnect`, and ends `Released`. Root plan 74's experiment calibrated
in 19 steps and then guided at about one step a second with 1-second
exposures.

## What the test cluster measured

The operator ran from its development build
`2026.10.04-004-dev-048-82139946`, applied by Flux from the deploy
artifact, on the two-node test cluster of plan 03. The inventory was
`examples/simulators.yaml` without its `Reservation`. The drill created
a `Reservation` for the east telescope by hand, read
`kubectl get reservation -w` and `kubectl get guider -w`, and took the
step times from `status.steps` and the operator's log, to the second.
The `indi` images had a new revision for PHD2, so each node pulled the
`indi` and `indi-simulators` images again, and the guider's node pulled
`weston` and `indi-phd2` for the first time.

| Step | Time | What it did |
|---|---|---|
| `Wait` | under 1 s | Took the telescope |
| `StartSite` | 38 s | 29 s of it pulled `indi-simulators` |
| `PowerOn` | 4 s | |
| `StartDevices` | 61 s | 57 s of it pulled `indi-simulators` on the other node |
| `Connect` | 1 s | |
| `Configure` | under 1 s | |
| `Prepare` | 19 s | Cooled the main camera to -10 °C |
| `StartGuider` | 47 s | 11 s pulled the 92 MB `weston` image and 23 s pulled the 211 MB `indi-phd2` image. The pod was Ready 10 s after PHD2 started, and PHD2 reported its camera and mount connected 1 s later. |
| `Abort` | under 1 s | "Stopped PHD2, which was Guiding" |
| `Secure` | 64 s | Parked the mount in 31 s and warmed the main camera to 5 °C |
| `StopGuider` | 2 s | Deleted the pod while the camera and the mount were connected |
| `Disconnect` | 1 s | |
| `StopDevices` | 1 s | |
| `PowerOff` | 3 s | |
| `StopSite` | 19 s | Parked the dome in 17 s |

The `Reservation` reached `Released` 91 s after its delete, and the
operator then removed it. The operator sets `SafeToPowerOff` to `True`
with the phase `Released`. The watch did not record the condition,
because the object was gone 1 s later.

The guider's pod, the guide camera's pod, and the server's pod ran on
one node. `kubectl get guider` showed `Ready` with the state `Stopped`
and a pixel scale of 2.475 arc-seconds per pixel, which matches 2.4 µm
pixels behind the 200 mm guide scope.

After `Prepare`, the mount was unparked, not tracking, at declination
-90°. Acting as the holder, the drill sent the mount through INDI to
declination 0° with tracking on, so the calibration measures both axes
at a declination where RA moves the star. Then, over the event API
through a port-forward to `east-guider`, it sent `set_exposure` of
1,000 ms, `loop`, `find_star`, and `guide` with a settle of 1.5 pixels
for 8 seconds, a timeout of 60 seconds, and `recalibrate`.

The times are from the `loop` request to the watch's report of the
`Guider`'s status.

| Time from `loop` | `Guider` state | Total RMS in the status |
|---|---|---|
| 2 s | `Looping` | none |
| 11 s | `Selected` | none |
| 16 s | `Calibrating` | none |
| 89 s | `Guiding` | none |
| 90 s | `Guiding` | 4.28 arc-seconds after the first step |
| 101 s | `Guiding` | 1.42 arc-seconds over 10 steps |
| 146 s | `Guiding` | 0.84 arc-seconds over 50 steps |
| 207 s | `Guiding` | 0.54 arc-seconds over 100 steps |
| 223 s to 298 s | `Guiding` | 0.50 to 0.52 arc-seconds over 100 steps |

PHD2 found a star on the first `find_star`, with an SNR of 77 and an
HFD of 2.2 to 2.4 pixels, so the example's guide scope works with the
guide camera simulator. Calibration took 73 s and 35 steps: 12 west,
4 east, 3 for backlash, 10 north, 4 south, and 2 south nudges. It
measured 2.94 pixels per second in RA at -179.3° and 3.06 pixels per
second in Dec at 88.4°. PHD2 settled in 10 frames with none dropped,
and guided for 3 min 56 s, until `Abort`. In the 198 s that the
drill's connection stayed open, PHD2 sent 179 guide steps, one in
1.1 s, and no `StarLost` or `Alert`. Over the last 100 steps, the RMS that the
drill computed from the `GuideStep` events was 0.42 arc-seconds in RA,
0.28 in Dec, and 0.50 in total. The status showed 0.51 arc-seconds
10 s later. The status
changed once a second, with `star.snr`, `star.hfd`, and
`lastStepTime`.

The `compositor` and `phd2` containers each had a restart count of 0
for the whole reservation, and no modal dialog opened. PHD2 logged 102 GDK assertions about the missing seat, such as
`gdk_seat_get_keyboard: assertion 'GDK_IS_SEAT (seat)' failed`: 28
when its window opened, 66 when it connected its equipment, and 2
each when it started to loop, to calibrate, and to guide. None of
them stopped PHD2. weston logged one warning, that
its runtime directory has the mode 0777 and the owner root, and one
that the read-only root holds no shader cache. Neither stopped it.
When `StopGuider` deleted the pod, the `phd2` container ended with
the status `Error`, because PHD2 has no handler for `SIGTERM`; the pod
was gone 2 s after the delete.

### A guider pod deleted while `Ready`, and an operator restart

The second drill used the same build, inventory, and method, with one
more source of evidence. The `indi-phd2` image has no shell, so an
ephemeral container from `kubectl debug --target=phd2 --profile=general`
read PHD2's debug log in `~/PHD2`. PHD2 writes each connection to its
event server and each request into that log, so the log shows every
request the operator sent. The images were on the node already.

A `Reservation` for the east telescope was `Ready` in 38 s, with
`StartGuider` done in 5 s. The drill slewed the mount to declination 0°
with tracking on, and sent the same requests over the event API as the
first drill. PHD2 calibrated and guided.

The drill then deleted the pod `east-guider` while PHD2 guided. The
times are from the delete.

| Time | What happened |
|---|---|
| 0.5 s | The `Guider` phase changed to `Activating`, and `Ready` to `False`, with the message "Waiting for pod east-guider (Running)" |
| 1.1 s | The operator's connection to PHD2 ended. The `Guider`'s state and RMS became empty. |
| 2.2 s | The operator created a new pod `east-guider` |
| 5.9 s | The new pod was Ready |
| 6.9 s | The operator connected to the new PHD2 and read the baseline: `Stopped`, equipment not connected |
| 7.5 s | The operator sent `set_connected true`, once |
| 8.6 s | The `Guider` was `Ready` with the state `Stopped`, and the message "PHD2 is connected to its camera and mount" |

For 1 s from 2.2 s, the message was "Waiting for StartGuider to
create pod east-guider". The runner, not `StartGuider`, creates the pod
again while the reservation is `Ready`, so that message names the wrong
step. The `Telescope` stayed `Ready`, and its guider column read
`Activating`, then `Ready, Stopped`. The `Reservation` stayed `Ready`,
and its `status.steps` did not change. No Event explains the gap: the
operator writes Events only for a reservation's phases and steps, and
its log has one line, "the connection to east-guider ended". The only
Events are the kubelet's, for the pod's stop and start, and one failed
readiness probe while PHD2 started. The new PHD2 was idle and not
calibrated, as expected, because calibration and guiding belong to the
holder. In PHD2's debug log, the operator sent only the four baseline
reads, one `set_connected`, and the reads that follow a
`ConfigurationChange`.

Correction, 2026-10-06, after the drill: while a `Ready`
reservation's runner creates a pod again, the `Ready` message of the
`Guider` or the device now reads "Creating pod <name>". The runner
records a `PodCreated` Event on the `Guider` or the device when it
creates the pod, and writes one line to its log.

The drill then calibrated and guided again. PHD2 calibrated in 72 s
and settled in 10 s. After 70 s of guiding, the drill ran
`kubectl rollout restart` on the `Deployment` `observatory-operator`.
Flux applied the `Deployment` again 90 s later, which removed the
restart annotation, so the operator restarted a second time.

| | First restart | Second restart |
|---|---|---|
| The old operator's connection ended | 0 s | 0 s |
| The new operator's pod was Running | 4.7 s | 1.4 s |
| The new operator connected and read the baseline | 5.5 s | 1.8 s |
| The `Guider`'s status changed again | 5.8 s | 2.7 s |
| Time between two writes of the `Guider`'s status | 6.5 s | 3.8 s |

The drill's own connection to the event API stayed open through both
restarts. From `StartGuiding` to `Abort`, it received 203 `GuideStep`
events, frames 1 to 203 with no frame missing, and at most 1.56 s
between two steps. Before `Abort`, PHD2 sent no `Alert`, `StarLost`,
`GuidingStopped`, or `Paused`. In PHD2's debug
log, each new operator sent `get_app_state`, `get_calibrated`,
`get_connected`, and `get_pixel_scale`, and nothing else: no
`set_connected` and no `stop_capture`. The baseline was `Guiding`,
calibrated, connected, and 2.475 arc-seconds per pixel. The
`Reservation`'s `status.steps` were byte-identical before and after
the restarts. The `Reservation` stayed `Ready`, and the `Telescope`
did not change. After each restart, the status changed once a second
again.

Each new operator computes the RMS only from the guide steps it
receives. Before the first restart, the status showed 0.71
arc-seconds over 61 steps. After it, the status showed 0.16
arc-seconds over 1 step. `rms.steps` reports the count, and the RMS
over 75 steps was 0.50 arc-seconds before the second restart. PHD2's
event API sends no past guide steps to a new connection, so a new
client cannot read them.

The drill then deleted the `Reservation`. `Abort` "Stopped PHD2, which
was Guiding" in 1 s, `Secure` took 64 s, and `StopGuider` deleted the
pod in 2 s. The `Reservation` reached `Released` 92 s after its
delete, and the operator then removed it.

PHD2's debug log also shows the pod's readiness probe, a TCP connect
to port 4400 every 10 s. PHD2 writes its catch-up events to each probe
connection after the probe closes it, and logs each failed write as a
"short write". The writes fail with no other effect.

## Upstream issues

- [phd2#683](https://github.com/OpenPHDGuiding/phd2/issues/683), open:
  PHD2 has no true headless mode, and its interface and its API are
  not separate.
- [phd2#485](https://github.com/OpenPHDGuiding/phd2/issues/485): the
  instance lock is a `wxSingleInstanceChecker` file in `$HOME` that
  stays after a crash.
- [phd2#1090](https://github.com/OpenPHDGuiding/phd2/issues/1090), open:
  the camera and the mount must be on one INDI server. The single
  server meets that.
- [phd2#1247](https://github.com/OpenPHDGuiding/phd2/issues/1247), open:
  the INDI camera backend sends an empty `setBLOBVector` while busy.
- [phd2#1438](https://github.com/OpenPHDGuiding/phd2/pull/1438), open:
  multi-star data for the event server. The event API is documented
  only on the wiki, at
  <https://github.com/OpenPHDGuiding/phd2/wiki/EventMonitoring>.

## References

- The PHD2 PPA: <https://launchpad.net/~pch/+archive/ubuntu/phd2>
- The event server: `src/event_server.cpp` in `OpenPHDGuiding/phd2`
- The command line: `src/phd.cpp`, and the profile: `src/phdconfig.cpp`
- The INDI backends: `src/cam_indi.cpp` and `src/scope_indi.cpp`
- [Root plan 74](../../../plans/74-astrophotography.md), "Guiding"
