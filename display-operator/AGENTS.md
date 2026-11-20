# Working on display-operator

`display-operator` is a Go Kubernetes operator and a DRA driver with
the driver name `display.liken.sh`. `README.md` says what it is for
and describes its kinds.

## How it runs

One `display-operator` pod runs on each node. It claims the graphics
card's primary node, its render node, and its i2c buses from `liken`.

The compositor in the `weston` container owns the card. It opens the
card first and becomes its DRM master, the one process that can set
modes and show frames on the card. Weston reads its modes from
`weston.ini` only when it starts. So the `declare` init container
writes the file from each `Display` before Weston starts. To apply a
new mode, the operator rewrites the file and restarts the compositor.
The containers share one process namespace, so the operator can send
the compositor a signal.

Weston loads the layout module, a Weston plugin that the `weston`
component builds. The module opens one Wayland socket for each
prepared claim. A claim is prepared when the kubelet has asked the
operator to set it up for a pod on the node. The window of the
consumer pod connects to that socket. The operator tells the
module where to place each window over a private control socket.

The `operator` container reads the card's modes and each monitor's
EDID, and sends each panel's settings over DDC/CI on the i2c buses.
It serves DRA to the kubelet, publishes each output, and writes the
status of each `Display` on its node.

A capture request goes to the `display-api` `Deployment`, which
forwards it to the `capture` container on the node of the `Display`.
`capture` reads frames from the layout module's capture socket,
encodes them on the render node, and streams them back.

`media-operator` claims outputs for its `Player`s, and it sets the
override of a `Display` for its idle screen. `equipment-operator`
reads the HDMI physical address of a `Display`.

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

## Finding things

- `docs/content/docs/reference/openapi.json` and `routes.md` are
  generated from the operator's router, with text from
  `docs/apiref/`.
- `smoke/display-capture.sh` checks that the `display-capture` image
  runs `/usr/bin/ffmpeg`, which is the path that the capture container
  calls.
- The EDID fixtures in `testdata/` come from real monitors. To read a
  monitor's EDID, run
  `od -An -tx1 /sys/class/drm/<card>-<connector>/edid`.
