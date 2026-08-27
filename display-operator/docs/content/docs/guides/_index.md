---
title: Guides
weight: 10
---

# Guides

The guides give the steps for the two tasks this operator exists
for: the install, and the claim that puts a window on a screen.

## How the pieces fit

[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
takes a screen from the machine to your container in four steps:
the inventory, the class, the claim, and the delivery.

The operator publishes what exists. Each node's
[`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)
is the inventory the scheduler reads. It has one device per
connector on the graphics card, with the monitor's facts as
attributes: `connector`, `model`, `serial`, and the rest.

A [`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
names a kind of device a workload can ask for: `display-output` for
this operator's monitor outputs. The base ships the generic
classes, and a class that picks one screen is cluster policy, yours
to create; the
[install guide](/docs/guides/install/#2-the-device-classes)
explains each one. A class can be generic, matching every output,
or specific to one screen;
[Generic or specific](/docs/guides/install/#generic-or-specific)
says how to choose.

A workload asks with a
[`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/).
The claim narrows the class with a selector written in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/).
A selector names the output whose `connector` is `HDMI-A-1`, or any
output whose monitor is an LG HDR WQHD. A `Deployment` can reference one claim by
name, or create one per pod from a `ResourceClaimTemplate`.

The scheduler matches the claim against the slices, allocates one
output, and places the pod on that output's machine. The kubelet
then asks this driver to prepare the claim, and the driver delivers
the device to the container. For a monitor output, the delivery is
the compositor's Wayland socket and the app-id that puts your window
on that screen.

Beside the claim path, the operator creates one
[`Display`](/docs/reference/displays/) per monitor: a cluster-scoped
resource that reports the panel's controls and takes declarations
for them, with no claim and no pod involved.
