# liken

`liken` is an immutable operating system that boots a machine
directly into Kubernetes, and the operators that give a cluster of
those machines its hardware. This repository holds every component.
Each top-level directory is one component, named for what it ships:

- [`liken/`](liken/) is the OS: the init, the image, the kernel, k3s,
  and the operators that manage the machines and the cluster.
- [`audio-operator/`](audio-operator/) publishes each physical audio
  output as a device under the class `audio.liken.sh`.
- [`bluetooth-operator/`](bluetooth-operator/) publishes each paired
  Bluetooth controller as a device under the driver name
  `bluetooth.liken.sh`.

Each component has its own `README.md`, `AGENTS.md`, and `Makefile`.
[`plans/`](plans/) holds the plans for every component. Plan 69
describes how the other components join this repository and how one
tag releases them.

The manual is at [liken.sh](https://liken.sh/).
