# Device management

Milestone 11 built and drilled both halves of device management, end
to end. The OS half covers unclaimed-device reporting and live module
loads. The DRA driver half covers inventory, the kubelet plugin, and
CDI delivery to an unprivileged pod.

This milestone opened with one question: how does a shell-less,
udev-less OS expose `/dev` beyond the basics? The basics do not cover
USB devices that arrive after boot, GPUs, or serial adapters.
devtmpfs creates the device nodes, but hotplug also requires the OS
to handle kernel uevents and load modules. On other Linux systems,
udev does that job.

The milestone also covered the Kubernetes half. Two questions applied
there: how do workloads reach the hardware, through device plugins or
dynamic resource allocation; and do devices belong in
`status.hardware` alongside CPUs and memory?

## What udev actually does

Most of udev's reputation describes jobs it no longer does. The
kernel enumerates hardware, binds drivers to devices, and, since
devtmpfs, creates the `/dev` nodes itself. Firmware blobs load from
`/lib/firmware` without help from userspace. Four jobs remain for
udev, and all four are policy:

1. **Loading modules.** When a device appears and no resident driver
   claims it, the kernel sends a uevent that carries a MODALIAS
   fingerprint for the orphan device, then waits. udev matches that
   fingerprint against `modules.alias` and loads the matching module.
   This is udev's only job that has no fallback inside the kernel.
2. **Stable names and symlinks** (`/dev/disk/by-uuid`, `enp3s0`).
   liken already avoids this job: storage identity uses GPT
   partition names probed at boot, and the cluster NIC is whichever
   interface holds an address inside nodeCIDR.
3. **Permissions.** This is desktop-and-multiuser policy. liken has
   no users. Workloads reach devices through Kubernetes, where the
   device plugin and the container runtime decide what reaches a pod.
4. **Acting as the event bus** for other software (libudev). liken's
   only potential subscribers can read the kernel's netlink socket
   directly, so they do not need udev for this.

Existing design decisions or Kubernetes already answer three of the
four jobs. The milestone's work reduces to the first job, module
loading, plus reporting.

## What the lab taught (QEMU drills, 2026-07-17)

A dev-cluster guest booted with an xhci controller and a QMP socket
(`QEMU_EXTRA`). A privileged pod observed the guest and carried tools
that the OS itself does not include: a netlink listener for uevents,
`/sys` and `/dev` through a hostPath, and insmod for the phases that
needed it.

* **The kernel does everything except load the module.** A hot-added
  USB stick produced the full announcement — a devtmpfs node,
  uevents, `MODALIAS=usb:v46F4p0001...ic08isc06ip50` — and then
  nothing happened, because the image shipped no `usb-storage`
  module and loaded none. The device stayed enumerated and inert.
* **A resident driver closes the gap without any userspace help.**
  One insmod of `usb-storage.ko` made the kernel bind the
  already-plugged orphan device on its own: bind, SCSI probe,
  `/dev/sda`, and eleven cascading uevents, all without userspace
  directing any step. This was the harder order to prove too, since
  the device arrived before the driver. Driver-first order, which is
  the boot-time arrangement that `spec.modules` produces, binds the
  same way. Hotplug needs no daemon. It needs the driver to already
  be resident.
* **Bus controllers are not a problem.** xhci and ehci are built
  into the Ubuntu kernel, so a hot-added EHCI controller enumerated
  and bound instantly. Only leaf-device drivers need a loading story.
* **The kernel log relay already reports what happens.** Every step —
  enumeration, SCSI attach, `[sda] Attached SCSI disk` — arrived in
  `kubectl logs` through the kmsg relay. Console parity for hardware
  events was already half built before the milestone started.
* **Naming the missing driver requires the full alias table.** The
  image prunes `modules.alias` along with the modules it drops: only
  71 lines survive of the kernel build's 38,171 (1.8 MB). A status
  message that says "this device needs usb-storage", instead of
  "unknown device 46f4:0001", needs the full table shipped. This
  costs little, but it is a real image-build change.
* **Modalias matching can match more than one driver.** The stick's
  fingerprint matches both `uas` and `usb_storage`. udev loads every
  match and lets the drivers sort out which one claims the device.
  liken's declarative design avoids this ambiguity, because a person
  declares the exact module they mean. But a reporting status should
  still name every candidate driver.
* **A DRM node does not mean a driver is bound.** With a virtio GPU
  cold-plugged, `/dev/dri/card0` existed before any GPU module
  loaded. That node belonged to `simple-framebuffer`, the firmware's
  framebuffer using a DRM node. The real GPU stayed undriven until
  two insmods (`virtio_dma_buf`, `virtio-gpu`) produced `card1` and
  `renderD128`. Hardware reporting must check the driver binding, not
  just whether a node exists.

Lab technique for the next drill: QEMU's emulated xhci never
enumerates full-speed devices like `usb-serial`, whether hot-plugged
or cold-plugged; the device stays unaddressed on the bus. Also,
`virtio-gpu-pci` refuses hotplug outright, so cold-plug it instead.
Adding devices also shifts PCI slot assignments. This once made OVMF
delete the installed boot entries as unreachable. `BOOT=kernel` boots
are not affected by this, and a reinstall creates the entries again.

## The emerging design

The design declares drivers and reports gaps. Module loading stays
exactly where milestone 18 put it: `spec.modules` names the drivers
that a machine's hardware needs, init loads them at boot, and a
resident driver then serves hotplug automatically. There is no
modalias-driven automatic loading. A surprise device on a production
machine should become an inert, reported fact, not a silently-loaded
driver. This matches the same approach as storage claiming, which
probes reality and refuses to act on ambiguity, and the feature
vocabulary, where an unknown slug fails loudly instead of continuing
to run unnoticed.

For declaring a driver to stay a pure edit, the module must already
be on the machine. Milestone 32 (batteries included) makes that true:
the image ships the kernel build's entire module tree, the firmware
blobs those modules can request, and CPU microcode, all inert until
something asks for them. Milestone 32 also carries the slot budget.
Milestone 11 takes one piece of that work for itself: the naming
data. The image ships the kernel build's *complete* modules.alias
(1.8 MB), while modules.dep keeps describing only what the image
ships. This is necessary because the report's whole job is to name
drivers that the image may not carry, and a pruned table cannot name
the very module that is missing.

The reporting is built, in the hardware package and in init's
hardware.go. It has two parts. The first part is a sysfs walk over
the pci and usb buses at boot. This walk performs the same coldplug
replay that udev does, but for observation only. The second part is
a netlink uevent listener on the machine plane. The listener
re-walks the buses when the hardware changes, and it treats each
uevent as a signal to re-walk, not as a fact to record directly. A
re-walk cannot drift out of sync with reality, but a stored mirror of
event payloads can.

Both parts feed the console and the facts file with the same lines.
The operator then lifts the facts into status on every pass. This is
why a hot-plugged device appears in `kubectl get machine` within
seconds, with no reboot needed.

For each device that no driver has claimed, the status names the
candidate modules from the full alias table. It names every match,
because one fingerprint matching several drivers, such as uas and
usb_storage for one stick, is normal. The status also carries a
message phrased as the fix: "declare usb_storage or uas in
spec.modules" when the image carries those modules, or "upgrade to a
release that does" when it does not. The judgment excludes two kinds
of devices: devices that only a builtin driver could claim, and
devices that no loadable module matches at all, such as host bridges
and platform stubs. A report that an operator cannot act on is
noise.

Status stays small because it reports the gap, never the full
census. A machine whose hardware is fully driven reports nothing. As
with conditions, absence is the healthy state. The full device
inventory does not belong in status. Anyone who needs it can read
`/sys` from a workload, which is how the drills themselves observed
the machine.

One deliberate absence came from the lab, not from advance design:
there is no HardwareClaimed condition, even though the report looks
similar to modules and features at first glance. Conditions judge
requests: a declared module that failed to load is a broken promise.
But unclaimed devices are hardware that nobody has requested
anything for, and staying undriven is a normal, permanent state.
Every QEMU guest carries a VGA adapter, which needs `bochs`, that no
server image drives, and a headless machine with a GPU also leaves
that GPU undriven by design. A condition would mark every one of
those machines Degraded forever. So the report follows the same
precedent as undeclared disks: it stays as inventory in status, loud
on the console, and judged by nobody, until a person declares the
driver. At that point, status.modules judges the request.

The report names hardware the way an operator knows it, not in hex.
The cost of this differs across three kinds of names.

USB devices carry their manufacturer and product strings in the
hardware itself. The kernel reads these strings at enumeration, so
the names cost nothing extra. An undriven interface borrows its
parent device's strings, because leaf drivers bind interfaces, and
the strings live on the device itself.

PCI devices carry only numeric IDs. The names that lspci prints come
from the pci.ids database. liken vendors this database as a pinned
flat file in the hwdata domain, the same way it ships the full
modules.alias: a small image cost that lets the status say "Red Hat,
Inc. Virtio 1.0 GPU" instead of "1af4:1050".

PCI class codes are a small, spec-defined enum that lives as a Go
table and needs no database.

The pci.ids dependency stays soft. The reporter falls back to
numeric IDs when the file is missing. hwdata's notice and source
mirror joined the licensing domain, like every other vendored pin.

The second drill, on 2026-07-18, ran the whole user story on the dev
cluster with one machine and no udev anywhere. A stick hot-plugged
over QMP appeared in status.hardware.unclaimed within seconds, named
from its own strings, with uas and usb_storage listed as candidates
from the full alias table, and a message naming the spec.modules
edit to make. The edit was staged, the conductor granted the reboot,
and the next boot loaded the driver and bound the stick before
Kubernetes came up. After that, the walk reported nothing, which is
the intended result.

The drill produced two findings. First, it settled the condition
question from above: QEMU's own VGA adapter stays unclaimed on every
guest. Second, it showed that status.hardware.blockDevices was only
a boot-time snapshot. The rebooted walk ran before the stick's SCSI
probe finished, so /dev/sda served pods while the inventory did not
list it yet.

The uevent watcher now refreshes the disk inventory in the same
republish step it uses for unclaimed devices; the probe's own
uevents trigger that refresh. This keeps blockDevices as current as
the rest of the report, so a hot-plugged disk whose driver is
already declared appears there within seconds of the plug.

Proving this fix taught its own lesson. Coalescing uevent bursts by
waiting for quiet is a trap on a Kubernetes node, because container
churn emits uevents more or less continuously; one crash-looping pod
held the watcher busy for minutes. So the wait for quiet now has a
hard time limit, and the watcher simply walks during the noise
instead. Walks are cheap and idempotent, and reporting honestly
beats waiting to coalesce events.

## The Kubernetes half: DRA

Workloads reach hardware through dynamic resource allocation
(resource.k8s.io), which is GA in the Kubernetes version liken runs.
DRA's object model already provides most of what this milestone
would otherwise have had to build:

* **ResourceSlice is the inventory.** A per-node driver publishes
  each usable device with typed attributes: vendor, product, serial,
  and class, decorated with hwdata's names. This is where bulk
  hardware belongs. ResourceSlice is built for churn, and Kubernetes
  garbage-collects it with the node, which is why Machine.status
  never carries a full census. The split follows naturally: slices
  carry what works, and status.unclaimed carries what does not work
  and what would fix it. A device whose driver is not loaded never
  reaches a slice, because spec.modules stays the gate.
* **DeviceClass is the purpose vocabulary.** It is a cluster-scoped
  name (`zigbee`, `ups`, `transcode`) with CEL selectors over device
  attributes. The hex vendor IDs live in exactly one object, owned
  by the deployment, and every workload manifest refers only to the
  name. Pinning to one physical unit uses a serial-number selector.
  A capability class, such as "any VAAPI-capable render node", uses
  an attribute expression instead. Real deployments need both
  shapes: fungible by capability, such as Jellyfin accepting any
  transcode device, and pinned by identity, such as NUT requiring
  the UPS on a specific wall. DeviceClass provides both through the
  same mechanism.
* **Claims deliver the device.** A pod references a claim, and the
  scheduler matches the claim against the slices. This makes "run
  this where the hardware is" ordinary scheduling, with no node
  labels and no nodeSelector to keep in agreement with physical
  reality. At container creation, the driver answers with CDI specs
  that name whatever the node is called during that boot. No pod
  needs privilege, no pod mounts /dev through a hostPath, and no
  YAML names a device by its enumeration order.

What liken owns shrinks down to the driver: a small program that
watches sysfs and uevents, publishes ResourceSlices, and answers the
kubelet's prepare calls with CDI specs. This driver reuses the same
watcher that the reporting half builds, giving one listener two
outputs. Its likely home is the machine operator, which already runs
on every node with API access. The memory envelope argues against
running a second daemon.

An earlier version of this design put the naming on the Machine
spec, using a spec.devices map that bound role names to matchers in
the same style as storage claiming. DRA replaces that design.
DeviceClass already provides naming-by-purpose, at the scope where
the names actually live, because a purpose is deployment vocabulary,
not a fact about one machine. The scheduler's matching also replaces
the spec's claim-and-refuse pass. Storage claiming needs strict
refusal semantics, but devices do not need them, since allocating
one of two matching dongles destroys nothing. So the extra Machine
API surface was not worth keeping.

The udev translation from earlier still holds, with one point
sharpened. Match rules become DeviceClass selectors, since upstream
built the rule engine and liken did not need to. SYMLINK's stable
name becomes the class name, resolved to a node path at injection
time. OWNER, GROUP, and MODE settings become simply which pod holds
the claim. The event bus becomes the uevent listener. The OS does
not run a host-policy daemon. The API carries the vocabulary. A
reconciler closes the loop.

Three hosting questions are now settled.

First, the driver lives inside the machine operator, as one more
goroutine in a process that already runs on every node. The memory
envelope has no room for a second daemon.

Second, the driver is standing equipment, not a feature slug. Slugs
exist to keep optional heft out of the default footprint, and there
is no heft here to opt out of. A machine that cannot report its own
hardware gaps is failing at its basic job.

Third, liken ships no DeviceClasses. A class encodes a deployment's
purposes, so liken shipping one would mean guessing at someone
else's vocabulary. The documentation teaches the shape, and
deployments write their own classes.

One question stays open for the design pass: how much attribute
vocabulary liken should standardize versus only document.

The inventory half is built and drilled, as of 2026-07-18. The
operator publishes one ResourceSlice per node, listing driven PCI
and USB devices but excluding bus plumbing such as usbcore's device
nodes, hubs, and PCIe ports. It does this through the same
hand-rolled, honest-subset style as every other API type. The team
measured importing k8s.io/api for the slice structs and declined it,
since that import would link 11 MB of apimachinery for what sixty
lines of code can write directly.

Each device carries string attributes under the driver's own domain:
bus, driver, class, name, modalias, vendor, product, and serial
number when the hardware has one. Each device is named by its
bus-prefixed sysfs address. This address names the slot, not the
physical unit, so replacing a dongle in the same port keeps the same
device name; identity-pinning is instead the job of the serial
attribute.

The slice is owned by the Node's UID, so the inventory is deleted
with the node registration instead of outliving it.

The naming database is part of the operator's own OCI image, not a
hostPath into the OS's copy. A DaemonSet template applied fleet-wide
mid-upgrade must never mount a path that an older node's OS lacks.

The drill: the release path shipped the change through a forward
roll onto slot B. The slice appeared with eight devices on the first
pass. Unplugging the stick converged the slice to seven devices at
pool generation 2, in ten seconds. Re-plugging it converged back at
generation 3, where QEMU had re-enumerated the stick on a different
port, and the device name moved with the slot, matching the
semantics chosen above.

The team resolved the census question against generosity, because
the claim scenario that worried them is real. Prepare hands a pod
device nodes with read-write access and no privilege check anywhere,
so a claim on the system disk would leave it one `dd` command from
ruin. A ResourceSlice is an offer, not a census, and publication is
the one place where enforcement can be airtight: the scheduler can
only allocate what a slice lists, through the API's own machinery.

So the publish rule applies three tests. First, the device must be
driven and must not be bus plumbing. Second, the device must be
*deliverable*: its sysfs subtree must carry /dev nodes, pruned at
nested bus devices so that a controller does not inherit its
peripherals' nodes. Third, the device must *not belong to the
platform*: nothing in its subtree may back a storage role. This
third test keeps the two claiming systems separate: a disk belongs
either to the machine, by role, or to workloads, by DRA, but never
both.

On the lab guest, these three tests leave exactly the honest offers:
the stick, and the IDE controller, whose CD-ROM drive is real,
deliverable hardware. The tests exclude NICs, which have nothing to
inject; the XHCI controller, whose nodes belong to the USB devices'
own decisions; and every role-backed virtio disk.

One refinement waits for real hardware. For SATA, IDE, and SAS, the
inventory unit is currently the controller, but the claimable thing
is really the media on its SCSI sub-bus. The controller's attributes
say "Intel AHCI", while delivery is whatever disks are attached to
it, so a six-disk controller would publish as one coarse entry. NVMe
and USB mass storage do not have this problem, because the bus
device *is* the media, and its attributes describe it directly.

The fix, once a machine can exercise it, is to publish each SCSI
disk or optical drive as its own slice device. Each device would be
named by its board-stable port path, never by an enumeration-order
name like sda, and attributed by the media's own model, serial, and
class. Each device could also be individually withheld when a role
stands on it. The controller itself would never be published. The
lab's single IDE CD-ROM is close enough to a one-to-one match that
the current shape stays honest until this refinement lands.

If a workload ever needs a whole controller instead of one of its
drives, DRA already has a mechanism ready for that. Partitionable
devices, using sharedCounters and consumesCounters in the v1 API
behind the DRAPartitionableDevices gate, let a slice list overlapping
devices that draw from one counter pool. The scheduler then enforces
that a whole-controller claim and a single-drive claim exclude each
other. This mechanism is purely additive to the media-leaf shape
described above, since the controller would simply re-enter the
slice as a full device over the same counters. Nothing is built for
this yet, because the need is not real yet.

The same mechanism could model GPT partitions of one disk, but
carving storage for workloads is already well served by volumes,
using volumeMode: Block and local provisioners. DRA claims deliver
drives, not filesystems.

The kubelet half is built and drilled, as of 2026-07-18. The
operator serves the two gRPC services that the kubelet dials:
registration in the plugin-watcher directory, and the v1 DRA plugin
API on its own socket. This finally brought grpc-go and the
k8s.io/kubelet stubs into go.mod, at about 3 MB compressed on the
operator image; the helper library and its forty-dependency tree
stayed out.

Prepare treats the request as a signal to act, in the same liken
style used elsewhere: the request names only the claim. The driver
then reads the allocation back from the API server, refuses a claim
whose UID changed, re-walks sysfs with the same code that published
the inventory, and writes one CDI spec per claim UID under
/var/run/cdi for containerd to resolve. This directory is tmpfs,
because the kubelet re-prepares every claim after a boot anyway.

Failures stay per-claim and in-band, so the kubelet retries and the
pod waits visibly in the ContainerCreating state. The drill: a
DeviceClass selecting mass-storage on this driver, a claim, and an
unprivileged pod reached the Running state, with /dev/sda injected,
six seconds after apply. The container reported CLAIMED-OK while
holding no privilege and no hostPath.

One upgrade lesson came from the drill. Right after the reboot, the
new operator binary woke inside the pod that the *previous* template
had created, because OnDelete keeps the pod and the stable image tag
resolves to the new build. That old pod lacked the kubelet-socket
mounts, and a fatal error there killed status publishing, which is
the very thing the pod steward waits on before it refreshes the pod.
The DRA plugin's startup failure is now loud but non-fatal. The
machine operates without device claims for the one template-lag
window, and the refreshed pod then brings the plugin up.

Re-plug semantics define what a standing allocation means when its
device disappears. Here, an upstream answer is arriving rather than
a liken design to invent. DRA's device-health and device-taints
features, alpha and maturing in the Kubernetes version liken runs,
let a driver report a published device as unhealthy and taint it.
This steers new claims away and surfaces the failure in the claiming
pod's own status.

The full device lifecycle then has three stages, each reported by
the right API to the right audience. A device is unclaimed in
Machine status when the kernel cannot drive it and an operator can
fix it. A device is published in a ResourceSlice when it is
claimable. A device is tainted when it was claimable but is now
sick, and the workload must tolerate it or leave.

The driver's uevent watcher already sees the remove events that this
needs, using the same listener to feed a third output. So the driver
interface should carry health support from day one, enabled as the
feature matures. The proving workloads are already picked: a
transcode claim against a render node, which the lab can fake today
with virtio-gpu, and an identity-pinned claim against a real USB
device once one is available.

One open question closed with a stronger answer than expected:
module loading does not use the k3s-restart tier at all. It
converges with no disruption. An *additive* spec.modules edit, one
that leaves storage untouched and retracts nothing, stages the
manifest for durability. It then asks init, over a third intent file
alongside the reboot and restart intents, to load the additions into
the running kernel.

Init re-derives the staged manifest's live-applicability for itself,
using the same shared drift functions that the operator uses, so the
two can never disagree. Init refuses anything that would need a
boot. Promotion happens after the loads complete, so a module that
panics the kernel leaves its manifest staged for the rejection
machinery, rather than marked as proven.

There is no policy gate and no reboot turn, the same terms that
apply to the sysctls the operator reconciles live: the gates exist
for disruptions, and this is not one. Retraction still keeps the
reboot tier, because loading a module is one-way.

The drill: plug a stick, watch it report unclaimed, declare
usb-storage, and the device is claimed five seconds later. This
happens in the same boot, with nothing drained. The console prints
"spec applied in place: usb-storage loaded without a reboot", then
"now driven by usb-storage", as the resident driver binds the
already-plugged hardware. This is exactly the device-first order
that the first drills proved.

Also open: real GPU compute stacks, since no emulation can
substitute for them. Firmware and everything else the image must
carry belong to milestone 32.
