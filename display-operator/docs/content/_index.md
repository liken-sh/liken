---
title: display.liken.sh
---

# `display.liken.sh`

`display-operator` gives a Kubernetes workload a screen. It publishes
each monitor output of a graphics card as a device on a
[`liken`](https://liken.sh/docs/) cluster, through
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/),
the Kubernetes API for devices. The operator runs the
[Weston](https://wayland.pages.freedesktop.org/weston/) compositor in
its pod. A pod that claims an output receives the Wayland socket and
the app-id that puts its window, fullscreen, on that screen.

A claim and a `Deployment` are the whole task. The claim names the
screen: a connector such as `HDMI-A-1`, a monitor by its model or its
serial, or any screen at least 1920 pixels wide. The `Deployment`
references the claim, and its container receives the socket and the
app-id. No step touches the machine itself: no SSH, no configuration
on the host, no privileged pod.

Screens you can run this way:

* a kiosk, one fullscreen browser on the screen at the door,
* a dashboard on a wall monitor,
* a movie or a game's video on the TV.

Start here:

* [Install the operator](/docs/guides/install/). The install applies
  the manifests this site serves at
  [`/deploy/`](/deploy/kustomization.yaml), so it needs no clone.
* [Put a window on a screen](/docs/guides/claim/): the claim, the
  `Deployment`, and what the container receives.
* [Devices](/docs/reference/devices/): every attribute a claim can
  select on.
* [Displays](/docs/reference/displays/): the resource the operator
  creates for every monitor, with the panel's controls and the
  declarations it takes.

The operator is one of the
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/),
the optional layer above the operating system. A cluster that never
installs it runs unchanged. `liken` itself publishes the graphics
card; this operator claims that card and publishes its outputs, one
device for each connector. Its siblings publish
[Bluetooth controllers](https://bluetooth.liken.sh) and
[audio outputs](https://audio.liken.sh). A monitor's speakers pair
with its screen through `monitor.liken.sh/id`, the identity both
drivers read from the same monitor.

* [The repository](https://github.com/liken-sh/display-operator)
* [The `liken` manual](https://liken.sh/docs/)
