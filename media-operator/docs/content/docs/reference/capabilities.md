---
title: Render node capabilities
weight: 68
---

# Render node capabilities

The capabilities agent publishes one `media.liken.sh` device for each
GPU render node that `liken` publishes. The device lists what the GPU's
VA-API media driver supports: the codecs it decodes and encodes, and the
bit depths its video processor scales. A claim requests this device
together with the `liken.sh` render node of the same GPU, so the
scheduler allocates a render node only on a GPU whose driver supports
what the claim needs. [Claim a GPU by what it
decodes](/docs/guides/claim-a-gpu-by-capability/) shows the claim.

## Where the values come from

The agent queries the driver and does no media work. For each render
node, it opens the node with libva, the VA-API library, and sends the
same queries that `vainfo` sends:

* `vaQueryConfigProfiles` and `vaQueryConfigEntrypoints` return the
  profiles that the driver lists and the entrypoints of each profile.
  The `VLD` entrypoint is a decoder, and `EncSlice` and `EncSliceLP`
  are encoders.
* `vaGetConfigAttributes` returns the render-target formats of each
  profile and entrypoint. AV1 has one VA-API profile for 8-bit and
  10-bit video, so these formats give its bit depths.
* For the video processor, the agent creates a config for
  `VAProfileNone` with `VAEntrypointVideoProc`.
  `vaQuerySurfaceAttributes` then returns the pixel formats that the
  config accepts. `NV12` is the 8-bit 4:2:0 format and `P010` is the
  10-bit format.

The agent publishes each value as libva returns it, and does not test
it. If a driver lists a profile that it cannot run, the agent publishes
that profile as `true`, and the fault is in the driver. A capability
that the driver does not list is published as `false`.

The agent queries a render node when its pod starts, and when `liken`
publishes a new render node. It does not query again on a timer. A new
agent image can bring a new driver, and its pod queries every render
node when it starts.

## Attributes

Each capability is a boolean. Every device has every capability
attribute, `true` or `false`, so a selector can read any of them
without a `has()` check, as in
`device.attributes["media.liken.sh"].decodeHEVCMain10`.

| Attribute | `true` when the driver lists |
|---|---|
| `decodeH264` | `VAProfileH264High` with `VLD`. Almost every H.264 file uses the High profile |
| `decodeHEVCMain` | `VAProfileHEVCMain` with `VLD` |
| `decodeHEVCMain10` | `VAProfileHEVCMain10` with `VLD` |
| `decodeAV1Main` | `VAProfileAV1Profile0` with `VLD` and the render-target format `YUV420` |
| `decodeAV1Main10` | `VAProfileAV1Profile0` with `VLD` and the render-target format `YUV420_10` |
| `decodeVP9` | `VAProfileVP9Profile0` with `VLD` |
| `decodeVP9Profile2` | `VAProfileVP9Profile2` with `VLD`. Profile 2 is 10-bit VP9 |
| `decodeVP8` | `VAProfileVP8Version0_3` with `VLD` |
| `decodeVC1` | `VAProfileVC1Advanced` with `VLD`. VC-1 on Blu-ray uses the Advanced profile |
| `decodeMPEG2` | `VAProfileMPEG2Main` with `VLD` |
| `encodeH264` | `VAProfileH264High` with `EncSlice`, the full slice encoder |
| `encodeHEVCMain` | `VAProfileHEVCMain` with `EncSlice` |
| `encodeHEVCMain10` | `VAProfileHEVCMain10` with `EncSlice` |
| `encodeH264LowPower` | `VAProfileH264High` with `EncSliceLP`, the low-power encoder |
| `encodeHEVCMainLowPower` | `VAProfileHEVCMain` with `EncSliceLP` |
| `encodeHEVCMain10LowPower` | `VAProfileHEVCMain10` with `EncSliceLP` |
| `scale8bit` | a video processor that accepts `NV12` surfaces |
| `scale10bit` | a video processor that accepts `P010` surfaces |

The full encoder and the low-power encoder are separate capabilities,
because a driver can list one without the other, and a transcoder
selects between them in its own settings. A transcoder with its
low-power encoders turned off needs `encodeH264`, not
`encodeH264LowPower`.

The device also has these attributes:

| Attribute | Type | What it is |
|---|---|---|
| `resource.kubernetes.io/pciBusID` | string | the GPU's PCI address, such as `0000:00:02.0`. The `liken.sh` render node of the same GPU has the same value, so a claim can pair the two with `matchAttribute` on this name |
| `vaDriver` | string | the driver's name and version, from `vaQueryVendorString`, truncated to 64 characters. It is absent when the query failed |
| `address`, `driver`, `vendor`, `product`, `name` | string | the same values as the `liken.sh` render node, so `kubectl get resourceslice` shows which GPU each device describes |

The device has the same name as the `liken.sh` render node, such as
`pci-0000-00-02-0`. It is in a `ResourceSlice` named
`<node>-media.liken.sh`.

The device gives a container no device node, and any number of claims
can allocate it at the same time. The container gets its render node
from `liken`, through the other request of the claim.

## The pairing attribute

An attribute name without a domain belongs to the driver that
published it, so `liken.sh/address` and `media.liken.sh/address` are
different attributes, and `matchAttribute` cannot match one against the
other. `resource.kubernetes.io/pciBusID` is a
[standard attribute](https://kubernetes.io/docs/reference/node/dra-standard-device-attributes/)
that Kubernetes defines for the address of a PCI device. It belongs to
no single driver. `liken` and this agent both publish it with the GPU's
PCI address, so a constraint on it pairs the devices of one GPU from
the two drivers.

A `liken` release older than this agent does not publish
`resource.kubernetes.io/pciBusID` on its render nodes, and a claim with
this constraint is then never allocated.

## When a query fails

When a query fails, or runs longer than 20 seconds, the agent publishes
the GPU's device with every capability `false` and no `vaDriver`. The
agent's log has one line for each render node, with the result or
libva's error text:

    kubectl -n liken-system logs ds/media-capabilities
    capabilities: pci-0000-00-02-0 (/dev/dri/renderD128, Intel iHD driver for Intel(R) Gen Graphics - 25.2.3 ()): decodeH264=true ...

A GPU whose kernel driver has no VA-API driver in the agent's image,
such as an NVIDIA GPU, fails its query this way. The image has two
VA-API drivers: the Intel iHD driver for the `i915` and `xe` kernel
drivers, and Mesa's `radeonsi` driver for `amdgpu`.

The image has Debian's non-free build of the iHD driver, because the
free build lists no AV1 decoder, no VC-1 decoder, and no full H.264 or
HEVC encoder on a Gen12 GPU such as Tiger Lake or Alder Lake, even
though the hardware supports them.

## Device classes

`media-operator` does not ship a class that selects `media.liken.sh`
devices. Each cluster needs different classes for its workloads, so you
write them. [Claim a GPU by what it
decodes](/docs/guides/claim-a-gpu-by-capability/) has example classes.

The base ships one class, `media-render`, for the agent's own claim. It
selects every `liken.sh` render node.
