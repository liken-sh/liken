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
  `display.liken.sh`.
- [`vulkan/`](vulkan/), [`vaapi/`](vaapi/), [`ffmpeg/`](ffmpeg/),
  [`mpv/`](mpv/), and [`weston/`](weston/) are the base images that
  the operators' screens, players, and compositor build on. Each one
  is a library closure on `scratch`, built from the Debian packages of
  one snapshot date.
- [`indi/`](indi/) holds the INDI images that `observatory-operator`
  runs: a library closure on `scratch` of `indiserver` and every core
  driver, and one image on it for each vendor SDK family, built from
  dated snapshots of Ubuntu and the INDI PPA.
- [`equipment-operator/`](equipment-operator/) drives the A/V equipment
  at the far end of a machine's cable: receivers over the network, and
  televisions over CEC.
- [`media-operator/`](media-operator/) declares players, plays,
  remotes, and keymaps, and reconciles them into pods that claim the
  hardware operators' devices.
- [`library-operator/`](library-operator/) declares media libraries,
  keeps a catalog of what they hold, and puts a media browser on the
  screens.
- [`media-screen/`](media-screen/) is the Rust crate that holds the
  bus rules of a screen client. The idle screen and the media browser
  both link it.
- [`people-operator/`](people-operator/) defines the `Person` resource.
- [`observatory-operator/`](observatory-operator/) will control an
  observatory's hardware through INDI, and
  [`astrophotography-operator/`](astrophotography-operator/) will run
  imaging sessions on it. Both hold only plans so far.
- [`git-csi-driver/`](git-csi-driver/) mounts git repositories as
  volumes.
- [`per-node-csi-driver/`](per-node-csi-driver/) gives a pod a
  directory that stays on the node it runs on.
- [`brand/`](brand/) holds the mark, the stylesheet, the Hugo theme,
  the voice rules, and the tools that build the sites.
- [`kubernetes/`](kubernetes/) is the Go module that the operators
  import to read, write, and watch Kubernetes objects.

Each component has its own `README.md`, `AGENTS.md`, and `Makefile`.
[`liken.sh/`](liken.sh/) declares the project's public presence in
Terraform: the liken.sh DNS zone, the release channel, and the
settings of every repository in the organization.
[`ci/`](ci/) reads each component's `package.toml`, writes the CI
workflows from them, and decides what each run builds and publishes.
[`plans/`](plans/) holds the plans that cover more than one component,
and each component keeps its own plans in its own `plans/`. The plans
of the OS, with its design overview, are in
[`liken/plans/`](liken/plans/). Plan 69
describes how the components came into this repository and how one
tag releases them. [`AGENTS.md`](AGENTS.md) holds the rules for
agents that work in the repository, and `.agents/skills` holds their
skills.

The manual is at [liken.sh](https://liken.sh/). To read every
component's manual together on a workstation, run `make preview` and
open <http://localhost:8080/>. The root `Makefile` explains the
layout.
