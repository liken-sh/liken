# liken

`liken` is an operating system that boots a machine straight into
Kubernetes, and a set of operators that turn the hardware of those
machines into Kubernetes resources. You manage a machine, a monitor,
a speaker, a telescope, and a workload the same way: with a manifest
and `kubectl`.

The OS is an immutable image with the Linux kernel, k3s, and one
`init` process. It has no shell and no package manager. It joins each
machine to a cluster, upgrades the fleet one machine at a time, and
publishes each machine's hardware as devices that a pod can claim.
All other software runs in containers.

Each operator is optional, and you install it like any other
manifest. An operator claims a device that the OS publishes and
offers pods something more specific, such as one monitor output or
one speaker. A cluster runs fine without any of them.

Every component is in this repository, and each top-level directory
is one component, named for what it ships. A component's `README.md`
says what it is for and what its parts are. If the component has a
manual, the manual is published under [liken.sh](https://liken.sh/),
and its `README.md` links to it. One release tag releases every
component that changed.

## The OS

- [`liken/`](liken/) is the OS: the init, the image, the kernel, k3s,
  and the operators that manage the machines and the cluster.

## Hardware

- [`display-operator/`](display-operator/) lets a pod claim one
  monitor output of a graphics card. It runs the Weston compositor
  that draws on it.
- [`audio-operator/`](audio-operator/) lets a pod claim a sound
  output or input. It runs PipeWire and sets the volume of each
  output.
- [`bluetooth-operator/`](bluetooth-operator/) pairs a Bluetooth
  controller through the Kubernetes API and lets one pod claim it.
- [`firmware-operator/`](firmware-operator/) loads firmware into
  devices that do not store any, and installs the firmware updates
  that you approve on devices that do.

## Home theater

- [`media-operator/`](media-operator/) plays films, music, and series
  on a `Player`. A `Player` is the screens, speakers, and controllers
  in one place.
- [`library-operator/`](library-operator/) builds a catalog of the
  media libraries that you declare, and draws a media browser on a
  `Player`'s screen.
- [`equipment-operator/`](equipment-operator/) controls the TV and
  the A/V receiver in a room. It reaches a receiver over the network
  and a TV over HDMI-CEC.
- [`people-operator/`](people-operator/) defines the `Person`
  resource. The other operators use it to refer to the people who use
  a cluster.
- [`media-screen/`](media-screen/) is a Rust crate with the message
  bus code that the idle screen and the media browser share.

## Astronomy

- [`observatory-operator/`](observatory-operator/) runs an
  observatory's equipment through INDI and PHD2 for as long as a
  `Reservation` has a telescope reserved.
- [`astrophotography-operator/`](astrophotography-operator/) takes
  images of the targets that you declare, on a telescope that
  `observatory-operator` controls.
- [`indi/`](indi/) builds the INDI images that `observatory-operator`
  runs. One image has `indiserver` and every core driver, and there
  is one more image for each vendor's SDK.
- [`astrometry/`](astrometry/) builds the image with the plate solver
  that `astrophotography-operator` runs.

## Storage

- [`git-csi-driver/`](git-csi-driver/) mounts a git repository as a
  volume, read-only or writeable.
- [`per-node-csi-driver/`](per-node-csi-driver/) gives a pod a
  directory that stays on the node where the pod runs.

## A map of a cluster

- [`transect/`](transect/) serves a live, read-only map of a
  Kubernetes cluster: its layers, how its parts relate, and their
  health.

## What the components build on

- [`kubernetes/`](kubernetes/) is the Go module that the operators
  import to read, write, and watch Kubernetes objects.
- [`vulkan/`](vulkan/), [`vaapi/`](vaapi/), [`ffmpeg/`](ffmpeg/),
  [`mpv/`](mpv/), and [`weston/`](weston/) are the base images for
  the operators' screens, players, and compositor. Each one is built
  `FROM scratch` and contains only the libraries it needs, taken from
  the Debian packages of one snapshot date.
- [`trust/`](trust/) pins the CA bundle that the OS and every image
  use to verify TLS. The bundle is Mozilla's root certificates, in
  the dated form that curl.se publishes.
- [`notices/`](notices/) has the scripts that copy license notices
  into an image, for the Go modules, Rust crates, and Debian packages
  in that image.
- [`toolchains/`](toolchains/) pins each outside image that a build
  runs, and has the shell library that every pin script uses.
- [`brand/`](brand/) has the mark, the stylesheet, the Hugo theme,
  the writing rules, and the tools that build the sites.
- [`ci/`](ci/) reads each component's `package.toml`, generates the
  CI workflows from them, and decides what each run builds and
  publishes.
- [`liken.sh/`](liken.sh/) is the Terraform for the project's public
  presence: the liken.sh DNS zone, the release channel, and the
  settings of the organization's repositories.
- [`plans/`](plans/) has the plans that cover more than one
  component. Each component has its own `plans/` too, and the plans
  of the OS, with its design overview, are in
  [`liken/plans/`](liken/plans/). Plan 69 describes how the
  components came into this repository and how one tag releases them.

## Working in the repository

[`AGENTS.md`](AGENTS.md) has the rules that apply to every component.
`.agents/skills` has a skill for each kind of task, such as a release
or a change to a Dockerfile. To read every component's manual
together on a workstation, run `make preview` and open
<http://localhost:8080/>. The root `Makefile` explains the layout.
