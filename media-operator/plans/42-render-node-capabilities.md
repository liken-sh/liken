# 42, Render node capabilities

Designed, not built. It follows a failure in library-operator plan 75,
where a worker that decodes every frame of a film landed on a GPU that
could decode the film but could not scale its frames.

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
scales 10-bit. A claim could have asked for a GPU that scales 10-bit, if
any device stated it.

A table of GPU models and their capabilities would answer the question
for the models it lists, and would be wrong for the rest. A capability
also depends on the driver, not only on the silicon: the same GPU gains
a decoder with a newer Mesa or media driver. So the answer has to come
from the GPU and the driver that will run the work, measured.

## The design

media-operator measures each GPU's media capabilities and publishes them
as devices of its own, in the `media.liken.sh` driver. A workload pairs
one of those devices with the `liken.sh` render node of the same GPU in
one claim.

### Measuring

A node agent of media-operator holds a shareable claim on every render
node of its node, through a class that selects `renderNode`. Its image
carries libva, the Intel and AMD media drivers, and ffmpeg, which the
player image already carries for playback. For each render node, it
records two kinds of fact:

- **What the driver states.** VA-API lists each profile and entrypoint
  it supports: decode, encode, and video processing, for each codec and
  bit depth. `vainfo` prints the same list.
- **What a test shows.** A driver can list a profile that fails in use.
  The agent runs a short test for each capability that a workload
  depends on, such as one 10-bit frame through `scale_vaapi`. The test
  is the operation, on the GPU, with the driver that the workload will
  use.

The agent measures again when its pod starts, which covers a new image
and so a new driver, and when a render node appears. A GPU does not
change while it runs, so no timer measures again.

### Publishing

For each render node, the agent publishes one `media.liken.sh` device,
with one boolean attribute for each capability. DRA attributes are
scalars, not lists, so a list of profiles becomes one attribute for
each profile:

    decodeH264: true
    decodeHEVCMain10: true
    decodeAV1: false
    encodeH264: true
    encodeHEVCMain10: false
    scale10bit: false

The device also states the GPU's `address`, so a claim can pair it with
the render node of the same GPU. The device delivers no device node. It
is a statement about the hardware, and it allows any number of claims to
allocate it at once.

### Claiming

A workload's claim has two requests: the render node from `liken.sh`,
and the `media.liken.sh` device with the capabilities it needs. A
constraint requires the two to share an address, so the scheduler
allocates only a GPU that has both. The machine operator delivers the
render node as it does now, and media-operator delivers nothing:

    requests:
      - name: gpu
        exactly: { deviceClassName: display-render }
      - name: decodes
        exactly:
          deviceClassName: media-decode
          selectors:
            - cel:
                expression: device.attributes["media.liken.sh"].scale10bit
    constraints:
      - requests: [gpu, decodes]
        matchAttribute: liken.sh/address

media-operator ships `DeviceClass` objects for the common needs, such as
`media-decode-10bit`, so that a workload names a class and not a CEL
expression.

### The first users

- **The appearances worker** of library-operator asks for a GPU that
  decodes the film's codec and scales its bit depth, and falls back to
  any render node when no GPU has both.
- **A Player's playback**, which selects a render node through its
  render class, and could ask for a GPU that decodes the codecs of its
  library.
- **A Jellyfin server** on the same fleet, which transcodes. Its claim
  would ask for the encoders it uses. This is the first workload from
  outside `liken` that the design serves.

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

## Open questions

- **The pairing attribute.** A `media.liken.sh` device needs an
  attribute that the `liken.sh` render node also has, with the same
  name, for `matchAttribute`. Whether one driver may publish an
  attribute in another driver's domain, such as `liken.sh/address`, is
  not checked. The alternative is a standard attribute that both
  drivers publish, such as `resource.kubernetes.io/pciBusID`, if the
  Kubernetes version that `liken` runs defines one. This must be
  settled before the build.
- **Which capabilities.** The VA-API list is long. The first set is what
  the users above need: decode and encode of H.264, HEVC Main and Main
  10, AV1, and VP9, and the video processor's 8-bit and 10-bit scale.
- **AMD.** The same VA-API list works through Mesa's radeonsi driver.
  NVIDIA needs NVDEC and NVENC, which VA-API does not cover, and waits
  for `liken` plan 34's GPU add-ons.
- **A test that hangs.** A broken driver can hang a test. Each test
  runs with a timeout, and a test that times out publishes the
  capability as false with a condition that names it.

## The proof

On the lab, the agent publishes a `media.liken.sh` device for each GPU.
A GPU that fails the 10-bit scale test states `scale10bit: false`, and a
GPU that passes states `true`. A claim that asks for `scale10bit`
schedules only on a node with a GPU that passes, and receives that GPU's
render node. An appearances worker with that claim decodes a 10-bit film
with no fallback.
