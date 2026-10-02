---
title: Claim a GPU by what it decodes
weight: 60
description: "Claim a GPU render node whose media driver decodes, encodes, or scales what a workload needs, by pairing a media.liken.sh capability device with the liken.sh render node of the same GPU. Use when a workload decodes 10-bit or AV1 video on a GPU, when a transcoder needs a hardware encoder, or when a pod with a capability claim does not schedule."
---

# Claim a GPU by what it decodes

A `liken` render node states the GPU's identity, and nothing about
what its media driver can do. On a fleet of mixed GPUs, a claim on any
render node can land on a GPU whose driver cannot decode the file's
codec, or cannot scale its 10-bit frames. The workload then falls
back to a slower path, or fails.

The capabilities agent of `media-operator` publishes what each GPU's
driver states, as a `media.liken.sh` device for each render node. A
claim asks for that device beside the render node, and a constraint
keeps the two on one GPU. The scheduler then places the pod only on a
GPU whose driver states what the claim asks for.

You need:

* `media-operator` installed with the capabilities agent
  ([Install the operator](/docs/guides/install/)).
* A `liken` release that publishes `resource.kubernetes.io/pciBusID`
  on its render nodes. On an older release, the claim below never
  allocates.

## 1. See what each GPU states

    kubectl get resourceslices
    kubectl get resourceslice <node>-media.liken.sh -o yaml

Each device is one GPU:

    - name: pci-0000-00-02-0
      allowMultipleAllocations: true
      attributes:
        address: {string: "0000:00:02.0"}
        decodeAV1Main: {bool: true}
        decodeAV1Main10: {bool: true}
        decodeH264: {bool: true}
        decodeHEVCMain: {bool: true}
        decodeHEVCMain10: {bool: true}
        decodeVP9: {bool: true}
        driver: {string: i915}
        encodeH264: {bool: true}
        encodeHEVCMain: {bool: true}
        encodeHEVCMain10: {bool: true}
        name: {string: Meteor Lake-P [Intel Arc Graphics]}
        product: {string: 7d55}
        resource.kubernetes.io/pciBusID: {string: "0000:00:02.0"}
        scale10bit: {bool: true}
        scale8bit: {bool: true}
        vaDriver: {string: Intel iHD driver for Intel(R) Gen Graphics - 25.2.3 ()}
        vendor: {string: "8086"}

[Render node capabilities](/docs/reference/capabilities/) describes
each attribute, and where its value comes from.

## 2. Claim the render node and the capability together

The claim has two requests: the render node from `liken`, and the
`media.liken.sh` device of a GPU that states what the workload needs.
The constraint requires both devices to have the same
`resource.kubernetes.io/pciBusID`, which is the GPU's PCI address, so
both are the same GPU:

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: decode-10bit
spec:
  spec:
    devices:
      requests:
        - name: gpu
          exactly:
            deviceClassName: media-render
        - name: decodes
          exactly:
            deviceClassName: media-decode-10bit
      constraints:
        - requests: [gpu, decodes]
          matchAttribute: resource.kubernetes.io/pciBusID
```

The pod names the claim, and the container receives the render node
of the GPU the scheduler chose:

```yaml
spec:
  resourceClaims:
    - name: gpu
      resourceClaimTemplateName: decode-10bit
  containers:
    - name: worker
      resources:
        claims:
          - name: gpu
```

The `media.liken.sh` device delivers nothing into the container. It
is a statement about the GPU, and any number of claims can allocate
it at once. The render node is shareable too, so several workloads
decode on one GPU at the same time.

`media-render` is the class the agent's own claim uses, and it
selects every `liken.sh` render node. A class of your own that
selects render nodes, such as `display-render` from
`display-operator`, works the same way.

## 3. Ask for a set no class names

`media-decode-10bit`, `media-decode-av1`, and `media-encode` are the
classes the base ships. For another set, use `media-capabilities` and
a selector in the request:

```yaml
        - name: decodes
          exactly:
            deviceClassName: media-capabilities
            selectors:
              - cel:
                  expression: |
                    device.attributes["media.liken.sh"].decodeVP9 &&
                    device.attributes["media.liken.sh"].scale8bit
```

Every device has every capability attribute, `true` or `false`, so a
selector needs no `has()` check.

## 4. Fall back when no GPU qualifies

A claim that no GPU satisfies stays unallocated, and its pod stays
`Pending`:

    kubectl describe pod <pod>

The events name the claim that cannot allocate. Such a workload has
two ways forward: a second claim on any render node, which the
workload uses with its own fallback path, or no claim at all and work
on the CPU. The scheduler does not choose between them; the workload's
controller does.

## When a GPU states false for a capability it has

The values are the driver's own statements, read through libva. The
agent does not test them. A capability that the GPU has but the
driver in the agent's image does not list publishes as `false`. The
agent's log has the driver's name and every value for each GPU:

    kubectl -n liken-system logs ds/media-capabilities

A query that failed has the failure there, word for word, and the
device then has every capability `false` and no `vaDriver`.
