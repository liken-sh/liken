# 42, Render node capabilities

Built on 2026-10-02. The capabilities agent, `media-capabilities`,
asks each GPU's VA-API driver what it decodes, encodes, and scales,
and publishes the answer as one `media.liken.sh` device for each
render node. A claim pairs that device with the `liken.sh` render node
of the same GPU through `resource.kubernetes.io/pciBusID`, which
`liken`'s machine operator now publishes. The query ran on a
workstation's Meteor Lake GPU; the result is at the end. The proof on
a fleet is still owed, and
[an open problem](../open-problems/render-node-capabilities-are-unproven-on-a-fleet.md)
holds it.

It follows a failure in library-operator plan 75, where a worker that
decodes every frame of a film landed on a GPU that could decode the
film but could not scale its frames.

## The problem

`liken` publishes each GPU's render node as a DRA device, with the
attributes it can read from the kernel: `driver`, `vendor`, `product`,
`name`, `address`, and `renderNode`. It publishes no fact that needs a
driver stack to measure, such as the codecs a GPU decodes, because the
image carries no libva and no vendor driver. The `liken` device
reference states this, and says that a pod holding a claim on the render
node can measure those facts for itself.

So a workload that needs a capability selects any render node and finds
out at run time. The appearances worker of library-operator selects a
render node by `display-render`, which matches every GPU. On a home
cluster of mixed Intel GPUs, it landed on a Coffee Lake GPU. That GPU
decodes 10-bit HEVC, but its video processor cannot scale 10-bit frames:
`scale_vaapi` fails with "the requested VAProfile is not supported"
before the first frame. The worker now detects the failure and scales on
the CPU, at 4.8 times real time against a faster path on a GPU that
scales 10-bit. An Alder Lake-N GPU on the same cluster reports no AV1
decode in its driver ("No support for codec av1 profile 0"). A claim
could have asked for a GPU that scales 10-bit or decodes AV1, if any
device stated it.

A table of GPU models and their capabilities would answer the question
for the models it lists, and would be wrong for the rest. A capability
also depends on the driver, not only on the silicon: the same GPU gains
a decoder with a newer Mesa or media driver. So the answer has to come
from the GPU and the driver that will run the work.

## The design

media-operator asks each GPU's driver for its media capabilities and
publishes them as devices of its own, in the `media.liken.sh` driver.
A workload pairs one of those devices with the `liken.sh` render node
of the same GPU in one claim.

### Asking the driver

A `DaemonSet`, `media-capabilities`, runs one agent pod on each node
with a GPU. Its claim takes every render node of the node through the
class `media-render`, which selects `renderNode`, with
`allocationMode: All`. The render nodes are shareable, so the hold
takes nothing from other workloads. The image builds on the `vaapi`
base, which carries libva, libva-drm, and the Intel iHD driver.

For each render node, the agent sends libva the queries that `vainfo`
sends, and reads nothing else:

- `vaQueryConfigProfiles` and `vaQueryConfigEntrypoints` give each
  profile and its entrypoints. `VLD` is a decoder, and `EncSlice` and
  `EncSliceLP` are encoders.
- `vaGetConfigAttributes` gives the render-target formats of each
  pair. AV1 has one VA-API profile for 8 and 10 bits, and these
  formats give its bit depths.
- For the video processor, the agent creates a config for
  `VAProfileNone` with `VAEntrypointVideoProc`, and
  `vaQuerySurfaceAttributes` gives the pixel formats the config
  accepts: `NV12` for 8 bits, `P010` for 10 bits.

The agent trusts what the driver states. It publishes exactly what
libva returns, and it does not verify a value, cross-check one against
another, or work around a driver that states a capability it does not
have. A driver that lists a profile it cannot run is a defect in that
driver, and the fix belongs there. A capability that no query states
is not published as `true`.

The agent queries each render node in a child process, with a timeout
of 20 seconds. A VA-API driver is a shared library that runs inside
the process that opens it, so a driver that hangs or crashes ends the
child and not the agent. A failed query publishes the GPU's device
with every capability `false`, and the agent's log has the failure in
the driver's words.

The agent queries when its pod starts, which covers a new image and so
a new driver, and when a render node appears. A GPU's driver does not
change while the pod runs, so no timer queries again. A render node
that `liken` publishes after the scheduler placed the pod is not in
the pod's claim, because a claim keeps its allocation for the life of
the pod. The agent watches its node's `liken.sh` slice, and when the
slice lists a render node the claim does not hold, it deletes its own
pod, and the `DaemonSet`'s next pod claims every render node.

The agent calls libva through purego, which opens a shared library and
calls it from Go with `CGO_ENABLED=0`. It builds in the same Go stage
as every other program in the repository, with no C toolchain and no
libva headers. The binary is not static: it names the dynamic loader,
`libc.so.6`, and the two glibc stubs `libdl.so.2` and
`libpthread.so.0`, which the image copies from the Debian image every
base builds from. Two other ways were weighed. cgo against libva-dev
needs a C toolchain and the headers in the build stage and in the CI
job, and a cgo build in every `go test` of the module. A C helper like
`vainfo` needs a build stage pinned to the bases' Debian snapshot, and
a text format between the helper and the agent.

### Publishing

For each render node, the agent publishes one `media.liken.sh` device
with one boolean attribute for each capability. DRA attributes are
scalars, not lists, so a list of profiles becomes one attribute for
each capability:

    decodeH264: true
    decodeHEVCMain: true
    decodeHEVCMain10: true
    decodeAV1Main: true
    decodeAV1Main10: true
    decodeVP9: true
    encodeH264: true
    encodeHEVCMain: true
    encodeHEVCMain10: true
    scale8bit: true
    scale10bit: true

Every device has all eleven, `true` or `false`. A selector that reads
an absent attribute fails to evaluate, and `false` is a value a claim
can select against. The device also states
`resource.kubernetes.io/pciBusID`, the driver's name in `vaDriver`,
and the GPU's `address`, `driver`, `vendor`, `product`, and `name`, so
a person can read which GPU each device is about. The device delivers
no device node, and it allows any number of allocations. The kubelet
plugin of `media.liken.sh` answers each prepare with no device and no
CDI edit; the kubelet still needs the plugin registered before it
starts a pod whose claim holds such a device.

### The pairing attribute

The design needed an attribute that the `media.liken.sh` device and
the `liken.sh` render node both have, with the same qualified name,
for `matchAttribute`. Kubernetes v1.36, which `liken`'s
`k3s/VERSION` names (v1.36.4+k3s1), allows two answers:

- **A driver may publish an attribute in another domain.** The
  `QualifiedName` documentation in `k8s.io/api/resource/v1` says that
  a name with no domain belongs to the publishing driver, and that
  "attributes or capacities defined by 3rd parties must include the
  domain prefix". The API server's `validateQualifiedName` checks only
  the form of the domain, never whose it is
  ([validation.go at v1.36.4](https://github.com/kubernetes/kubernetes/blob/v1.36.4/pkg/apis/resource/validation/validation.go)).
  The allocator's `lookupAttribute` finds a constraint's name as the
  full key first, and as a bare key only on a device whose driver owns
  the domain
  ([allocator_stable.go at v1.36.4](https://github.com/kubernetes/kubernetes/blob/v1.36.4/staging/src/k8s.io/dynamic-resource-allocation/structured/internal/stable/allocator_stable.go)).
  So `matchAttribute: liken.sh/address` would match a `liken.sh`
  device's bare `address` against a `media.liken.sh` device's full
  `liken.sh/address` key. It would also have the agent write a name
  in a domain that another driver defines.
- **A standard attribute exists.** Kubernetes defines
  `resource.kubernetes.io/pciBusID`, "the PCI bus address of a PCI
  device in extended BDF notation", which "uniquely identifies a PCI
  device on a node"
  ([standard device attributes](https://kubernetes.io/docs/reference/node/dra-standard-device-attributes/),
  and `StandardDeviceAttributePCIBusID` in
  [deviceattribute/attribute.go at v1.36.4](https://github.com/kubernetes/kubernetes/blob/v1.36.4/staging/src/k8s.io/dynamic-resource-allocation/deviceattribute/attribute.go)).
  `resource.kubernetes.io/pcieRoot` is defined too, but it names a
  root complex, which an integrated GPU and a discrete GPU can share,
  so it does not identify one GPU. `liken` published neither.

The build takes the standard attribute. Its domain belongs to no
single driver, which is the same reason `monitor.liken.sh/id` pairs
the display and audio operators' devices. Any driver for the same
card can publish it, so a claim from outside `liken` pairs the same
way. `liken`'s machine operator now publishes
`resource.kubernetes.io/pciBusID` on every device of a PCI device,
with the value of `address`. On a `liken` release before that change,
the constraint matches nothing and the claim does not allocate.

### Claiming

A workload's claim has two requests: the render node from `liken.sh`,
and the `media.liken.sh` device with the capabilities it needs. The
constraint requires the two to share a PCI address, so the scheduler
allocates only a GPU that has both. The machine operator delivers the
render node as it does now, and media-operator delivers nothing:

    requests:
      - name: gpu
        exactly: { deviceClassName: media-render }
      - name: decodes
        exactly: { deviceClassName: media-decode-10bit }
    constraints:
      - requests: [gpu, decodes]
        matchAttribute: resource.kubernetes.io/pciBusID

media-operator ships four `DeviceClass` objects, so that a workload
names a class and not a CEL expression: `media-capabilities` (every
device), `media-decode-10bit` (`decodeHEVCMain10` and `scale10bit`),
`media-decode-av1` (both AV1 depths), and `media-encode` (H.264 and
HEVC at both depths).

### The first users

None of these is wired yet; each is its own change.

- **The appearances worker** of library-operator would replace its
  claim on `display-render` with the pair above, through
  `media-decode-10bit` for a 10-bit film or `media-decode-av1` for an
  AV1 film, and keep a second claim on any render node as its fallback
  when no GPU qualifies. Its CPU scale for a GPU that cannot scale
  stays as the path for that fallback.
- **A Player's playback**, which selects a render node through its
  render class, could ask for a GPU that decodes the codecs of its
  library.
- **A Jellyfin server** on the same fleet, which transcodes, would
  claim `media-encode` beside its render node. This is the first
  workload from outside `liken` that the design serves.

## What was considered and set aside

- **The OS publishes the capabilities.** The image would carry libva and
  every vendor driver, against the rule that the OS carries no driver
  stack. Each driver update would need an OS release.
- **A table of models.** It is wrong for every model it does not list,
  and it cannot follow a driver update.
- **media-operator proxies the render node.** A `media.liken.sh` device
  could deliver the render node itself, and a claim would have one
  request. But two drivers would then deliver one node, and media-operator
  would repeat the machine operator's work of preparing it. The paired
  claim keeps one owner for each node.
- **Each workload probes at run time.** It works, and it is what the
  appearances worker does now as a fallback. But the scheduler cannot
  use a fact that a pod learns after it starts on a node.
- **The agent runs a short test of each capability.** The first draft
  of this plan had the agent decode, encode, and scale a few generated
  frames on each GPU, because a driver can list a profile that fails
  in use. It was set aside before the build. An operator that
  publishes facts about a device reads what the device's driver
  states, and does not do the media work itself: a test decodes and
  scales on a GPU that other workloads share, needs ffmpeg and test
  media in the image, and can hang where a query returns at once. A
  driver that lists what it cannot run is wrong in that driver, and a
  test would only hide the defect from the people who can fix it.

## Not covered

- **AMD.** Mesa's `radeonsi` VA-API driver answers the same queries,
  but the `vaapi` base carries only the Intel iHD driver, so an AMD
  GPU's query fails and publishes every capability `false`. Adding
  `mesa-va-drivers` to `vaapi` is a new revision of that base and of
  each pinned base that builds on it.
- **NVIDIA.** NVDEC and NVENC are not VA-API, and wait for `liken` plan
  34's GPU add-ons.
- **Capabilities no query states.** Every capability in the first set
  has a query that states it. A fact such as "decodes a 4K frame in
  real time" has none, and is not published.

## Measured

On 2026-10-02, on a workstation with a Meteor Lake-P GPU
(`8086:7d55`), the query read the same eleven `true` values from two
drivers: the workstation's own iHD 26.1.2, through the binary built on
the host, and Debian trixie's iHD 25.2.3, through the
`media-operator-capabilities` image with the render node passed in.
The video processor accepted 26 surface formats, `NV12` and `P010`
among them. `scale_vaapi` scaled `p010le` frames on the same GPU with
no error, so the 10-bit query agrees with the operation. One query
took 20 ms and peaked at 27 MB of resident memory.

## The proof

On the lab, the agent publishes a `media.liken.sh` device for each
GPU. A claim through `media-decode-10bit` schedules only on a GPU
whose driver states `scale10bit`, and receives that GPU's render node.
The open problem holds this proof.
