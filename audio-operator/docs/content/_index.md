---
title: audio.liken.sh
---

# `audio.liken.sh`

`audio-operator` gives a Kubernetes workload a place to play sound.
It publishes each physical audio output of a
[`liken`](https://liken.sh/docs/) machine, a monitor's speakers or
the analog jack, as a device on the cluster, through
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/),
the Kubernetes API for devices. The operator runs
[PipeWire](https://pipewire.org/) and
[WirePlumber](https://pipewire.pages.freedesktop.org/wireplumber/)
in its pod, so the machine's system image contains no sound server. A
pod that claims an output receives the PipeWire socket and the name
of the sink its streams must reach.

A claim and a `Deployment` are the whole task. The claim names the
output: a monitor's speakers by the monitor's identity, the analog
jack by its connection type, or one output by name. The `Deployment`
references the claim, and its container receives the socket and the
sink name. No step touches the machine itself: no SSH, no
configuration on the host, no privileged pod.

Sound you can run this way:

* a video's sound on the same monitor that shows its picture, paired
  with the [display operator](https://display.liken.sh),
* music from a player pod to the amplifier on the analog jack,
* an announcement to the speakers of a named monitor.

Start here:

* [Install the operator](/docs/guides/install/). The install applies
  the manifests this site serves at
  [`/deploy/`](/deploy/kustomization.yaml), so it needs no clone.
* [Play sound to an output](/docs/guides/claim/): the claim, the
  `Deployment`, and what the container receives.
* [Pair sound with its screen](/docs/guides/pair/): one claim that
  holds a monitor's screen and that monitor's speakers.
* [Devices](/docs/reference/devices/): every attribute a claim can
  select on, the taints, and the delivery.

The operator is one of the
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/),
the optional layer above the operating system. A cluster that never
installs it runs unchanged. `liken` itself publishes the sound card;
this operator claims that card and publishes its outputs, one device
for each playback PCM device, under `audio.liken.sh`. Its siblings
publish [monitor outputs](https://display.liken.sh) and
[Bluetooth controllers](https://bluetooth.liken.sh). A monitor's
speakers pair with its screen through `monitor.liken.sh/id`, the
identity both drivers read from the same monitor.

* [The repository](https://github.com/liken-sh/audio-operator)
* [The `liken` manual](https://liken.sh/docs/)
