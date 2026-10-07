---
title: How a claim reaches your pod
weight: 10
---

# How a claim reaches your pod

Your pod gets its device through
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/).

**The `ResourceSlice`** lists the devices on one node. The operator's
pod on each node writes one, with a device for each output and input
of the sound card, named like `kitchen-pci-0000-00-1f-3-hdmi-0`. Each
device carries facts about the endpoint as attributes, such as the
node that a stream goes to and `monitor.liken.sh/id`, which pairs a
monitor's speakers with its screen. When
[`bluetooth-operator`](https://liken.sh/bluetooth/) publishes a media
bus for the node's radio, the slice also has a device for each paired
Bluetooth speaker, named by its MAC address, and everything below
works the same way for it.

Each endpoint is also a `Sink` or a `Source` resource. There you can
read what the endpoint is doing, and set its volume, mute, and
controls without a claim.

**A `DeviceClass`** names a kind of device that a workload can ask
for. Which classes a cluster offers is your decision, so you create
them yourself, usually `audio-sink` and `audio-source`, one for each
direction. [Install the operator](/docs/guides/install/) gives the
YAML. A class can also be specific and carry its own selector, so
claims don't need one.
[Generic or specific](/docs/guides/install/#generic-or-specific)
shows both kinds.

**A `ResourceClaim`** is a workload's request, or a
`ResourceClaimTemplate` when each pod of a `Deployment` needs its own
device. The claim names a class and narrows it with a selector, an
expression in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/)
over the device's attributes, such as "the speakers of this monitor"
or "any analog jack".

**The scheduler** matches the claim against the slices, allocates one
device that matches, and places the pod on that device's node. When
the pod starts, the operator gives the container the PipeWire socket
and the name of the sink that its streams must play to.
