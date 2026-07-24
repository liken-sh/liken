# Devices that say what they are

Milestone 38 — Done

Milestone 11 gave liken a DRA driver, and the machine that finally
exercised it asked for three things the published devices could not
say. A GPU that multiplexes could not say so, so two workloads could
not share it. A DeviceClass could select a device by vendor and product
and by nothing else, which is a brittle question to ask a fleet. And
hardware a workload could claim never reached the person installing the
machine, because the hardware report is about booting and joining, not
about claiming.

The line this milestone works from: **liken publishes the facts about
hardware that nothing else can know, and it publishes them where no
other layer could add them later.** A ResourceSlice is written by its
driver and by nobody else, so a fact missing from the slice is a fact
that no DeviceClass, no claim, and no workload can supply. That is what
makes these liken's to state.

Two of the findings behind this milestone are not liken's, and the
reasoning matters as much as the code.

## What the node must not enforce

`allowMultipleAllocations` governs how many claims may allocate a
device. It does not govern how many pods may consume one claim, and
nothing does: a ResourceClaim's `reservedFor` holds many consumers by
design, because that is how two pods share one allocation. So two pods
that name the same claim both receive the device, even a device that
must have one writer.

`NodePrepareResources` is the last layer that sees this, and it must
still not refuse the second consumer.

* The refusal would be racy. Which pod wins depends on the order the
  kubelet calls, and a pod rescheduled onto the node can lose to a pod
  that is still terminating. The loser waits in `ContainerCreating`
  with nobody able to fix it.
* Handing over a device node is not granting exclusive access. The
  kernel enforces exclusion, through `O_EXCL`, tty locks, and the
  driver's own open path. liken cannot make a serial port
  single-writer by withholding a CDI edit, and it cannot make one safe
  by delivering it.
* The workload can already say what it means. A `ResourceClaimTemplate`
  mints one claim per pod, which is exactly "this pod, alone". A shared
  `ResourceClaim` is a deliberate act of sharing.

So the manual teaches it, and the node does not police it.

## What the OS should not measure

"This GPU can encode HEVC" is the question a transcoding deployment
actually has. Answering it means opening the render node through
libva and a vendor driver, neither of which the image carries, and
both of which are large. A pod holding a claim on that render node
can answer it for itself, so the fact fails the test above: it is not
something only the OS can know.

liken publishes the structural facts instead, and the manual says
where the line is. If a deployment needs codec attributes, the thing to
build is a workload that probes and publishes them, not a bigger OS
image.

## What liken publishes

`hardware.Delivery` already computed each delivered node's subsystem
and dropped everything except the block test. It now keeps the list.
`hardware.Device` keeps the full class code the bus published, not only
the base-class word.

The slice gains three attributes and one flag:

* `renderNode` — the device delivers a DRM render node. This is the
  attribute a transcoder's DeviceClass should select on, in place of a
  PCI ID that is true of one machine.
* `subsystem` — the kind of node a claim delivers, when the nodes
  agree: `drm` for a GPU, `tty` for a serial adapter.
* `classCode` — the whole class code, six hex digits on PCI and two on
  USB. A selector can ask for a VGA controller without liken shipping a
  subclass table it would have to maintain.
* `allowMultipleAllocations` — true for a graphics device: one that
  delivers a DRM render node, and delivers nothing from outside the
  graphics stack.

The shareability rule is deliberately narrow. A DRM render node
multiplexes by the kernel's own contract, and the lab measured twelve
concurrent VAAPI encoders dividing one iGPU evenly, two pods reaching
2.39 and 2.38 times realtime against 4.76 for one pod alone. Every
other device stays exclusive, because a device wrongly marked shareable
cannot be corrected by any other layer, while a device wrongly marked
exclusive is a claim that waits.

The first rule written here tested for DRM nodes and nothing else, and
the lab guest disproved it within the hour: a GPU also delivers the
legacy framebuffer that fbdev emulation creates, so the rule would have
passed its unit tests and shared nothing on a real machine. The test
that matters is the render node, and the graphics stack is what may
accompany it.

List-valued attributes stay out of this: `DRAListTypeAttributes` is
alpha and off, so every attribute is a single value.
`DRAConsumableCapacity`, which `allowMultipleAllocations` needs, is beta
and on in the k3s liken ships.

## What the report names

The report loads storage and network drivers and no others, and that
limit stays: a display driver takes over the screen the report is
printing to. But the report can name what it will not load. It now
carries a second section for hardware that is present and undriven,
with the modules that would drive it, written as commented lines under
`spec.modules`. The proposal installs unchanged, and the person sees
the GPU that is the reason they bought the machine.

The same pass fixes a blind spot the keyboard found. `usbhid` binds by
modalias, and `hid_generic` binds over the HID bus, so the alias table
can never name it. A driver that binds to a bus needs a companion
entry, because `modules.alias` cannot describe it.

## What the lab proved

The drill gave a guest a `virtio-gpu` and declared `virtio_gpu` in the
Machine's `spec.modules`. The module loaded without a reboot, and the
node published the device with `renderNode` and
`allowMultipleAllocations`. Two independent `ResourceClaim` objects,
in the same DeviceClass, both allocated it, and both pods started with
`/dev/dri/card1` and `/dev/dri/renderD128` inside. That is the case
that left the second pod pending before this milestone. A claim on a
device that is not shareable still allocates once: the second claim on
the guest's IDE controller stayed `pending`, and its pod stayed
`Pending`.

The guest also showed a limit of the report's reach that is worth
recording. A virtio GPU's PCI function is driven by `virtio-pci`, and
`virtio_gpu` binds the virtio device that driver creates, on a bus the
walk does not cover. So the device is not undriven, and no
recommendation names the module that would make it claimable. Real
hardware does not have this shape, because the GPU's own PCI function
is the device. If a virtio guest ever needs the advice, the walk has
to reach the virtio bus.

## The manual

The device story has never been in the manual. This milestone adds a
reference page for how liken implements DRA, and a guide with worked
examples: sharing an iGPU between deployments, and holding a USB
dongle alone.
