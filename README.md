# liken

`liken` is an operating system that boots a machine directly into
Kubernetes, and a set of operators that make the hardware of those
machines into Kubernetes resources. You manage a machine, a monitor, a
speaker, a telescope, and a workload the same way: with a manifest and
`kubectl`.

The OS is an immutable image with the Linux kernel, k3s, and one
`init` process, and it has no shell and no package manager. It joins
each machine to a cluster, upgrades the fleet one machine at a time,
and publishes each machine's hardware as devices that a pod can
claim. All other software runs in a container. Each operator is an
optional workload that you install like any other manifest. It claims
a device that the OS publishes and gives pods a narrower one, such as
one monitor output or one speaker. A cluster runs without any of the
operators.

This repository holds every component, and one tag releases each
component that changed. Each top-level directory is one component,
named for what it ships. Its `README.md` says what it is for and what
its parts are. A component that has a manual publishes it under
[liken.sh](https://liken.sh/), and its `README.md` links to it.

## The OS

- [`liken/`](liken/) is the OS: the init, the image, the kernel, k3s,
  and the operators that manage the machines and the cluster.

## Hardware

- [`display-operator/`](display-operator/) gives a pod one monitor
  output of a graphics card, and runs the Weston compositor that draws
  on it.
- [`audio-operator/`](audio-operator/) gives a pod a sound output or
  input, runs PipeWire, and sets the volume of each output.
- [`bluetooth-operator/`](bluetooth-operator/) pairs a Bluetooth
  controller through the Kubernetes API and gives it to one pod.
- [`firmware-operator/`](firmware-operator/) loads firmware into the
  devices that keep none, and installs the updates that you approve
  for the firmware that a device keeps.

## Home theater

- [`media-operator/`](media-operator/) plays films, music, and series
  on a `Player`, which groups the screens, speakers, and controllers
  in one place.
- [`library-operator/`](library-operator/) keeps a catalog of the
  media libraries that you declare, and draws a media browser on a
  `Player`'s screen.
- [`equipment-operator/`](equipment-operator/) controls the TV and the
  A/V receiver in a room: a receiver over the network, and a TV over
  HDMI-CEC.
- [`people-operator/`](people-operator/) defines the `Person`
  resource, which the other operators refer to for the people who use
  a cluster.
- [`media-screen/`](media-screen/) is the Rust crate that the idle
  screen and the media browser link for their use of the message bus.

## Astronomy

- [`observatory-operator/`](observatory-operator/) runs an
  observatory's equipment through INDI and PHD2 while a `Reservation`
  holds a telescope.
- [`astrophotography-operator/`](astrophotography-operator/) images
  the targets that you declare, on a telescope that
  `observatory-operator` controls.
- [`indi/`](indi/) holds the INDI images that `observatory-operator`
  runs: a library closure on `scratch` of `indiserver` and every core
  driver, and one image on it for each vendor SDK family.
- [`astrometry/`](astrometry/) holds the plate solver that
  `astrophotography-operator` runs.

## Storage

- [`git-csi-driver/`](git-csi-driver/) mounts a git repository as a
  volume, read-only or writeable.
- [`per-node-csi-driver/`](per-node-csi-driver/) gives a pod a
  directory that stays on the node it runs on.

## A map of a cluster

- [`transect/`](transect/) serves a live, read-only map of a
  Kubernetes cluster: its layers, the relationships between its parts,
  and their health.

## What the components build on

- [`kubernetes/`](kubernetes/) is the Go module that the operators
  import to read, write, and watch Kubernetes objects.
- [`vulkan/`](vulkan/), [`vaapi/`](vaapi/), [`ffmpeg/`](ffmpeg/),
  [`mpv/`](mpv/), and [`weston/`](weston/) are the base images that
  the operators' screens, players, and compositor build on. Each one
  is a library closure on `scratch`, built from the Debian packages of
  one snapshot date.
- [`trust/`](trust/) pins the CA bundle that the OS and every image
  that verifies TLS trust: Mozilla's roots, as curl.se publishes them
  by date.
- [`notices/`](notices/) holds the scripts that copy the license
  notices of the Go modules, the Rust crates, and the Debian packages
  in an image into that image.
- [`toolchains/`](toolchains/) pins each outside image that a build
  runs, and holds the library that every pin script uses.
- [`brand/`](brand/) holds the mark, the stylesheet, the Hugo theme,
  the voice rules, and the tools that build the sites.
- [`ci/`](ci/) reads each component's `package.toml`, writes the CI
  workflows from them, and decides what each run builds and publishes.
- [`liken.sh/`](liken.sh/) declares the project's public presence in
  Terraform: the liken.sh DNS zone, the release channel, and the
  settings of every repository in the organization.
- [`plans/`](plans/) holds the plans that cover more than one
  component, and each component keeps its own plans in its own
  `plans/`. The plans of the OS, with its design overview, are in
  [`liken/plans/`](liken/plans/). Plan 69 describes how the components
  came into this repository and how one tag releases them.

## Working in the repository

[`AGENTS.md`](AGENTS.md) holds the rules that every component shares,
and `.agents/skills` holds the procedures for one kind of task each,
such as a release or a change to a Dockerfile. To read every
component's manual together on a workstation, run `make preview` and
open <http://localhost:8080/>. The root `Makefile` explains the
layout.
