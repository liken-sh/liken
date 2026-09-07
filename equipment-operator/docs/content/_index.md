---
title: equipment.liken.sh
---

A `Receiver` is one A/V receiver on the far end of a `liken`
machine's HDMI cable. The cable carries the picture and the sound,
and the receiver's own remote or a socket on the network carries the
control. `equipment-operator` reaches the receiver over the network,
reports its power, input, and volume, and while a `Player` plays
through it, powers it on, selects the input, and turns its volume
from the room's remote.

Equipment is anything a `Player` needs to reach the room and that
`liken` does not host. A receiver is the first kind. This site
describes the `Receiver` and the operator that drives it.

Start with the [manual](docs/). The design and the plans are in the
[repository](https://github.com/liken-sh/equipment-operator).
