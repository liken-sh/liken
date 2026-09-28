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
- [`display-operator/`](display-operator/) runs the Weston compositor
  and publishes each monitor output as a device under
  `display.liken.sh`. Its Dockerfile also builds the base images
  `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`.
- [`equipment-operator/`](equipment-operator/) drives the A/V equipment
  at the far end of a machine's cable: receivers over the network, and
  televisions over CEC.
- [`media-operator/`](media-operator/) declares players, plays,
  remotes, and keymaps, and reconciles them into pods that claim the
  hardware operators' devices.
- [`library-operator/`](library-operator/) declares media libraries,
  keeps a catalog of what they hold, and puts a media browser on the
  screens.
- [`people-operator/`](people-operator/) defines the `Person` resource.
- [`git-csi-driver/`](git-csi-driver/) mounts git repositories as
  volumes.
- [`per-node-csi-driver/`](per-node-csi-driver/) gives a pod a
  directory that stays on the node it runs on.
- [`brand/`](brand/) holds the mark, the stylesheet, the Hugo theme,
  the voice rules, and the tools that build the sites.

Each component has its own `README.md`, `AGENTS.md`, and `Makefile`.
[`plans/`](plans/) holds the plans that cover more than one component,
and each component keeps its own plans in its own `plans/`. Plan 69
describes how the components came into this repository and how one
tag will release them. [`AGENTS.md`](AGENTS.md) holds the rules for
agents that work in the repository, and `.agents/skills` holds their
skills.

The manual is at [liken.sh](https://liken.sh/). To read every
component's manual together on a workstation, run `make preview` and
open <http://localhost:8080/>. The root `Makefile` explains the
layout.
