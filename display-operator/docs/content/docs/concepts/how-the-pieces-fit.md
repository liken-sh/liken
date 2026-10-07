---
title: How a claim reaches your pod
weight: 10
---

# How a claim reaches your pod

Your pod gets its device through
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/).

**The [`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)**
lists the devices on one node. The operator's pod on each node writes
one, with a device for each connector of the graphics card. Each
device carries the monitor's facts as attributes, such as
`connector`, `model`, and `serial`.

**A [`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)**
names a kind of device that a workload can ask for. Which classes a
cluster offers is your decision, so you create them yourself.
`display-output`, which matches every monitor output, is the one to
start with, and the
[install guide](/docs/guides/install/#2-the-device-classes) gives its
YAML. A class can also pick one screen.
[Generic or specific](/docs/guides/install/#generic-or-specific)
explains how to choose.

**A [`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)**
is a workload's request. It names a class and narrows it with a
selector written in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/),
such as "the output whose `connector` is `HDMI-A-1`" or "any output
whose monitor is an LG HDR WQHD". A `Deployment` can name one claim,
or create one for each pod from a `ResourceClaimTemplate`.

**The scheduler** matches the claim against the slices, allocates one
output, and places the pod on that output's machine. When the pod
starts, the operator gives the container a Wayland socket that the
compositor opened for that claim, and a window on that socket shows
on that screen.

Separately from claims, the operator creates one
[`Display`](/docs/reference/displays/) for each monitor. It reports
the panel's controls, and you can set them there without a claim or a
pod.
