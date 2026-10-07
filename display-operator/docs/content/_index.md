---
title: display-operator
---

# `display-operator`

`display-operator` gives a pod on a [`liken`](https://liken.sh/docs/)
cluster a screen to draw on, with no privilege and no host path. You
write a claim that names the screen, such as the connector
`HDMI-A-1`, a monitor by its model or serial, or any screen at least
1920 pixels wide. The pod that holds the claim gets a Wayland socket,
and its window is on that screen. You don't configure anything on the
machine.

Things you can run this way:

* a kiosk: one fullscreen browser on the screen by the door,
* a dashboard on a wall monitor, with each panel in its own pod,
* a film or a game on the TV, with a camera feed in the corner.

Start here:

* [Install the operator](/docs/guides/install/). The install applies
  the manifests that this site serves at
  [`/deploy/`](/deploy/kustomization.yaml), so you don't need a clone.
* [Put a window on a screen](/docs/guides/claim/): the claim, the
  `Deployment`, and what the container gets.
* [Put regions on a screen](/docs/guides/layout/): a `Layout` gives
  each pod its own rectangle of one screen.
* [Take a picture of a screen](/docs/guides/screenshot/): see what a screen
  shows, from your laptop.
* [Devices](/docs/reference/devices/): every attribute that a claim
  can select on.

## How it works

The operator runs the
[Weston](https://wayland.pages.freedesktop.org/weston/) compositor in
its own pod. The pod claims the machine's graphics card, which
`liken` publishes, and publishes each connector of the card as its
own device under the
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver `display.liken.sh`. When your pod's claim is prepared, the
compositor opens a Wayland socket for it, and a window on that socket
shows on that screen.

The operator also creates a [`Display`](/docs/reference/displays/)
for each monitor. It reports the panel's brightness, power, and other
controls, and you can set them there without a claim. A monitor's
speakers pair with its screen through `monitor.liken.sh/id`, which
this operator and [`audio-operator`](https://liken.sh/audio/) both
read from the same monitor.

`display-operator` is one of the extension operators for
[claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/).
You install it only if your cluster needs screens.

* [The source](https://github.com/liken-sh/liken/tree/main/display-operator)
* [The `liken` manual](https://liken.sh/docs/)
