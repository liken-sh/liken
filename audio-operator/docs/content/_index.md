---
title: audio-operator
---

# `audio-operator`

`audio-operator` lets a pod on a [`liken`](https://liken.sh/docs/)
cluster play sound through a real speaker, or record from a
microphone, with no privilege and no host path. You write a claim
that names the output, such as the speakers of one monitor, the
analog jack, or a Bluetooth speaker, and the pod that holds the claim
gets a PipeWire socket and the name of the sink to play to. You don't
configure anything on the machine.

Things you can run this way:

* a video's sound on the speakers of the monitor that shows its
  picture, together with
  [`display-operator`](https://liken.sh/display/),
* music from a player pod to the amplifier on the analog jack,
* an announcement on the speakers of one named monitor,
* music to a paired Bluetooth speaker, which you claim by its MAC
  address like any other output.

Start here:

* [Install the operator](/docs/guides/install/). The install applies
  the manifests that this site serves at
  [`/deploy/`](/deploy/kustomization.yaml), so you don't need a clone.
* [Play sound to an output](/docs/guides/claim/): the claim, the
  `Deployment`, and what the container gets.
* [Pair sound with its screen](/docs/guides/pair/): one claim for a
  monitor's screen and that monitor's speakers together.
* [Devices](/docs/reference/devices/): every attribute that a claim
  can select on.

## How it works

The operator runs [PipeWire](https://pipewire.org/) and
[WirePlumber](https://pipewire.pages.freedesktop.org/wireplumber/) in
its own pod, so the machine's system image has no sound server. The
pod claims the machine's sound card, which `liken` publishes, and
publishes each output and input of the card as its own device under
the
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver `audio.liken.sh`. Your pod claims one of those devices, and
the scheduler places it on the machine that has it.

[`bluetooth-operator`](https://liken.sh/bluetooth/) publishes the
media bus of each Bluetooth radio, and this operator claims it too.
So the same PipeWire also runs Bluetooth audio, and each paired
speaker appears as an `audio.liken.sh` device beside the card's
outputs.

`audio-operator` is one of the extension operators for
[claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/).
You install it only if your cluster needs sound.

* [The source](https://github.com/liken-sh/liken/tree/main/audio-operator)
* [The `liken` manual](https://liken.sh/docs/)
