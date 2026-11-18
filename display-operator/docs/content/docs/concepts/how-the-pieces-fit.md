---
title: How a claim reaches your pod
weight: 10
---

# How a claim reaches your pod

Your pod gets a screen through
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/),
the Kubernetes API for claiming hardware. `display-operator` publishes
each monitor output as a device, and when a pod's claim is allocated
an output, the operator connects the pod to the compositor that draws
on it.

**The [`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)**
lists the devices on one node. The operator's pod on each node writes
one, with the devices of each connector of the graphics card. The
attributes of a device describe the connector and the monitor, such as
`connector`, `model`, and `serial`.

**A [`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)**
describes a kind of device that a workload can request. The classes
are cluster policy, so you create them. Start with
`display-output`, which matches every monitor output. The
[install guide](/docs/guides/install/#2-the-device-classes) gives its
YAML. A class can also match one screen.
[Generic or specific](/docs/guides/install/#generic-or-specific)
explains how to choose.

**A [`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)**
is a workload's request for a device. It refers to a class and can
narrow it with a selector written in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/),
such as "the output whose `connector` is `HDMI-A-1`" or "any output
whose monitor is an LG HDR WQHD". A `Deployment` can refer to one
claim, or create a claim for each pod from a `ResourceClaimTemplate`.

**The scheduler** matches the claim against the slices, allocates one
output, and places the pod on the machine that has that output. When
the pod starts, the operator gives its container a Wayland socket that
the compositor opened for that claim. A window that the pod opens on
that socket appears on that screen.

The operator also creates a [`Display`](/docs/reference/displays/)
for each monitor, whether or not a claim exists. Its status lists the
panel's controls, and you can set the panel's brightness and power
in its spec without a claim or a pod.
