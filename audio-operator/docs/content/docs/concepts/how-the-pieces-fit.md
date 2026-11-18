---
title: How a claim reaches your pod
weight: 10
---

# How a claim reaches your pod

Your pod gets its audio device through
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/),
the Kubernetes API for claiming hardware. Every guide in this manual
uses the objects below.

**The `ResourceSlice`** lists the devices on one node. The operator's
pod on each node writes one slice, with a device for each output and
input of the sound card, named like
`kitchen-pci-0000-00-1f-3-hdmi-0`. Each device has attributes that
describe the endpoint, such as the PipeWire node name that a stream
plays to, and `monitor.liken.sh/id`, which pairs a monitor's speakers
with its screen. When
[`bluetooth-operator`](https://liken.sh/bluetooth/) publishes a media
bus for the node's radio, the slice also has a device for each paired
Bluetooth speaker, named by its MAC address. Everything below works
the same way for a speaker.

Each endpoint is also a `Sink` or a `Source` resource. You read the
endpoint's state from that resource, and you set the endpoint's
volume, mute, and controls on it without a claim.

**A `DeviceClass`** names a kind of device that a workload can ask
for. You decide which classes the cluster offers, so you create them.
They are usually `audio-sink` and `audio-source`, one for each
direction. [Install the operator](/docs/guides/install/) has the YAML.
A class can also have a selector of its own, so that claims don't need
one. [Generic or specific](/docs/guides/install/#generic-or-specific)
shows both kinds.

**A `ResourceClaim`** is a workload's request for a device. Use a
`ResourceClaimTemplate` when each pod of a `Deployment` needs its own
device. The claim names a class and can narrow it with a selector. The
selector is an expression in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/)
over the device's attributes, such as "the speakers of this monitor"
or "any analog jack".

**The scheduler** matches the claim against the slices, allocates one
device that matches, and places the pod on that device's node. When
the pod starts, the operator gives the container the PipeWire socket
and the name of the sink that its streams must play to.
