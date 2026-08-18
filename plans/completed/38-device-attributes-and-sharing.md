# Device attributes and shared devices

Milestone 38. Completed. liken publishes structural device attributes
in its ResourceSlices, marks a graphics device shareable, and names
undriven hardware in the report.

Milestone 11 gave liken a DRA driver. The machine that exercised it
showed three limits in the published devices. A GPU that multiplexes
could not state this, so two workloads could not share it. A
DeviceClass could select a device by vendor and product and by nothing
else, which is brittle across a fleet. Hardware that a workload can
claim did not reach the person who installs the machine, because the
hardware report is about the boot and the join, not about claims.

This milestone works from one rule: liken publishes the facts about
hardware that no other layer can observe, and it publishes them where
no other layer can add them later. A ResourceSlice is written by its
driver and by nobody else, so a fact that is not in the slice is a fact
that no DeviceClass, no claim, and no workload can supply.

Two of the findings are about work that liken does not do. The next two
sections give the reasons.

## What the node must not enforce

`allowMultipleAllocations` governs how many claims may allocate a
device. It does not govern how many pods may consume one claim, and
nothing does. A ResourceClaim's `reservedFor` holds many consumers by
design, because that is how two pods share one allocation. So two pods
that name the same claim both receive the device, even a device that
must have one writer.

`NodePrepareResources` is the last layer that could refuse this, and it
must still not refuse the second consumer.

* The refusal would be racy. Which pod wins depends on the order the
  kubelet calls in, and a pod rescheduled onto the node can lose to a
  pod that is still terminating. The loser waits in
  `ContainerCreating`, and nobody can correct it.
* Delivery of a device node does not grant exclusive access. The kernel
  enforces exclusion, through `O_EXCL`, tty locks, and the driver's own
  open path. liken cannot make a serial port single-writer when it
  withholds a CDI edit, and it cannot make one safe when it delivers
  one.
* The workload can already state what it means. A
  `ResourceClaimTemplate` makes one claim for each pod, which is "this
  pod, alone". A shared `ResourceClaim` is a deliberate act of sharing.

So the manual gives this rule, and the node does not enforce it.

## What the OS should not measure

"This GPU can encode HEVC" is the question a transcoding deployment
has. To answer it, the OS must open the render node through libva and a
vendor driver. The image holds neither, and both are large. A pod
that holds a claim on that render node can answer the question for
itself, so the fact fails the test above: it is not something only the
OS can observe.

liken publishes the structural facts instead, and the manual states
where the limit is. If a deployment needs codec attributes, build a
workload that probes them and publishes them, not a larger OS image.

## What liken publishes

`hardware.Delivery` already computed each delivered node's subsystem
and dropped everything except the block test. It now keeps the list.
`hardware.Device` keeps the full class code the bus published, not only
the base-class word.

The slice gains three attributes and one flag:

* `renderNode`. The device delivers a DRM render node. This is the
  attribute a transcoder's DeviceClass should select on, in place of a
  PCI ID that is true of one machine.
* `subsystem`. The kind of node a claim delivers, when the nodes
  agree: `drm` for a GPU, `tty` for a serial adapter.
* `classCode`. The whole class code, six hex digits on PCI and two on
  USB. A selector can ask for a VGA controller without liken shipping a
  subclass table it would have to maintain.
* `allowMultipleAllocations`. True for a graphics device: one that
  delivers a DRM render node, and delivers nothing from outside the
  graphics stack.

The shareability rule is narrow on purpose. A DRM render node
multiplexes by the kernel's own contract, and the drill measured twelve
concurrent VAAPI encoders that divided one iGPU evenly, with two pods
at 2.39 and 2.38 times realtime against 4.76 for one pod alone. Every
other device stays exclusive. No other layer can correct a device that
is wrongly marked shareable, and a device that is wrongly marked
exclusive gives a claim that waits.

The first version of this rule tested for DRM nodes and nothing else,
and the lab guest disproved it within the hour. A GPU also delivers the
legacy framebuffer that fbdev emulation creates, so the rule passed its
unit tests and shared nothing on a real machine. The test that matters
is the render node, and the graphics stack is what may accompany it.

List-valued attributes stay out of this. `DRAListTypeAttributes` is
alpha and off, so every attribute is a single value.
`DRAConsumableCapacity`, which `allowMultipleAllocations` needs, is
beta and on in the k3s liken ships.

## What the report names

The report loads storage drivers and network drivers and no others, and
that limit stays: a display driver takes over the screen the report
prints to. The report can still name what it does not load. It now
has a second section for hardware that is present and undriven,
with the modules that would drive it, written as commented lines under
`spec.modules`. The proposal installs with no change, and the person
sees the GPU the machine has.

The same pass corrects a gap that the keyboard showed. `usbhid` binds
by modalias, and `hid_generic` binds over the HID bus, so the alias
table can never name it. A driver that binds to a bus needs a companion
entry, because `modules.alias` cannot describe it.

## The lab drill

The drill gave a guest a `virtio-gpu` and declared `virtio_gpu` in the
Machine's `spec.modules`. The module loaded with no reboot, and the
node published the device with `renderNode` and
`allowMultipleAllocations`. Two independent `ResourceClaim` objects, in
the same DeviceClass, both allocated it, and both pods started with
`/dev/dri/card1` and `/dev/dri/renderD128` inside. Before this
milestone the second pod stayed pending. A claim on a device that is
not shareable still allocates once: the second claim on the guest's IDE
controller stayed `pending`, and its pod stayed `Pending`.

The guest also showed a limit of the report's reach. A virtio GPU's PCI
function is driven by `virtio-pci`, and `virtio_gpu` binds the virtio
device that driver creates, on a bus the walk does not cover. So the
device is not undriven, and no recommendation names the module that
would make it claimable. Real hardware does not have this shape,
because the GPU's own PCI function is the device. If a virtio guest
needs this advice, the walk must reach the virtio bus.

## The manual

The manual has no page for devices. This milestone adds a reference
page for how liken implements DRA, and a guide with worked examples:
how to share an iGPU between deployments, and how to hold a USB dongle
alone.
