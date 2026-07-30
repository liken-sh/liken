# Sharing integrated graphics

Milestone 49 — Proposed. It would let a real integrated GPU publish as
shareable, so a second claim allocates hardware that the lab measured
serving twelve encoders at once.

## What already runs

`inventoryDevices` in `machine-operator/dra.go` publishes one slice
device for each device it offers to workloads, and `shareable` sets
`allowMultipleAllocations` on that device. The rule has two parts. The
device must deliver a DRM render node, and every node it delivers must
come from a graphics subsystem. `graphicsSubsystems` holds two names:
`drm` for the modern interface, and `graphics` for the framebuffer that
the kernel's fbdev emulation creates for the same hardware.

Milestone 38 made the rule narrow on purpose, and the reason still
holds. Only the driver writes a slice, so no DeviceClass, claim, or
workload can correct a device that is wrongly marked shareable. A
device that is wrongly marked exclusive costs a claim that waits, where
a person can see it waiting.

The same node list feeds two other places. `soleSubsystem` publishes
the `subsystem` attribute only when every delivered node is of one
kind. `prepareClaim` in `machine-operator/draplugin.go` writes one CDI
device node for every path in `hardware.Delivery.DevNodes`, so an
allocated claim receives the whole list.

## What the fleet measured

i915 registers an `i2c-dev` node for each display output, which is the
channel that carries DDC and AUX. The Alder Lake-N hardware the DRA
guide describes has nine of them. `InspectDelivery` in
`hardware/delivery.go` walks the GPU's sysfs subtree and stops only at
a nested bus device, so those nodes are inside the walk. They join
`Delivery.DevNodes`, and `i2c-dev` joins `Delivery.Subsystems`.
`graphicsSubsystems` does not hold `i2c-dev`, so `shareable` returns
false for every integrated GPU liken has met.

The failed subsystem test has four results.

* **The device publishes with no `allowMultipleAllocations`.** One
  claim allocates the GPU, and the second claim waits against hardware
  the lab measured running twelve concurrent VAAPI encoders, with two
  pods at 2.39 and 2.38 times realtime against 4.76 for one pod alone.
* **The device publishes with no `subsystem` attribute.** The delivery
  names both `drm` and `i2c-dev`, so `soleSubsystem` returns the empty
  string and the attribute is omitted. A DeviceClass that selects
  `subsystem == "drm"` never matches a real GPU.
* **Nothing reports the wait.** The claim stays pending and the pod
  stays pending. No condition on the Machine, and no field in the
  slice, says that the hardware could serve both.
* **A claim that does allocate delivers the i2c nodes.** A workload
  that asked for a GPU receives nine monitor-control buses inside its
  container, and an `i2c-dev` node passes a raw transfer to every
  device on its bus.

This is the second time real hardware showed a delivery list wider than
the rule. The first version of the rule tested for DRM nodes alone, and
the lab guest disproved it within the hour, because fbdev emulation
adds a `graphics` node to the same device. `graphicsSubsystems` is the
answer to that finding, and the i2c nodes are the next one.

## The candidate fixes

### Widen `graphicsSubsystems`

Add `i2c-dev` to the map. This is the smallest change, and it publishes
the flag on the measured hardware immediately.

The trade is what the map then means. `i2c-dev` is a general bus
interface, not part of the graphics stack, so the map stops naming a
stack and starts naming what one GPU happens to deliver. A non-graphics
device that delivers a render node beside an i2c bus would pass the
rule as well. The next node kind that a GPU driver registers needs
another entry, and nothing marks when that entry is missing except a
claim that waits. This fix also leaves the `subsystem` attribute
absent and leaves the i2c nodes in the container.

### Test only the render node

Drop the loop, so `shareable` returns what `hasRenderNode` returns. The
render node is the kernel interface that makes sharing safe.

The trade is the guard that milestone 38 bought with the loop. A device
that delivers a render node beside a node from hardware liken has not
met would publish as shareable, and no later layer can take that back.
The trade is smaller when the delivery is selective, because then the
unexamined node never reaches a workload. That makes this fix and the
next one a single design rather than two.

### Deliver only the nodes the claim selected

Change the prepare half instead of the rule. `prepareClaim` writes a
CDI device node for each path in the delivery. It could write only the
nodes of the kind the claim asked for, for example the `/dev/dri`
nodes for a request that selected on `renderNode`. `shareable` then
tests the nodes liken would deliver, not the nodes the subtree
contains, and both the flag and the `subsystem` attribute describe the
same set.

The trade is that DRA has no such field. A request selects a device,
and nothing in it names a node kind, so liken must derive the set: from
the DeviceClass selectors that matched, or from a table that maps a
device kind to the subsystems it delivers. This change touches the
rule, the prepare half, and the derivation between them, so it is the
largest of the three. It is also the only one that keeps the monitor-control
buses out of a container that asked for a GPU.

## What a drill must show

A drill runs against the measured integrated GPU. Two independent `ResourceClaim`
objects in one DeviceClass must both allocate the integrated GPU, and
both pods must encode at the same time. `ls /dev/dri` and `ls
/dev/i2c-*` inside a pod state what the chosen fix delivers, and the
answer must match what the milestone says it delivers.

The guest drill from milestone 38 cannot show this. A `virtio-gpu`
delivers `drm` nodes and no i2c nodes, so it passes the current rule
and would pass every candidate above.

## The manual

The rule is written down in two pages, and both name this hardware.
`docs/content/docs/reference/devices.md` states that liken publishes
`allowMultipleAllocations` for a graphics device only.
`docs/content/docs/guides/devices.md` shows a slice entry for
`Alder Lake-N [UHD Graphics]` with `allowMultipleAllocations: true` and
`subsystem: drm`, and the worked example shares an iGPU between two
deployments. That entry is not what the measured hardware publishes
today, so the fix changes the pages and the code together.
