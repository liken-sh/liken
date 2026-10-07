---
title: How a claim reaches your pod
weight: 10
---

# How a claim reaches your pod

Your pod gets its device through
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/).

**The `ResourceSlice`** lists the devices on one node. The operator
writes one for each node, with the node's paired controllers and
their attributes.

**A `DeviceClass`** names a kind of device that a workload can ask
for. The manifests ship only `bluetooth-adapter`, which the operator's
own pod uses. The class that your workloads claim through is yours to
create, and [Install the operator](/docs/guides/install/) gives the
YAML for `bluetooth-input`, which matches any paired input device. A
class can also pick a single device.
[Generic or specific](/docs/guides/install/#generic-or-specific)
explains how to choose.

**A `ResourceClaim`** is a workload's request, or a
`ResourceClaimTemplate` under a `Deployment`. Its selector is a
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/)
expression over the attributes. For example,
`device.attributes["bluetooth.liken.sh"].address == "A0:AB:51:33:B7:12"`
selects one controller by its MAC address.

**The scheduler** matches the claim against the slices, allocates one
device, and places the pod on that device's node. When the pod
starts, the operator gives the container the controller's input
device nodes.
