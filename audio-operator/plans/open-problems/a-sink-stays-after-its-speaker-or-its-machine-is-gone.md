# A `Sink` stays after its speaker is unpaired or its machine is removed

Open bug, recorded on 2026-11-10. It needs a design decision before
it is built.

`audio-operator` never deletes a `Sink` or a `Source`.
`audio-operator/sweep.go` gives the reason: the resource holds the
declaration that a person wrote, so a USB card that is unplugged for
an hour comes back at the level it rested at. The machine that
`status.node` names reports the absence in the conditions instead.
That is correct for hardware that can come back. In two cases the
hardware cannot come back, and the resource stays until a person
deletes it.

## A Bluetooth speaker that was unpaired

When a person pairs a speaker with a machine and then unpairs it, the
`Peripheral` is deleted, and the speaker's `Sink` stays with
`Connected=False/EndpointAbsent` and `Ready=False/NoNode`. Only
`kubectl delete sink` removes it. On a home cluster, one such `Sink`
had reported the absence for two months, and its `spec` was empty.

A person who unpairs a speaker states that the speaker is gone, so
the `Sink` must go with it. One remedy is an owner reference: the
`Sink` of a Bluetooth speaker names its `Peripheral` as its owner, and
the garbage collector deletes the `Sink` when the `Peripheral` is
deleted. Both kinds are cluster-scoped, so the API server accepts the
reference. The remedy also keeps what `sweep.go` requires for a
speaker that moves to another machine, because the `Peripheral` moves
with the speaker.

Two questions are open:

- `audio-operator` reads the paired set from bluetoothd, not from the
  `Peripheral`. An owner reference needs the `Peripheral`'s UID, so
  the operator would have to read the `Peripheral` too.
- What the `Sink` of a Bluetooth speaker gets on a cluster where
  `bluetooth-operator` is not installed, and no `Peripheral` exists.

## A machine that was removed from the cluster

When a person removes a machine by the remove-nodes guide, the
machine's `Sink`s and `Source`s stay, and each one's status is what
the machine last wrote. Only the machine that `status.node` names
sweeps a resource, so no operator writes these resources again. On a
home cluster, four HDMI `Sink`s still read `Ready=True/NodePresent`,
with a `nodeName`, ten days after their machine's `Node` was deleted.
A reader takes each one for a healthy endpoint, on a machine that
does not exist.

`audio-operator` already deletes a machine's `ResourceSlice` this
way: `NodeOwner` in `audio-operator/slices.go` builds an owner
reference to the `Node`. The name of a sound card's `Sink` or `Source`
holds the name of its machine, so the resource can name the same
owner. The `Sink` of a Bluetooth speaker cannot, because the speaker
can move between machines.

## What this problem does not cover

A card can leave a machine that stays in the cluster, such as an HDMI
codec whose driver the machine no longer loads. Its `Sink`s then
report the absence with no end. This case is harder, because the
operator cannot tell such a card from a card that will come back. A
`Sink` with an empty `spec` holds no declaration to keep, and that
can be enough reason for the sweep to delete it.
