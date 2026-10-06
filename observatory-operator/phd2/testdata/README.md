# PHD2 event API transcripts

These files hold one session with a real PHD2 on its event API, TCP
port 4400. The tests of the `phd2` package replay them through a fake
event server, so the fake sends what PHD2 sends.

Each line is one JSON line of the protocol, with a mark before it:

- `> ` marks a line that the client sent to PHD2.
- `< ` marks a line that PHD2 sent to the client: an event, or the
  reply to a request.

The session is one connection, split into a file for each phase. A
phase starts when the client sends its first request, so an event
that PHD2 sent late in one phase can be the first line of the next.

| File | The client | What PHD2 sent |
|---|---|---|
| `01-connect.txt` | opens the connection and sends nothing | `Version`, then `AppState` with `Stopped` |
| `02-baseline.txt` | sends `get_app_state`, `get_connected`, `get_calibrated`, `get_pixel_scale` | `Stopped`, `false`, `false`, and `null`, because PHD2 knows no pixel scale before the camera connects |
| `03-set-connected.txt` | sends `set_connected` with `true`, then `get_connected`, `get_pixel_scale`, `get_app_state` | `ConfigurationChange`, then `true`, 1.23759 arcseconds per pixel, and `Stopped` |
| `04-loop.txt` | sends `loop` | `LoopingExposures` events with `No star selected` |
| `05-find-star.txt` | sends `find_star`, then `get_app_state` | `LockPositionSet`, `StarSelected`, the star's position, `LoopingExposures` with the star's SNR and HFD, and `Selected` |
| `06-calibrate.txt` | sends `guide` with `recalibrate` and a settle of 1.5 pixels for 5 seconds, then `get_calibrated` | `StartCalibration`, 21 `Calibrating` events, `CalibrationComplete`, `StartGuiding`, `SettleBegin`, and `true` |
| `07-guide.txt` | waits about 30 seconds after the settle, then sends `get_app_state` | `Settling` events, `SettleDone`, 33 `GuideStep` events, and `Guiding` |
| `08-stop.txt` | sends `stop_capture`, then `get_app_state` | `GuidingStopped`, `LoopingExposuresStopped`, and `Stopped` |

PHD2 sends an `AppState` event only when a client connects. After that,
a client follows the state through the other events, or asks with
`get_app_state`.

## How the session was recorded

The session was recorded on 2026-10-06, by hand, on one workstation
with Docker, from three containers on one network:

- `ghcr.io/liken-sh/indi-simulators:20261005-3`, which ran
  `indiserver` with `indi_simulator_telescope` and
  `indi_simulator_guide`. INDI 2.2.5.
- `ghcr.io/liken-sh/weston:20260928-3`, headless, with the arguments
  and the `weston.ini` of `weston/smoke/weston.sh`. Weston 14.0.2.
- `ghcr.io/liken-sh/indi-phd2:20261005-4`, PHD2 2.6.14 from the
  package `2.6.14.rev20251216-0ppa1~ubuntu26.04`. The container's host
  name was `phd2`, which is the `Host` field of every event.

The guide simulator had a focal length of 400 mm and an aperture of
80 mm, set through `SCOPE_INFO`. Its pixels are 2.4 micrometers, so
the scale is 1.24 arcseconds per pixel. The simulator draws stars with
6 arcseconds of seeing, and the guide star had an HFD of about 4.8
pixels and an SNR of about 81. The mount simulator was unparked,
tracking, and pointed at RA 5.588 h, Dec -5.39 degrees.

PHD2 read this profile, with 400 as the focal length in millimeters:

```ini
ConfigVersion=2001
currentProfile=1
[profile]
[profile/1]
name=Simulators
[profile/1/indi]
INDIhost=indi
INDIport=7624
INDIcam=Guide Simulator
INDIcam_ccd=0
INDImount=Telescope Simulator
[profile/1/camera]
LastMenuChoice=INDI Camera [Guide Simulator]
[profile/1/scope]
LastMenuChoice=INDI Mount [Telescope Simulator]
[profile/1/frame]
focalLength=400
```

The exposures were PHD2's default of 1 second. A small Python client
sent the requests and wrote each line with its mark.
