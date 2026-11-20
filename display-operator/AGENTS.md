# Working on display-operator

`display-operator` is a Go Kubernetes operator and a DRA driver with
the driver name `display.liken.sh`. `README.md` says what it is for
and describes its kinds.

## How it runs

The `display-operator` `DaemonSet` runs one pod on each node, except a
node with the label `display.liken.sh/display: none`. The pod claims
the graphics card's primary node, its render node, and its i2c buses
from `liken`. Its containers share one process namespace, so the
operator can send signals to the compositor.

- `declare` is an init container. It writes `weston.ini` with the
  mode that each `Display` sets.
- `weston` runs the compositor. The compositor opens the card first
  and becomes its DRM master, the one process that can set modes and
  show frames on the card. It loads the layout module. The module
  opens one Wayland socket for each prepared claim, and it places each
  window where the operator tells it over a private control socket.
- `operator` serves DRA to the kubelet and publishes each output. It
  reads the card's modes and each monitor's EDID, sends each panel's
  settings over DDC/CI, and writes the status of each `Display` on its
  node. To apply a mode, it rewrites `weston.ini` and restarts the
  compositor, because Weston reads the file only when it starts.
- `capture` reads frames from the layout module's capture socket and
  encodes them on the render node.

The `display-api` `Deployment` serves the capture API for the whole
cluster. It forwards each request to the `capture` container on the
node of the `Display`. `media-operator` claims outputs for its
`Player`s, and it sets the override of a `Display` for its idle
screen. `equipment-operator` reads the HDMI physical address of a
`Display`.

## Design

The operator publishes each connector, and it leaves each choice to a
claim or a `Display`. A connector whose monitor is gone stays
published with a taint. If the operator removed the device instead,
the kubelet would retry each claim on it with no limit.

The compositor is DRM master. The operator opens the card only while
it has a connection to Weston, and it gives up master again at once,
because the compositor fails every commit while it doesn't have
master. A claim is identified by the Wayland socket that its window
connects to, not by the app-id, because the client chooses its own
app-id. The control socket is in a volume that only this pod mounts,
because a client that can reach it can place any window.

The operator decides the arrangement of each screen. The layout module
in the `weston` component stores no layout of its own, so the
operator sends the whole state again on each new connection. The
operator rejects a module with a different protocol version, and any
change to the module needs a new revision of `weston`. Each `Display`
is named by the monitor's identity. `audio-operator` builds the same
identity from the monitor's ELD, so that a claim can pair a screen
with its speakers.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.

## Finding things

- `docs/content/docs/reference/displays.md`, `layouts.md`,
  `openapi.json`, and `routes.md` are generated from `deploy/`, the
  text in `docs/crdref/` and `docs/apiref/`, and the operator's
  router. `make -C docs generate` regenerates them, and
  `make -C docs skills` regenerates `skills/` from the guides.
  `make test` fails when either is out of date.
- `smoke/display-capture.sh` checks that the `display-capture` image
  runs `/usr/bin/ffmpeg`, which is the path that the capture container
  calls.
- The EDID fixtures in `testdata/` come from real monitors. To read a
  monitor's EDID, run
  `od -An -tx1 /sys/class/drm/<card>-<connector>/edid`.
