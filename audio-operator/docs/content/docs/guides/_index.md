---
title: Guides
weight: 10
---

# Guides

The guides give the steps for the four tasks this operator exists
for: the install, the claim that plays sound to an output, the
claim that pairs a monitor's speakers with that monitor's screen,
and the declaration that sets what an endpoint rests at.

## How the pieces fit

Every guide moves through the same four
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
objects, so here is the whole arrangement once.

The operator publishes what exists. Its pod on each node writes one
`ResourceSlice`, the inventory the scheduler reads. It has one
device for each physical endpoint, named like
`kitchen-pci-0000-00-1f-3-hdmi-0`, with the facts about it as
attributes, such as the node a stream targets and the
monitor-pairing identity `monitor.liken.sh/id`. The same endpoint
is also a `Sink` or a `Source` of its own, where you can read what
it is doing and set its volume, mute, and controls without a
claim.

On a machine whose radio the Bluetooth operator publishes a media
bus for, the same slice holds one device for each paired Bluetooth
speaker, named by its MAC address. Everything below applies to a
speaker unchanged.

A `DeviceClass` names a kind of device a workload can ask for. A
class is cluster policy, so you create it yourself: `audio-sink`
and `audio-source`, the classes that cover each direction this
driver publishes.
[Install the operator](/docs/guides/install/) gives its YAML. A
class can also be specific, with the selector in the class itself, so claims write
none; [Generic or specific](/docs/guides/install/#generic-or-specific)
shows both grains.

A workload asks with a `ResourceClaim`, or with a
`ResourceClaimTemplate` when each pod of a `Deployment` needs a
device of its own. The claim names the class, and it narrows the
class with a selector: an expression in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/)
over a device's attributes, such as the speakers of one monitor or
any analog jack.

The scheduler matches the claim against the slices, allocates one
matching device, and places the pod on that device's node. When the
pod starts, this driver delivers the device to the container: the
PipeWire socket, and the name of the sink its streams must reach.
