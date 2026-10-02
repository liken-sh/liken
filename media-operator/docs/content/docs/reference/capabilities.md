---
title: Render node capabilities
weight: 68
---

# Render node capabilities

The capabilities agent publishes one `media.liken.sh` device for
each GPU render node that `liken` publishes. The device states what
the GPU's VA-API media driver supports: which codecs it decodes and
encodes, and which bit depths its video processor scales. A claim
pairs the device with the `liken.sh` render node of the same GPU, so
the scheduler allocates a render node only on a GPU whose driver
states what the claim asks for.
[Claim a GPU by what it decodes](/docs/guides/claim-a-gpu-by-capability/)
gives the claim.

## Where the values come from

The agent asks the driver, and runs no media work. For each render
node, it opens the node with libva, the VA-API library, and sends the
same queries that `vainfo` sends:

* `vaQueryConfigProfiles` and `vaQueryConfigEntrypoints` give each
  profile the driver lists, and the entrypoints of each one. The `VLD`
  entrypoint is a decoder, and `EncSlice` and `EncSliceLP` are
  encoders.
* `vaGetConfigAttributes` gives the render-target formats of each
  profile and entrypoint. AV1 has one VA-API profile for 8 and 10
  bits, and these formats give its bit depths.
* For the video processor, the agent creates a config for
  `VAProfileNone` with `VAEntrypointVideoProc`, and
  `vaQuerySurfaceAttributes` gives the pixel formats that config
  accepts. `NV12` is the 8-bit 4:2:0 format, and `P010` the 10-bit
  one.

Each value is the driver's own statement, as libva returns it. The
agent does not test a value or correct it. A driver that lists a
profile it cannot run publishes that profile as `true`, and the fault
is in that driver. A capability the driver does not list publishes
as `false`.

The agent queries each render node once, when its pod starts or when
`liken` publishes a new render node. A new image of the agent, and so
a new driver, starts a new pod. No timer queries again.

## Attributes

Each capability is a boolean. Every device has all eleven, `true` or
`false`, so a selector can read any of them with no `has()` check. A
selector reads one as
`device.attributes["media.liken.sh"].decodeHEVCMain10`.

| Attribute | `true` when the driver lists |
|---|---|
| `decodeH264` | `VAProfileH264High` with `VLD`. Nearly every H.264 file uses the High profile |
| `decodeHEVCMain` | `VAProfileHEVCMain` with `VLD` |
| `decodeHEVCMain10` | `VAProfileHEVCMain10` with `VLD` |
| `decodeAV1Main` | `VAProfileAV1Profile0` with `VLD` and the render-target format `YUV420` |
| `decodeAV1Main10` | `VAProfileAV1Profile0` with `VLD` and the render-target format `YUV420_10` |
| `decodeVP9` | `VAProfileVP9Profile0` with `VLD` |
| `encodeH264` | `VAProfileH264High` with `EncSlice` or `EncSliceLP` |
| `encodeHEVCMain` | `VAProfileHEVCMain` with `EncSlice` or `EncSliceLP` |
| `encodeHEVCMain10` | `VAProfileHEVCMain10` with `EncSlice` or `EncSliceLP` |
| `scale8bit` | a video processor that accepts `NV12` surfaces |
| `scale10bit` | a video processor that accepts `P010` surfaces |

The device also has these attributes:

| Attribute | Type | What it is |
|---|---|---|
| `resource.kubernetes.io/pciBusID` | string | the GPU's PCI address, such as `0000:00:02.0`. The `liken.sh` render node of the same GPU has the same value, and a claim pairs the two with `matchAttribute` on this name |
| `vaDriver` | string | the driver's own name and version, from `vaQueryVendorString`, cut to 64 characters. It is absent when the query failed |
| `address`, `driver`, `vendor`, `product`, `name` | string | the same values as the `liken.sh` render node, so `kubectl get resourceslice` shows which GPU each device is about |

The device's name is the name of the `liken.sh` render node, such as
`pci-0000-00-02-0`. It is in a `ResourceSlice` named
`<node>-media.liken.sh`.

The device delivers no device node, and any number of claims can
allocate it at once. The render node comes from `liken`, through the
other request of the claim.

## The pairing attribute

A bare attribute name belongs to the driver that published it, so
`liken.sh/address` and `media.liken.sh/address` are two different
names, and `matchAttribute` cannot match one against the other.
`resource.kubernetes.io/pciBusID` is a
[standard attribute](https://kubernetes.io/docs/reference/node/dra-standard-device-attributes/)
that Kubernetes defines for a PCI device's address. Its domain
belongs to no single driver, and `liken` and this agent both publish
it with the GPU's PCI address. So a constraint on it pairs the two
drivers' devices of one GPU.

`liken` publishes `resource.kubernetes.io/pciBusID` from the release
that ships with this agent. On an older release, the `liken.sh`
render node has no such attribute, and a claim with the constraint
never allocates.

## When a query fails

A query that fails, or that runs longer than 20 seconds, publishes
the GPU's device with every capability `false` and no `vaDriver`.
The agent's log has one line for each render node, with the result or
libva's error text:

    kubectl -n liken-system logs ds/media-capabilities
    capabilities: pci-0000-00-02-0 (/dev/dri/renderD128, Intel iHD driver for Intel(R) Gen Graphics - 25.2.3 ()): decodeH264=true ...

A GPU whose kernel driver has no VA-API driver in the agent's image,
such as an NVIDIA GPU, fails its query this way. The image carries
the Intel iHD driver.

## Device classes

`media-operator` ships no class that selects `media.liken.sh`
devices. A class encodes a deployment's purposes, so the cluster owner
writes the classes that a workload claims through.
[Claim a GPU by what it decodes](/docs/guides/claim-a-gpu-by-capability/)
gives example classes.

The base ships one class, `media-render`, because the agent's own
claim names it. It selects every `liken.sh` render node.
