# 74, Astrophotography on a `liken` cluster

Not built. This plan is a stub. It records a design conversation about
running an astrophotography rig from a `liken` cluster, and it makes no
final decision. The facts about upstream projects come from their
documentation as of September 2026, and a fact that was not checked is
marked as such.

## The idea

A telescope rig is a set of USB and serial devices on one machine next
to the mount: the mount, a main camera, a filter wheel, a focuser, and a
guide camera. Today a small computer runs the rig, usually a Raspberry
Pi with a distribution such as StellarMate or AstroArch, and a person
drives it from a desktop application.

On a `liken` cluster, a small x86-64 machine at the mount joins the
cluster as one more node, because `liken` does not run on ARM. The node
claims the rig's devices through DRA. An operator plans the night from
declared targets, drives the rig through INDI, and grades each frame as
it arrives. The rest of the cluster stacks the frames, and a screen in
the house shows the session.

The rig in this plan does not travel. A rig at a remote dark site has
no control plane, and a one-node cluster adds little over the
distributions above.

## What stays upstream

The operator speaks to existing programs and replaces none of them.

- **INDI** is the device layer. `indiserver` starts one process for
  each driver and serves every device over one XML protocol on TCP port
  7624. The core drivers ship with the library, and vendors maintain
  their drivers in `indi-3rdparty`. Release 2.2.3 shipped on
  2026-08-01 ([releases](https://github.com/indilib/indi/releases)).
  Some camera drivers link closed vendor SDKs, for example ZWO's
  `libASICamera2`.
- **PHD2** guides the mount. It has a JSON-RPC event API on TCP port
  4400, so the operator drives it the way media-operator drives mpv.
  This was not checked for this plan.
- **A plate solver**, ASTAP or astrometry.net, turns a frame into
  coordinates. A solve is a function of one frame, so it runs as a
  process or a `Job`.

## What the operator owns

The operator is an INDI client in Go that speaks the wire protocol
directly, the way `equipment-operator/cec/` speaks the kernel's CEC API
with no libCEC and no cgo. The Go clients that exist,
[`goastro/indiclient`](https://pkg.go.dev/github.com/goastro/indiclient)
and its fork `jnmorley/indiclient/v2`, target indiserver 1.7.

The INDI protocol already follows the repository's rule for events.
The client sends `getProperties`. The server answers with a
`def*Vector` for each property, which is the baseline, and then sends a
`set*Vector` for each change on the same connection.

The operator owns the sequence: slew, center with a plate solve, change
filters, expose, dither, flip at the meridian, and focus. Autofocus is
the least certain part. It fits a V-curve to the half-flux radius of
the stars, which is simple to state and slow to tune on real skies.

The operator does not drive the rig through Ekos. The Ekos Scheduler is
scriptable over DBus
([docs](https://kstars-docs.kde.org/en/user_manual/ekos-scheduler.html)),
and it would bring years of focus, solve, and flip logic. It would also
put KStars, a Qt desktop application, inside a pod, and make its DBus
interface the operator's real API.

## Devices

Most of a typical rig works with the DRA claims `liken` has today. A
mount or focuser behind an FTDI, PL2303, or CH340 chip publishes a tty.
A ZWO filter wheel is a HID device, so it publishes a device with its
usbfs node.

A ZWO ASI camera has no kernel driver, so `liken` does not publish it.
[Driverless USB devices are not published](../liken/plans/open-problems/driverless-usb-devices-are-not-published.md)
records that problem. The operator also depends on hot-plug reaching the
`ResourceSlice` promptly, which
[the device inventory waits for the ticker](../liken/plans/open-problems/device-inventory-waits-for-the-ticker.md)
records.

## Resources

A first sketch, with names that are not settled:

```yaml
kind: Telescope
spec:
  indi: {service: backyard-indi}
  devices:
    mount: EQMod Mount
    camera: ZWO CCD ASI2600MM Pro
    filterWheel: ZWO EFW
    focuser: Pegasus FocusCube
    guider: phd2
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

## Frames and stacking

A 26-megapixel 16-bit frame is about 52 MB of FITS. By default an INDI
camera driver sends each frame to the client as base64 in the XML
stream, on the same connection as every property update. INDI also has
an upload mode in which the driver writes the file to a local directory
and sends only the path. The property names were not checked for this
plan.

The operator uses the local mode. The camera's driver writes frames to
a volume on the rig's node. The operator grades each frame there at
once, because the planner needs the accepted count before its next
choice. Stacking runs later, anywhere: a Siril `Job` on a machine with
more cores reads the accepted frames from the `Catalog`. A stack of the
frames captured so far can run during the night, to show progress.

## The screen

A screen in the house shows the session: the current target, the
progress of each plan, the last frame, and the latest stack. It runs as
a delegate of a `Player`'s idle screen through
`spec.idle.controller`, which is how the media browser takes a
`Player`'s screen today. It reads the display claim, the draw request,
and the bus topics from `status.idle`.

The screen never talks to INDI. A person's action edits a resource:
skipping a target sets a field on the `Target`, and pausing sets one on
the `Telescope`. The operator stays the only writer to the rig, and
every action is on record in the API.

A screen next to the rig must keep night vision, with red colors and a
low backlight. display-operator can already set a panel's brightness.

## Adjacent work this plan does not cover

The screen shows that some pieces of the media stack serve more than
media. Each needs its own plan, and this plan does not depend on them.

- **The bus.** `media-operator/deploy/bus.yaml` installs the Mosquitto
  broker at `bus.liken-system.svc:1883`. media-operator, the pods it
  starts, and library-operator's screens connect to it. Root plan 77
  took equipment-operator off it, so the bus carries only the media
  domain today, and an astronomy operator would be its first client
  outside that domain. The broker could become its own component.
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

## Open questions

- Where frames land: shared storage, or a per-node volume that the
  pipeline copies from.
- How calibration frames work: a library of darks by temperature, gain,
  and exposure, and flats that need the rig.
- Where safety comes from: weather, a cloud sensor, and later a roof.
  KStars 3.8.0 added support for the INDI Safety Monitor driver
  ([release](https://knro.blogspot.com/2025/12/kstars-v380-is-released.html)).
- What the component is called, and whether the screen is part of it.
- How an observatory with more than one telescope shares one sky,
  one roof, and one safety state.
