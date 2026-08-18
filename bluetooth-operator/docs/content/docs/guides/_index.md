---
title: Guides
weight: 10
---

# Guides

The guides give the steps for the two tasks this operator exists
for: the install, and the pairing that puts a controller in one
pod's hands.

## How the pieces fit

Four kinds of [Dynamic Resource Allocation
(DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
object carry a controller from the radio to your container.

The operator publishes what exists. It writes one `ResourceSlice`
for each node, and the slice lists that node's paired controllers
with their attributes. The slices are the inventory the scheduler
reads.

A `DeviceClass` names a kind of device a workload can ask for. The
classes are yours to create, and
[Install the operator](/docs/guides/install/) gives the YAML:
`bluetooth-controller` names a paired controller. A class can be
generic like that one, or specific down to a single device;
[Generic or specific](/docs/guides/install/#generic-or-specific)
weighs the choice. A workload asks
with a `ResourceClaim`, or with a `ResourceClaimTemplate` under a
`Deployment`. The claim's selector is a
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/)
expression over the published attributes:
`device.attributes["bluetooth.liken.sh"].address == "A0:AB:51:33:B7:12"`
selects one controller by its MAC address.

The scheduler matches the claim against the slices, allocates one
device, and places the pod on that device's node. The
`bluetooth.liken.sh` driver then delivers the device: the kubelet
calls it to prepare the claim, and the container starts with the
controller's evdev nodes.
