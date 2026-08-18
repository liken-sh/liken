---
title: display.liken.sh
---

# `display.liken.sh`

`display-operator` is a Kubernetes DRA driver that publishes each
monitor output of a graphics card as a device on a
[`liken`](https://liken.sh/) cluster. It runs the Weston compositor in
the same pod. A pod that claims an output receives the Wayland socket
and the app-id that puts its window on that screen.

The driver's manual will publish here.

* [The repository](https://github.com/liken-sh/display-operator)
* [The `liken` manual](https://liken.sh/docs/)
