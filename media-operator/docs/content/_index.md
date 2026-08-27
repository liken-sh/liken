---
title: media.liken.sh
---

# `media.liken.sh`

`media-operator` is a Kubernetes operator for
the routing and control of media playback in a cluster. It runs on a
[`liken`](https://liken.sh/docs/) cluster above the hardware
operators: the [`display-operator`](https://display.liken.sh),
[`audio-operator`](https://audio.liken.sh), and
[`bluetooth-operator`](https://bluetooth.liken.sh) publish each
display, speaker, and controller as a claimable device, and this
operator declares what those devices form together.

The API is five resources. A `Player` is one unit of equipment in
one place: a lone speaker, a TV with its surround pair, a gaming TV
with its controllers. A `Play` is one run of media
on a player, with a lifecycle analogous to a `Job`: a film, an
album, or a season of episodes, played in order and run to
completion. Create it to start, delete it to stop, and
`kubectl get plays` lists what plays right now. A `Remote` is one
physical controller, bound to the players it drives. A `Keymap` maps
one controller model's buttons to named media actions. A fifth,
`MediaPreferences`, holds one cluster-wide default for audio and
subtitle languages.

The operator reconciles a `Play` into one playback pod beside the
hardware, running [`mpv`](https://mpv.io/) under a thin supervisor,
with every device claim, toleration, and socket built from the
`Player` spec. Each `Remote` has its own pod, which publishes
button events to the cluster's [message bus](/docs/reference/bus/),
and the playback pod applies the bindings. Between runs, each
`Player`'s idle pod holds its display: it shows a clock and the
unit's name and fades them after minutes of no activity. After
longer, the operator darkens the panel through the display layer's
own resource for the screen.

Media you can run this way:

* a film to a TV with its surround pair, the picture on the claimed
  display and the sound on the claimed sinks,
* an album to a lone speaker,
* a season of episodes, played in order and run to completion,
* any `https://` stream or `nfs://` mount, named by URI in the
  `Play`.

Start here:

* [Install the operator](/docs/guides/install/). The install applies
  the manifests this site serves at
  [`/deploy/`](/deploy/kustomization.yaml), so it needs no clone.
* [The message bus](/docs/reference/bus/): every topic the operator,
  the pods, and your own programs share.
* [Reference](/docs/reference/): each resource, its fields, and the
  bus topics it speaks on.

The operator publishes no devices of its own. A `Player` selects
devices out of what the hardware operators publish, with the same
[CEL](https://kubernetes.io/docs/reference/using-api/cel/) selectors
a hand-written `ResourceClaim` would use, and the operator writes
the claims itself: one long-lived claim for each `Player`'s idle
display, and one claim per run for what a `Play` needs. A cluster that never installs
this operator runs unchanged. Media library management is a separate
concern, and this project does none of it.

* [The repository](https://github.com/liken-sh/media-operator)
* [The `liken` manual](https://liken.sh/docs/)
