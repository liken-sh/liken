# The compositor drives one card

Open problem. The operator runs one Weston kiosk bound to one DRM card,
and it runs as a `DaemonSet`, one pod per node. A node with two graphics
cards serves only the card the claim took. The monitors on the second
card publish no device.

## Why one card

Weston's drm-backend opens one DRM device and drives the connectors on
that card. One pod is one compositor, and one compositor is one card.
The operator's claim asks for one card node and one render node, so the
scheduler gives the pod one card. The pod publishes that card's
connectors and nothing else.

The audio operator does not have this limit, and the difference is the
daemon. One PipeWire serves every ALSA card on a machine. So the audio
operator claims every sound controller on its node with
`allocationMode: All` and publishes them all from one slice. Weston has
no equivalent. Two cards need two compositors.

## Why two compositors do not fit

Two compositors are two pods. A DRA driver's slice identity is
`<node>-<driver>`, so both pods would write the one slice named
`<node>-display.liken.sh`. The conflict-retry loop would make them
overwrite each other on every pass. One node has one publisher per
driver, so the operator cannot answer two cards with two pods.

## What breaks on a two-card node

The claim takes one card node, and nothing in the claim chooses which.
The scheduler picks. The other card's connectors publish no device, so
a monitor on the second card cannot be claimed, and a pod that selects
it stays `Pending`.

## What an answer has to weigh

The question is whether one operator process can serve both cards. Two
shapes, and the slice rules out the simpler one.

* One pod that runs a compositor per card and publishes every card's
  outputs into the one slice. Weston runs more than once in the pod,
  each instance on its own DRM device, and the operator routes and
  taints per card. This is the shape that fits the slice, and it is
  the most work.
* Distinct driver names per card, one slice each. This splits the
  `display.liken.sh` contract and the `display-output` class, so a
  consumer must know which card's driver to claim.

## What is not measured

No liken machine has two graphics cards today. liken-1 has one
integrated GPU. So nobody has measured the cost of this limit, and the
shape of the answer is a guess until a two-card machine runs the
operator.
