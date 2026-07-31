# Sharing integrated graphics

Milestone 49 — Completed. It lets a real integrated GPU publish as
shareable, so a second claim allocates hardware that the lab measured
serving twelve encoders at once. The design below groups a device's
delivery by kernel subsystem and publishes each group as its own
slice device. Releases 2026.07.31-003 and -004 carry it; the second
adds the DisplayPort AUX channel, the companion kind that only a
machine with a connected DP display delivers.

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

## The design

### Group a delivery by subsystem

`InspectDelivery` keeps its walk, but the result gains structure: the
device nodes group by the kernel subsystem they belong to. One grouping
function turns one physical device into one or more published devices,
and each published device carries exactly one subsystem's nodes. The
inventory half and the prepare half both call this function, so the
slice and the CDI spec can never disagree about what a name delivers.

The plan considered three narrower fixes: widening
`graphicsSubsystems` with `i2c-dev`, testing only the render node, and
deriving the wanted node kind from the DeviceClass selectors that
matched a claim. The first leaves the monitor buses in the container
and the `subsystem` attribute absent. The second gives up the guard
against hardware liken has not met. The third founders on the API: a
request selects a device and names no node kind, so nothing in a claim
can carry the answer. Publishing one slice device per subsystem
dissolves that problem, because the allocated device's own name states
which nodes the claim receives.

### The policy table

The mechanism is generic; the policy stays explicit. A device whose
delivery holds a render node is a graphics device. Its primary
published device carries only the `drm` nodes, and its `i2c-dev` nodes
publish as a secondary device. A device whose delivery is all one
subsystem publishes exactly as before. A mixed delivery the table does
not know publishes as before: whole, exclusive, and with no
`subsystem` attribute, which keeps the milestone 38 default for
hardware nobody has examined.

A graphics device's framebuffer node stops being delivered. The fbdev
node is the kernel's legacy console interface, holding it grants
display takeover, and no workload claims a bare framebuffer.

### Names and attributes

The primary device keeps its bare name, so an existing allocation
against `pci-0000-00-02-0` stays valid. A secondary device appends its
subsystem to that name: `pci-0000-00-02-0-i2c-dev`. Every published
device carries the parent's identifying attributes plus its own
`subsystem`, which now always publishes, because each published device
holds one kind by construction. Only the primary graphics device
carries `renderNode` and `allowMultipleAllocations`. The i2c device
stays exclusive: two raw writers on one wire have no arbitration
contract.

The split forces one migration. A deployed DeviceClass that selects
only on `driver == "i915"` now matches both published devices, so a
fresh claim could allocate the monitor buses instead of the GPU.
Tighten such a class to `renderNode` or to `subsystem == "drm"` before
the fleet upgrades. An allocation that already exists is sticky, so a
running workload keeps its device either way.

### The prepare half

`prepareClaim` maps an allocated name back mechanically: the bare name
delivers the primary set, and a name with a subsystem suffix delivers
that subsystem's set. No selector inspection exists anywhere. A name
that maps to nothing fails the claim, the same as a missing device.

## What a drill must show

A drill runs against the measured integrated GPU. First the deployed
DeviceClass tightens to select the render node. Then two independent
`ResourceClaim` objects in that DeviceClass must both allocate the
integrated GPU, and both pods must encode at the same time. `ls
/dev/dri` and `ls /dev/i2c-*` inside a pod must show the dri nodes and
nothing else. A third claim, through a DeviceClass that selects
`subsystem == "i2c-dev"`, must receive the nine monitor buses and no
dri node.

The guest drill from milestone 38 cannot decide this milestone. A
`virtio-gpu` delivers `drm` nodes and no i2c nodes, so it passes the
old rule and the new one. It still serves as the regression check: the
guest's GPU must keep publishing as shareable, now without its
framebuffer node in the delivery.

The drill ran on 2026-07-31 against the measured hardware and passed
every check. The display on the machine's DP-1 first disproved the
-003 table: a `drm_dp_aux_dev` node routed the whole GPU to the
unknown-mix default, the third time real hardware showed a delivery
wider than the rule. With the AUX channel in the companion table, the
iGPU published as three devices. Two independent claims allocated the
graphics device at once, and both pods encoded 1080p30 at about 4.0x
realtime each through `h264_vaapi`. The GPU claim received the two
dri nodes alone. The companion claims received the ten monitor buses
and the AUX node, each without a dri node.

## The manual

The rule is written down in two pages, and both name this hardware.
`docs/content/docs/reference/devices.md` states that liken publishes
`allowMultipleAllocations` for a graphics device only, and defines the
graphics stack as the DRM nodes and the legacy framebuffer with them.
`docs/content/docs/guides/devices.md` explains that the measured iGPU
does not share, because of its i2c monitor-control nodes. The fix
rewrites both: the reference describes the per-subsystem split and the
delivery each published device makes, and the guide's example shows
the two published devices, the tightened selector, and a shared iGPU.
