# Bonds follow the pod's ordinal and adapters do not

Open problem. A Bluetooth link key belongs to one adapter, and the
StatefulSet gives a replica its bonds volume by ordinal. On a fleet
with more than one adapter, the two do not have to agree, and nothing
in the StatefulSet vocabulary makes them agree.

## The mechanism

Three facts combine.

- A link key binds to the adapter's own MAC address. BlueZ stores the
  bonds under that address on the `bonds` volume, so a bond written
  against one radio means nothing to another radio.
- The `volumeClaimTemplates` key each volume by replica ordinal.
  Replica 0 always mounts `bonds-bluetooth-operator-0`, whatever
  hardware that pod ends up holding.
- The adapter comes from a ResourceClaimTemplate, so each pod
  allocates an adapter afresh when it is created. The scheduler picks
  a machine that has a free adapter. Nothing in the claim says which
  one, and nothing carries the previous allocation forward.

So a recreated ordinal 0 can allocate a different machine's radio than
the one its bonds were written for. The bonds it mounts are for the
old radio, the new radio has no bonds at all, and every controller
paired to that machine has to be paired again by hand.

A cluster with one adapter never sees this, because there is only one
allocation to make. The README's paragraph on more than one adapter,
under "Deploying it", says to raise `replicas` to the number of
adapters. That instruction is correct about placement and silent about
this.

## What a fix would have to do

A fix has to tie the volume to the allocation, not to the ordinal.
The Kubernetes objects in play offer no way to say that. A
`volumeClaimTemplate` keys on the ordinal, a ResourceClaimTemplate
allocates per pod, and neither one reads the other. No fix is known
today.

Two shapes are worth thinking about, and neither one has been
designed:

- Take the bonds off the ordinal. Put every adapter's bonds in one
  place keyed by adapter address, on storage that any replica can
  mount. That trades the ordinal problem for a `ReadWriteMany`
  requirement and for the question of what happens when two replicas
  land on the same directory.
- Take the allocation off the pod. Give each adapter a named claim
  that a person writes once, so replica 0 always allocates the same
  radio. That means somebody writes down which machine has which
  adapter, which is the thing the raw claim exists to avoid.

## What it waits on

A drill with more than one adapter. A QEMU lab with two emulated
adapters would size the problem without hardware: delete the pods,
watch which claim allocates which adapter, and record how often the
allocation changes. Until that runs, the size of the problem is a
guess.

This is recorded in liken's
[milestone 58](https://github.com/liken-sh/liken/blob/main/plans/58-the-bluetooth-operator.md)
as well. It belongs here, with the operator that has the StatefulSet.

Two adapters on one machine hit a second problem that this one does not
cause. `sliceName` names the ResourceSlice for the node and the driver
alone, so two replicas on the same machine write to the same object and
each pass removes the other's devices. The audio operator has the same
naming and the same collision, and its
[The claim takes any sound card, and a node serves only one](https://github.com/liken-sh/audio-operator/blob/main/plans/open-problems/the-claim-takes-any-sound-card-and-a-node-serves-only-one.md)
works it through for both operators.
