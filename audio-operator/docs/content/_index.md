---
title: audio-operator
---

# `audio-operator`

`audio-operator` lets a pod on a [`liken`](https://liken.sh/docs/)
cluster play sound through a real speaker, or record from a
microphone, with no privilege and no host path. You write a claim
that names the output, such as the speakers of one monitor, the analog
jack, or a Bluetooth speaker. The pod that uses the claim gets a
PipeWire socket and the name of the sink to play to. You configure
nothing on the machine.

With it, you can play:

* a video's sound on the speakers of the monitor that shows its
  picture, together with
  [`display-operator`](https://liken.sh/display/),
* music from a player pod to the amplifier on the analog jack,
* an announcement on the speakers of one named monitor,
* music to a paired Bluetooth speaker, which you claim by its MAC
  address like any other output.

Start with these guides:

* [Install the operator](/docs/guides/install/). The install applies
  the manifests that this site serves at
  [`/deploy/`](/deploy/kustomization.yaml), so you don't need a clone.
* [Play sound to an output](/docs/guides/claim/): the claim, the
  `Deployment`, and what the container gets.
* [Pair sound with its screen](/docs/guides/pair/): one claim for a
  monitor's screen and that monitor's speakers.
* [Devices](/docs/reference/devices/): the attributes that a claim can
  select on.

## How it works

The operator runs [PipeWire](https://pipewire.org/) and
[WirePlumber](https://pipewire.pages.freedesktop.org/wireplumber/) in
its own pod, so the machine's system image has no sound server. The
pod claims the machine's sound card, which `liken` publishes. The
operator then publishes each output and input of the card as its own
device, under the
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver `audio.liken.sh`. Your pod claims one of those devices, and the
scheduler places the pod on the machine that has it.

[`bluetooth-operator`](https://liken.sh/bluetooth/) publishes the
media bus of each Bluetooth radio, and this operator claims that bus
too. The same PipeWire then plays Bluetooth audio, and each paired
speaker appears as an `audio.liken.sh` device beside the outputs of
the card.

`audio-operator` is one of the extension operators for
[claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/).
Install it only if your cluster needs sound.

* [The source](https://github.com/liken-sh/liken/tree/main/audio-operator)
* [The `liken` manual](https://liken.sh/docs/)
