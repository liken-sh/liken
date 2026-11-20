# liken

`liken` is an operating system that boots a machine straight into
Kubernetes, and a set of operators that turn the hardware of those
machines into Kubernetes resources. You manage a machine, a monitor,
a speaker, a telescope, and a workload the same way: with a manifest
and `kubectl`.

`liken` aims to be the most Linux-native and Kubernetes-native
operating system there is. Where the kernel or Kubernetes already has
a mechanism, `liken` uses it and does not invent one of its own.

The OS is an immutable image with the Linux kernel, k3s, and one
`init` process. It has no shell and no package manager. It joins each
machine to a cluster, upgrades the fleet one machine at a time, and
publishes each machine's hardware as devices that a pod can claim.
All other software runs in containers. The OS, with the operators
that manage the machines and the cluster, is in [`liken/`](liken/).

Each operator is optional, and you install it like any other manifest.
An operator claims a device that the OS publishes and offers pods
something more specific, such as one monitor output or one speaker.

Each top-level directory is one component. Its `README.md` says what
it is for and links to its manual on [liken.sh](https://liken.sh/).

## Hardware

- [`display-operator/`](display-operator/) lets a pod claim one
  monitor output, and runs the Weston compositor that draws on it.
- [`audio-operator/`](audio-operator/) lets a pod claim a sound
  output or input, and runs PipeWire.
- [`bluetooth-operator/`](bluetooth-operator/) pairs a Bluetooth
  controller through the Kubernetes API and lets one pod claim it.
- [`firmware-operator/`](firmware-operator/) loads firmware into
  devices that store none, and installs the updates that you approve.

## Home theater

- [`media-operator/`](media-operator/) plays films, music, and series
  on a `Player`: the screens, speakers, and controllers in one place.
- [`library-operator/`](library-operator/) builds a catalog of your
  media libraries, and draws a media browser on a `Player`'s screen.
- [`equipment-operator/`](equipment-operator/) controls the TV and
  the A/V receiver in a room, over the network and HDMI-CEC.
- [`people-operator/`](people-operator/) defines the `Person` that
  the other operators use to refer to the people of a cluster.
- [`media-screen/`](media-screen/) is the Rust crate with the message
  bus code that the idle screen and the media browser share.

## Astronomy

- [`observatory-operator/`](observatory-operator/) runs a telescope's
  equipment through INDI and PHD2 for the time of a `Reservation`.
- [`astrophotography-operator/`](astrophotography-operator/) images
  the targets that you declare, through `observatory-operator`.
- [`indi/`](indi/) builds the INDI images: `indiserver` with the core
  drivers, and one image for each vendor's SDK.
- [`astrometry/`](astrometry/) builds the plate solver image.

## Storage and maps

- [`git-csi-driver/`](git-csi-driver/) mounts a git repository as a
  volume, read-only or writeable.
- [`per-node-csi-driver/`](per-node-csi-driver/) gives a pod a
  directory that stays on the node where the pod runs.
- [`transect/`](transect/) serves a live, read-only map of a cluster:
  its layers, how its parts relate, and their health.

## What the components build on

- [`kubernetes/`](kubernetes/) is the Go module that the operators
  import to read, write, and watch Kubernetes objects.
- [`vulkan/`](vulkan/), [`vaapi/`](vaapi/), [`ffmpeg/`](ffmpeg/),
  [`mpv/`](mpv/), and [`weston/`](weston/) are the base images of the
  screens, players, and compositor. Each one is built `FROM scratch`
  from the Debian packages of one snapshot date.
- [`trust/`](trust/) pins Mozilla's CA bundle, which the OS and each
  image that verifies TLS use.
- [`notices/`](notices/) copies the license notices of the Go modules
  and Rust crates in an image into that image.
- [`toolchains/`](toolchains/) pins each outside image that a build
  runs, and has the library that every pin script uses.
- [`brand/`](brand/) has the mark, the stylesheet, the Hugo theme,
  the writing rules, and the tools that build the sites.
- [`ci/`](ci/) writes the CI workflows from each `package.toml`, and
  decides what each run builds and publishes.
- [`liken.sh/`](liken.sh/) is the Terraform for the liken.sh DNS zone,
  the release channel, and the organization's repositories.
- [`plans/`](plans/) has the plans that cover more than one
  component. Each component has its own `plans/`, and the design of
  the OS is in [`liken/plans/`](liken/plans/).

## Working in the repository

[`AGENTS.md`](AGENTS.md) has the rules that apply to every component,
and `.agents/skills` has a skill for each kind of task. One release
tag releases every component that changed. To read every manual
together, run `make preview` and open <http://localhost:8080/>.
