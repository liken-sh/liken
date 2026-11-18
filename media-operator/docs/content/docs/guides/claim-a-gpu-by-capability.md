---
title: Claim a GPU by what it decodes
weight: 60
description: "Claim a GPU render node whose media driver decodes, encodes, or scales what a workload needs, by pairing a media.liken.sh capability device with the liken.sh render node of the same GPU. Use when a workload decodes 10-bit or AV1 video on a GPU, when a transcoder needs a hardware encoder, or when a pod with a capability claim does not schedule."
---

# Claim a GPU by what it decodes

A `liken` render node identifies a GPU, but it does not say what the
GPU's media driver can do. On a fleet with different GPUs, a claim on
any render node can be allocated a GPU whose driver cannot decode the
file's codec or scale its 10-bit frames. The workload then uses a
slower path or fails.

The capabilities agent of `media-operator` publishes what each GPU's
driver supports, as a `media.liken.sh` device for each render node. A
claim requests that device together with the render node, and a
constraint requires both to be on the same GPU. The scheduler then
places the pod only on a node with a GPU whose driver supports what the
claim needs.

You need:

* `media-operator` installed with the capabilities agent
  ([Install the operator](/docs/guides/install/)).
* A `liken` release that publishes `resource.kubernetes.io/pciBusID`
  on its render nodes. On an older release, the claim below never
  allocates.

## 1. See what each GPU supports

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
        decodeMPEG2: {bool: true}
        decodeVC1: {bool: false}
        decodeVP8: {bool: true}
        decodeVP9: {bool: true}
        decodeVP9Profile2: {bool: true}
        driver: {string: i915}
        encodeH264: {bool: true}
        encodeH264LowPower: {bool: false}
        encodeHEVCMain: {bool: true}
        encodeHEVCMain10: {bool: true}
        encodeHEVCMain10LowPower: {bool: false}
        encodeHEVCMainLowPower: {bool: false}
        name: {string: Meteor Lake-P [Intel Arc Graphics]}
        product: {string: 7d55}
        resource.kubernetes.io/pciBusID: {string: "0000:00:02.0"}
        scale10bit: {bool: true}
        scale8bit: {bool: true}
        vaDriver: {string: Intel iHD driver for Intel(R) Gen Graphics - 26.1.2 ()}
        vendor: {string: "8086"}

[Render node capabilities](/docs/reference/capabilities/) describes
each attribute and where its value comes from.

## 2. Write the classes

`media-operator` does not ship a class for a capability. Each cluster
needs different classes for its workloads, so you write them. This
guide uses two.

The first selects the `media.liken.sh` devices of GPUs that decode
10-bit HEVC and scale 10-bit frames on the video processor. A 10-bit
HEVC film needs both:

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: decode-10bit
spec:
  selectors:
    - cel:
        expression: |
          device.driver == "media.liken.sh" &&
          has(device.attributes["media.liken.sh"].decodeHEVCMain10) &&
          device.attributes["media.liken.sh"].decodeHEVCMain10 &&
          has(device.attributes["media.liken.sh"].scale10bit) &&
          device.attributes["media.liken.sh"].scale10bit
```

The second selects the render nodes that `liken` publishes. If you
already have a class like it, such as `display-render` from
`display-operator`, use that class and skip this one:

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: gpu-render
spec:
  selectors:
    - cel:
        expression: |
          device.driver == "liken.sh" &&
          has(device.attributes["liken.sh"].renderNode)
```

Do not use `media-render` for your workloads. The install ships it for
the capabilities agent's own claim.

## 3. Claim the render node and the capability together

The claim has two requests: the render node from `liken`, and the
`media.liken.sh` device of a GPU that supports what the workload needs.
The constraint requires both devices to have the same
`resource.kubernetes.io/pciBusID`, the GPU's PCI address, so both
devices are on the same GPU:

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
            deviceClassName: gpu-render
        - name: decodes
          exactly:
            deviceClassName: decode-10bit
      constraints:
        - requests: [gpu, decodes]
          matchAttribute: resource.kubernetes.io/pciBusID
```

The pod references the claim, and the container receives the render
node of the GPU that the scheduler chose:

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

The `media.liken.sh` device gives the container nothing. It only
describes the GPU, and any number of claims can allocate it at the same
time. The render node can be shared too, so several workloads can decode
on one GPU at the same time.

## 4. Claim a different set of capabilities

For capabilities that `decode-10bit` does not select, add a selector to
the request. A selector narrows the class, so it works with any class
that selects `media.liken.sh` devices. This request needs a GPU that
decodes VP9 and scales 8-bit frames. It uses a class that you write,
here named `media-gpu`, which selects every `media.liken.sh` device
with `device.driver == "media.liken.sh"`. Every device has every
capability attribute, `true` or `false`, so the selector needs no
`has()` check:

```yaml
        - name: decodes
          exactly:
            deviceClassName: media-gpu
            selectors:
              - cel:
                  expression: |
                    device.attributes["media.liken.sh"].decodeVP9 &&
                    device.attributes["media.liken.sh"].scale8bit
```

A selector on a request that uses `decode-10bit` works the same way,
and the device must also have the 10-bit capabilities. To reuse a
selector, put its expression in another class.

## 5. Fall back when no GPU matches

When no GPU matches a claim, the claim is not allocated and its pod
stays `Pending`:

    kubectl describe pod <pod>

The events name the claim that cannot be allocated. The scheduler does
not fall back on its own, so the workload's controller must choose: use
a second claim on any render node with its own slower code path, or use
no claim and decode on the CPU.

## When a GPU reports false for a capability it has

The agent publishes the values that the driver returns through libva,
and does not test them. If the GPU has a capability but the driver in
the agent's image does not list it, the agent publishes `false`. The
agent's log has the driver's name and every value for each GPU:

    kubectl -n liken-system logs ds/media-capabilities

When a query fails, the log has libva's error text, and the device has
every capability `false` and no `vaDriver`.
