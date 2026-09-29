# Device management

Milestone 11. Completed. The OS reports hardware that no driver
claims, and a DRA driver delivers devices to unprivileged pods.

The milestone has two halves. The OS half covers unclaimed-device
reporting and live module loads. The DRA driver half covers the device
inventory, the kubelet plugin, and CDI delivery to an unprivileged pod.

liken has no shell and no udev. devtmpfs creates the device nodes, but
a USB device that arrives after boot also needs the OS to handle kernel
uevents and to load modules. On other Linux systems, udev does that
job. The Kubernetes half answers two more questions: how workloads
reach the hardware, and whether devices belong in `status.hardware`
next to CPUs and memory.

## What udev does

The kernel enumerates hardware, binds drivers to devices, and, since
devtmpfs, creates the `/dev` nodes itself. Firmware blobs load from
`/lib/firmware` without userspace. Four jobs remain for udev, and all
four are policy:

1. **Loading modules.** When a device appears and no resident driver
   claims it, the kernel sends a uevent that carries a MODALIAS
   fingerprint for the orphan device, then waits. udev matches that
   fingerprint against `modules.alias` and loads the matching module.
   This is udev's only job with no fallback inside the kernel.
2. **Stable names and symlinks** (`/dev/disk/by-uuid`, `enp3s0`).
   liken does not need this job: storage identity uses GPT partition
   names probed at boot, and the cluster NIC is whichever interface
   holds an address inside nodeCIDR.
3. **Permissions.** This is desktop and multiuser policy. liken has no
   users. Workloads reach devices through Kubernetes, where the device
   plugin and the container runtime control what reaches a pod.
4. **The event bus** for other software (libudev). liken's only
   possible subscribers read the kernel's netlink socket directly.

Existing design decisions or Kubernetes answer three of the four jobs.
The milestone's work is the first job, module loading, plus reporting.

## QEMU drills, 2026-07-17

A dev-cluster guest booted with an xhci controller and a QMP socket
(`QEMU_EXTRA`). A privileged pod observed the guest with tools
that the OS does not include: a netlink listener for uevents, `/sys`
and `/dev` through a hostPath, and insmod for the phases that needed
it.

* **The kernel does everything except load the module.** A hot-added
  USB stick produced a devtmpfs node, uevents, and
  `MODALIAS=usb:v46F4p0001...ic08isc06ip50`. Nothing more happened,
  because the image shipped no `usb-storage` module and loaded none.
  The device stayed enumerated and inert.
* **A resident driver closes the gap with no userspace help.** One
  insmod of `usb-storage.ko` made the kernel bind the already-plugged
  orphan device: bind, SCSI probe, `/dev/sda`, and eleven cascading
  uevents, with no userspace direction at any step. Device-first order
  is the harder order to prove. Driver-first order, which is what
  `spec.modules` produces at boot, binds the same way. Hotplug needs
  no daemon. It needs the driver to be resident.
* **Bus controllers are not a problem.** xhci and ehci are built into
  the Ubuntu kernel, so a hot-added EHCI controller enumerated and
  bound immediately. Only leaf-device drivers need a loading story.
* **The kernel log relay already reports what happens.** Enumeration,
  SCSI attach, and `[sda] Attached SCSI disk` all arrived in `kubectl
  logs` through the kmsg relay.
* **Naming the missing driver needs the full alias table.** The image
  prunes `modules.alias` along with the modules it drops: 71 lines
  survive of the kernel build's 38,171 (1.8 MB). A status message that
  says "this device needs usb-storage", instead of "unknown device
  46f4:0001", needs the full table in the image.
* **One modalias can match more than one driver.** The stick's
  fingerprint matches both `uas` and `usb_storage`. udev loads every
  match and lets the drivers settle which one claims the device.
  liken's declarative design avoids the ambiguity, because a person
  declares the exact module. The report still names every candidate.
* **A DRM node does not mean a driver is bound.** With a virtio GPU
  cold-plugged, `/dev/dri/card0` existed before any GPU module loaded.
  That node belonged to `simple-framebuffer`, the firmware's
  framebuffer on a DRM node. The GPU stayed undriven until two insmods
  (`virtio_dma_buf`, `virtio-gpu`) produced `card1` and `renderD128`.
  Hardware reporting must check the driver binding, not the node.

Lab technique for the next drill:

* QEMU's emulated xhci never enumerates full-speed devices such as
  `usb-serial`, hot-plugged or cold-plugged. The device stays
  unaddressed on the bus.
* `virtio-gpu-pci` refuses hotplug. Cold-plug it instead.
* Adding devices shifts PCI slot assignments. This once made OVMF
  delete the installed boot entries as unreachable. `BOOT=kernel`
  boots are not affected, and a reinstall creates the entries again.

## The design

The design declares drivers and reports gaps. Module loading stays
where milestone 18 put it: `spec.modules` names the drivers that a
machine's hardware needs, init loads them at boot, and a resident
driver then serves hotplug. There is no modalias-driven automatic
load. An unexpected device on a production machine becomes an inert,
reported fact, not a silently loaded driver. This matches storage
claiming, which probes reality and refuses to act on ambiguity, and
the feature vocabulary, where an unknown slug fails loudly.

A driver declaration is only a pure edit if the module is already on
the machine. Milestone 32 makes that true: the image ships the kernel
build's full module tree, the firmware blobs those modules can
request, and CPU microcode, all inert until something asks for them.
Milestone 32 also covers the slot budget. Milestone 11 takes one
piece of that work: the naming data. The image ships the kernel
build's complete `modules.alias` (1.8 MB), while `modules.dep`
describes only what the image ships. The report's job is to name
drivers that the image may not ship, and a pruned table cannot name
the missing module.

The reporting is in the hardware package and in init's
`hardware.go`. It has two parts:

* A sysfs walk over the pci and usb buses at boot. This is the same
  coldplug replay that udev does, but for observation only.
* A netlink uevent listener on the machine plane. The listener
  re-walks the buses when the hardware changes. It treats each uevent
  as a signal to re-walk, not as a fact to record. A re-walk cannot
  drift out of sync with reality, but a stored mirror of event
  payloads can.

Both parts write the same lines to the console and to the facts file.
The operator lifts the facts into status on every pass, so a
hot-plugged device appears in `kubectl get machine` within seconds
with no reboot.

For each device that no driver claims, the status names the candidate
modules from the full alias table. It names every match, because one
fingerprint that matches several drivers, such as `uas` and
`usb_storage` for one stick, is normal. The status also includes a
message phrased as the fix: "declare usb_storage or uas in
spec.modules" when the image ships those modules, or "upgrade to a
release that does" when it does not. The report excludes two kinds of
device: a device that only a builtin driver could claim, and a device
that no loadable module matches at all, such as a host bridge or a
platform stub. An operator cannot act on either one.

Status reports the gap, never the full census. A machine whose
hardware is fully driven reports nothing, the same way an absent
condition is the healthy state. Anyone who needs the full inventory
can read `/sys` from a workload, which is how the drills observed the
machine.

There is no HardwareClaimed condition. Conditions judge requests: a
declared module that failed to load is a request the machine did not
satisfy. An unclaimed
device is hardware that nobody requested, and staying undriven is a
normal, permanent state. Every QEMU guest has a VGA adapter, which
needs `bochs`, that no server image drives, and a headless machine
with a GPU also leaves that GPU undriven by design. A condition would
mark all of those machines Degraded forever. The report follows the
precedent of undeclared disks: it stays as inventory in status, loud
on the console, and judged by nobody, until a person declares the
driver. At that point `status.modules` judges the request.

### Device names

The report names hardware the way an operator knows it. The cost
differs across three kinds of name.

USB devices have their manufacturer and product strings in the
hardware. The kernel reads these strings at enumeration, so the names
cost nothing. An undriven interface borrows its parent device's
strings, because leaf drivers bind interfaces and the strings are on
the device.

PCI devices have only numeric IDs. The names that lspci prints come
from the pci.ids database. liken vendors this database as a pinned
flat file in the hwdata domain, the same way it ships the full
`modules.alias`. This small image cost lets the status say "Red Hat,
Inc. Virtio 1.0 GPU" instead of "1af4:1050".

PCI class codes are a small, spec-defined enum. They are in a Go
table and need no database.

The pci.ids dependency is soft. The reporter falls back to numeric IDs
when the file is missing. hwdata's notice and source mirror joined the
licensing domain, like every other vendored pin.

### Drill, 2026-07-18

The second drill ran the whole user story on the dev cluster with one
machine and no udev. A stick hot-plugged over QMP appeared in
`status.hardware.unclaimed` within seconds, named from its own
strings, with `uas` and `usb_storage` listed as candidates from the
full alias table, and a message that named the `spec.modules` edit to
make. The edit was staged, the conductor granted the reboot, and the
next boot loaded the driver and bound the stick before Kubernetes came
up. After that, the walk reported nothing.

The drill produced two findings:

* QEMU's own VGA adapter stays unclaimed on every guest, which settles
  the condition question above.
* `status.hardware.blockDevices` was only a boot-time snapshot. The
  rebooted walk ran before the stick's SCSI probe finished, so
  `/dev/sda` served pods while the inventory did not list it.

The uevent watcher now refreshes the disk inventory in the same
republish step it uses for unclaimed devices, and the probe's own
uevents trigger that refresh. blockDevices is now as current as the
rest of the report, so a hot-plugged disk whose driver is already
declared appears there within seconds of the plug.

The watcher must not coalesce uevent bursts by waiting for quiet. On a
Kubernetes node, container churn emits uevents almost continuously,
and one crash-looping pod held the watcher busy for minutes. The wait
for quiet now has a hard time limit, and the watcher walks during the
noise. Walks are cheap and idempotent.

## The Kubernetes half: DRA

Workloads reach hardware through dynamic resource allocation
(resource.k8s.io), which is GA in the Kubernetes version liken runs.
DRA's object model provides most of what this milestone would
otherwise build:

* **ResourceSlice is the inventory.** A per-node driver publishes each
  usable device with typed attributes: vendor, product, serial, and
  class, decorated with hwdata's names. This is where bulk hardware
  belongs. ResourceSlice is built for churn, and Kubernetes
  garbage-collects it with the node, which is why `Machine.status`
  never holds a full census. Slices list what works, and
  `status.unclaimed` lists what does not work and what would fix it.
  A device whose driver is not loaded never reaches a slice, because
  `spec.modules` stays the gate.
* **DeviceClass is the purpose vocabulary.** It is a cluster-scoped
  name (`zigbee`, `ups`, `transcode`) with CEL selectors over device
  attributes. The hex vendor IDs are in exactly one object, owned by
  the deployment, and every workload manifest refers only to the name.
  To pin one physical unit, use a serial-number selector. For a
  capability class, such as "any VAAPI-capable render node", use an
  attribute expression. Real deployments need both shapes: fungible by
  capability, such as Jellyfin accepting any transcode device, and
  pinned by identity, such as NUT requiring the UPS on a specific
  wall. DeviceClass provides both through one mechanism.
* **Claims deliver the device.** A pod references a claim, and the
  scheduler matches the claim against the slices. This makes "run this
  where the hardware is" ordinary scheduling, with no node labels and
  no nodeSelector to keep in agreement with physical reality. At
  container creation, the driver answers with CDI specs that name
  whatever the node is called during that boot. No pod needs
  privilege, no pod mounts `/dev` through a hostPath, and no YAML
  names a device by its enumeration order.

liken owns only the driver: a small program that watches sysfs and
uevents, publishes ResourceSlices, and answers the kubelet's prepare
calls with CDI specs. The driver reuses the watcher that the reporting
half builds, so one listener has two outputs.

An earlier version of this design put the naming on the Machine spec,
with a `spec.devices` map that bound role names to matchers in the
style of storage claiming. DRA replaces that design. DeviceClass
already provides naming by purpose, at the scope where the names live,
because a purpose is deployment vocabulary and not a fact about one
machine. The scheduler's matching replaces the spec's claim-and-refuse
pass. Storage claiming needs strict refusal semantics, but devices do
not, because allocating one of two matching dongles destroys nothing.
The extra Machine API surface was not worth keeping.

The udev translation holds, with one point sharpened. Match rules
become DeviceClass selectors, because upstream built the rule engine.
SYMLINK's stable name becomes the class name, resolved to a node path
at injection time. OWNER, GROUP, and MODE become which pod holds the
claim. The event bus becomes the uevent listener. The OS runs no
host-policy daemon. The API holds the vocabulary, and a reconciler
closes the loop.

Three hosting questions are settled:

* The driver runs inside the machine operator, as one more goroutine
  in a process that already runs on every node with API access. The
  memory envelope has no room for a second daemon.
* The driver is standing equipment, not a feature slug. Slugs keep
  optional heft out of the default footprint, and there is no heft
  here to opt out of. Every machine must report its own hardware gaps.
* liken ships no DeviceClasses. A class encodes a deployment's
  purposes, so a shipped class would guess at someone else's
  vocabulary. The documentation teaches the shape, and deployments
  write their own classes.

One question stays open for the design pass: how much attribute
vocabulary liken standardizes, and how much it only documents.

### The inventory

The inventory half is built and drilled, as of 2026-07-18. The
operator publishes one ResourceSlice per node, listing driven PCI and
USB devices but excluding bus plumbing such as usbcore's device nodes,
hubs, and PCIe ports. It uses the same hand-rolled, honest-subset
style as every other API type. Importing `k8s.io/api` for the slice
structs was measured and declined, because that import links 11 MB of
apimachinery for what sixty lines of code write directly.

Each device has string attributes under the driver's own domain:
bus, driver, class, name, modalias, vendor, product, and serial number
when the hardware has one. Each device is named by its bus-prefixed
sysfs address. That address names the slot, not the physical unit, so
replacing a dongle in the same port keeps the same device name. To pin
one physical unit, select on the serial attribute.

The slice is owned by the Node's UID, so the inventory is deleted with
the node registration instead of outliving it.

The naming database is part of the operator's own OCI image, not a
hostPath into the OS's copy. A DaemonSet template applied fleet-wide
mid-upgrade must never mount a path that an older node's OS lacks.

The drill: the release path shipped the change through a forward roll
onto slot B. The slice appeared with eight devices on the first pass.
Unplugging the stick converged the slice to seven devices at pool
generation 2, in ten seconds. Re-plugging it converged back at
generation 3, where QEMU had re-enumerated the stick on a different
port, and the device name moved with the slot.

The publish rule is restrictive, not generous. Prepare hands a pod
device nodes with read-write access and no privilege check anywhere,
so a claim on the system disk would let a pod destroy it with one `dd`
command. A ResourceSlice is an offer, not a census, and
publication is the one place where enforcement can be airtight,
because the scheduler can only allocate what a slice lists.

The publish rule applies three tests:

1. The device must be driven and must not be bus plumbing.
2. The device must be deliverable. Its sysfs subtree must hold `/dev`
   nodes, pruned at nested bus devices so that a controller does not
   inherit its peripherals' nodes.
3. The device must not belong to the platform. Nothing in its subtree
   may back a storage role. This test keeps the two claiming systems
   separate: a disk belongs either to the machine, by role, or to
   workloads, by DRA, but never to both.

On the lab guest, these tests leave the stick and the IDE controller,
whose CD-ROM drive is real, deliverable hardware. They exclude NICs,
which have nothing to inject; the XHCI controller, whose nodes belong
to the USB devices; and every role-backed virtio disk.

One refinement waits for real hardware. For SATA, IDE, and SAS, the
inventory unit is currently the controller, but the claimable thing is
the media on its SCSI sub-bus. The controller's attributes say "Intel
AHCI", while delivery is whatever disks attach to it, so a six-disk
controller publishes as one coarse entry. NVMe and USB mass storage do
not have this problem, because the bus device is the media and its
attributes describe it directly.

The fix, once a machine can exercise it, is to publish each SCSI disk
or optical drive as its own slice device. Each device gets a name from
its board-stable port path, never an enumeration-order name such as
`sda`, and attributes from the media's own model, serial, and class.
Each device can be withheld on its own when a role stands on it. The
controller is never published. The lab's single IDE CD-ROM is close
enough to a one-to-one match that the current shape stays accurate
until this refinement lands.

If a workload needs a whole controller instead of one of its drives,
DRA has a mechanism ready. Partitionable devices, using
`sharedCounters` and `consumesCounters` in the v1 API behind the
DRAPartitionableDevices gate, let a slice list overlapping devices
that draw from one counter pool. The scheduler then enforces that a
whole-controller claim and a single-drive claim exclude each other.
This is additive to the media-leaf shape above, because the controller
re-enters the slice as a full device over the same counters. Nothing
is built for this yet, because the need is not real yet.

The same mechanism could model GPT partitions of one disk, but volumes
already serve that need, with `volumeMode: Block` and local
provisioners. DRA claims deliver drives, not filesystems.

### The kubelet plugin

The kubelet half is built and drilled, as of 2026-07-18. The operator
serves the two gRPC services that the kubelet dials: registration in
the plugin-watcher directory, and the v1 DRA plugin API on its own
socket. This brought grpc-go and the `k8s.io/kubelet` stubs into
go.mod, at about 3 MB compressed on the operator image. The helper
library and its forty-dependency tree stayed out.

Prepare treats the request as a signal to act, in the same style liken
uses elsewhere. The request names only the claim. The driver reads the
allocation back from the API server, refuses a claim whose UID
changed, re-walks sysfs with the same code that published the
inventory, and writes one CDI spec per claim UID under `/var/run/cdi`
for containerd to resolve. That directory is tmpfs, because the
kubelet re-prepares every claim after a boot.

Failures stay per-claim and in-band, so the kubelet retries and the
pod waits visibly in the ContainerCreating state.

The drill: a DeviceClass selecting mass-storage on this driver, a
claim, and an unprivileged pod reached the Running state, with
`/dev/sda` injected, six seconds after apply. The container reported
CLAIMED-OK while holding no privilege and no hostPath.

The drill also produced an upgrade lesson. Right after the reboot, the
new operator binary started inside the pod that the previous template
had created, because OnDelete keeps the pod and the stable image tag
resolves to the new build. That old pod lacked the kubelet-socket
mounts, and a fatal error there killed status publishing, which is
what the pod steward waits on before it refreshes the pod. The DRA
plugin's startup failure is now loud but non-fatal. The machine
operates without device claims for the one template-lag window, and
the refreshed pod then brings the plugin up.

### Device health

Re-plug semantics define what a standing allocation means when its
device disappears. Upstream is answering this. DRA's device-health and
device-taints features, alpha and maturing in the Kubernetes version
liken runs, let a driver report a published device as unhealthy and
taint it. This steers new claims away and reports the failure in the
claiming pod's own status.

The device lifecycle then has three stages, each reported by the right
API to the right audience:

* A device is unclaimed in Machine status when the kernel cannot drive
  it and an operator can fix it.
* A device is published in a ResourceSlice when it is claimable.
* A device is tainted when it was claimable but is now faulty, and the
  workload must tolerate it or leave.

The driver's uevent watcher already receives the remove events that
this needs, so the same listener feeds a third output. The driver interface
includes health support from day one, enabled as the feature matures.
Two workloads will prove it: a transcode claim against a render node,
which the lab can fake today with virtio-gpu, and an identity-pinned
claim against a real USB device once one is available.

## Live module loads

Module loading does not use the k3s-restart tier. It converges with no
disruption. An additive `spec.modules` edit, one that leaves storage
untouched and retracts nothing, stages the manifest for durability. It
then asks init, over a third intent file alongside the reboot and
restart intents, to load the additions into the running kernel.

Init re-derives the staged manifest's live-applicability for itself,
with the same shared drift functions the operator uses, so the two
cannot disagree. Init refuses anything that would need a boot.
Promotion happens after the loads complete, so a module that panics
the kernel leaves its manifest staged for the rejection machinery
instead of marked as proven.

There is no policy gate and no reboot turn, the same terms that apply
to the sysctls the operator reconciles live. The gates exist for
disruptions, and this is not one. Retraction keeps the reboot tier,
because loading a module is one-way.

The drill: plug a stick, watch it report unclaimed, declare
`usb-storage`, and the device is claimed five seconds later. This
happens in the same boot, with nothing drained. The console prints
"spec applied in place: usb-storage loaded without a reboot", then
"now driven by usb-storage", as the resident driver binds the
already-plugged hardware. This is the device-first order that the
first drills proved.

Also open: real GPU compute stacks, because no emulation can
substitute for them. Firmware and everything else the image must hold
belong to milestone 32.
