# Device management

Milestone 11 — The OS half is built and drilled end to end; the
Kubernetes half (the DRA driver) awaits its design pass

The question this milestone opened with: how does a shell-less,
udev-less OS expose `/dev` beyond the basics — USB devices arriving
after boot, GPUs, serial adapters? devtmpfs gives us the nodes, but
hotplug means fielding kernel uevents and loading modules, which is
the job udev does elsewhere. Then the Kubernetes half: how workloads
get to the hardware (device plugins, dynamic resource allocation) and
whether devices belong in `status.hardware` alongside CPUs and
memory.

## What udev actually does

Most of udev's reputation describes jobs it no longer has. The kernel
enumerates hardware, binds drivers to devices, and (since devtmpfs)
creates the `/dev` nodes itself; firmware blobs load from
`/lib/firmware` without userspace help. What remains for udev is
policy, in four pieces:

1. **Loading modules.** When a device appears and no resident driver
   claims it, the kernel announces the orphan — a uevent carrying a
   MODALIAS fingerprint — and waits. Matching that fingerprint
   against `modules.alias` and loading the result is udev's one job
   with no in-kernel fallback for hardware.
2. **Stable names and symlinks** (`/dev/disk/by-uuid`, `enp3s0`).
   liken already declined this category: storage identity is GPT
   partition names probed at boot, and the cluster NIC is whichever
   interface holds an address inside nodeCIDR.
3. **Permissions.** Desktop-and-multiuser policy; liken has no
   users, and workloads reach devices through Kubernetes, where the
   device plugin and the container runtime decide what lands in a
   pod.
4. **Being the event bus** for other software (libudev). liken's
   only would-be subscribers can read the kernel's netlink socket
   directly.

Three of the four are already answered by existing design decisions
or by Kubernetes. The milestone reduces to the first, plus
reporting.

## What the lab taught (QEMU drills, 2026-07-17)

A dev-cluster guest booted with an xhci controller and a QMP socket
(`QEMU_EXTRA`), observed from a privileged pod carrying the tools the
OS refuses to: a netlink listener for uevents, `/sys` and `/dev`
through a hostPath, insmod for the phases that needed it.

* **The kernel does everything but load the module.** A hot-added
  USB stick produced the full announcement — devtmpfs node, uevents,
  `MODALIAS=usb:v46F4p0001...ic08isc06ip50` — and then nothing,
  because `usb-storage` was neither shipped nor loaded. The device
  sat enumerated and inert.
* **A resident driver closes the gap with zero userspace.** One
  insmod of `usb-storage.ko` and the kernel bound the already-plugged
  orphan on its own: bind, SCSI probe, `/dev/sda`, eleven uevents
  cascading with no userspace directing any of it. That was the
  harder direction, too — device first, driver second. Driver-first
  (the boot-time arrangement `spec.modules` produces) binds the same
  way. Hotplug needs no daemon; it needs the driver resident.
* **Bus controllers are a non-problem.** xhci and ehci are built
  into the Ubuntu kernel; a hot-added EHCI controller enumerated and
  bound instantly. Only leaf-device drivers need a loading story.
* **The kernel log relay already tells the story.** Every step —
  enumeration, SCSI attach, `[sda] Attached SCSI disk` — arrived in
  `kubectl logs` through the kmsg relay. Console parity for hardware
  events is half-built before the milestone starts.
* **Naming the missing driver requires the full alias table.** The
  image prunes `modules.alias` along with the modules it drops: 71
  lines survive of the kernel build's 38,171 (1.8 MB). A status that
  says "this device wants usb-storage" — rather than "unknown device
  46f4:0001" — needs the full table shipped. Cheap, but it is a real
  image-build change.
* **Modalias matching is one-to-many.** The stick's fingerprint
  matches both `uas` and `usb_storage`; udev loads every match and
  lets the drivers sort out claiming. A declarative liken sidesteps
  the ambiguity — a person declares the module they mean — but a
  reporting status should name every candidate.
* **A DRM node is not a driver.** With a virtio GPU cold-plugged,
  `/dev/dri/card0` existed before any GPU module loaded — it was
  `simple-framebuffer`, the firmware's framebuffer wearing a DRM
  node. The real GPU sat undriven until two insmods
  (`virtio_dma_buf`, `virtio-gpu`) produced `card1` and
  `renderD128`. Hardware reporting must read the driver binding, not
  the presence of a node.

Lab technique, for the next drill: QEMU's emulated xhci never
enumerates full-speed devices like `usb-serial`, hot- or cold-plugged
(the device sits unaddressed on the bus), and `virtio-gpu-pci`
refuses hotplug outright — cold-plug it. Adding devices also shifts
PCI slot assignments, which once made OVMF delete the installed boot
entries as unreachable; `BOOT=kernel` boots are immune, and a
reinstall re-mints the entries.

## The emerging design

Declared drivers, reported gaps. Module loading stays exactly where
milestone 18 put it: `spec.modules` names the drivers a machine's
hardware needs, init loads them at boot, and a resident driver
serves hotplug for free. No modalias-driven automatic loading — a
surprise device on a production machine should be an inert, reported
fact, not a silently-loaded driver. That is the same posture as
storage claiming (probe reality, refuse to act on ambiguity) and the
feature vocabulary (an unknown slug degrades loudly instead of
acting quietly).

For declaring a driver to be a pure edit, the module has to already
be on the machine. Making that true — the image shipping the kernel
build's entire module tree, the firmware blobs those modules can
request, and CPU microcode, all inert until something asks — is
milestone 32 (batteries included), which also carries the slot
budget. The one piece 11 takes for itself is the naming data: the
image ships the kernel build's *complete* modules.alias (1.8 MB,
while modules.dep keeps describing only what shipped), because the
report's whole job is naming drivers the image may not carry, and a
pruned table cannot name the very module that's missing.

The reporting is built (the hardware package, plus init's
hardware.go): a sysfs walk over the pci and usb buses at boot — the
same coldplug replay udev does, but for observation — and a netlink
uevent listener on the machine plane that re-walks when the
hardware changes, treating each uevent as a doorbell rather than a
fact (a re-walk cannot drift; a mirror of event payloads can). Both
feed the console and the facts file with the same lines, and the
operator lifts the facts into status on every pass, which is what
makes a hot-plugged device surface in `kubectl get machine` within
seconds, no reboot involved. For each device no driver has claimed,
the status names the candidate modules from the full alias table —
every match, because one fingerprint matching several drivers (uas
and usb_storage for one stick) is normal — and a message phrased as
the fix: "declare usb_storage or uas in spec.modules" when the
image carries them, "upgrade to a release that does" when it
doesn't. The judgment excludes devices only builtin drivers could
claim and devices no loadable module matches at all (host bridges,
platform stubs): a report an operator cannot act on is noise.

Status stays small by reporting the gap, never the census. A machine
whose hardware is fully driven reports nothing — like conditions,
absence is the healthy state. The full device inventory is not
status material: anyone who needs it can read `/sys` from a
workload, which is how the drills themselves observed the machine.

One deliberate absence, learned in the lab rather than designed in
advance: there is no HardwareClaimed condition, though the report
looks like modules and features at first glance. Conditions judge
asks — a declared module that didn't load is a promise unkept — but
unclaimed devices are hardware nobody has asked anything about, and
undriven is a normal permanent state: every QEMU guest carries a
VGA adapter (wanting `bochs`) no server image drives, and a
headless machine with a GPU leaves it undriven by design. A
condition would read every one of those machines Degraded forever.
So the report follows the undeclared-disk precedent — inventory in
status, loud on the console, judged by nobody — until a person
declares the driver, at which point status.modules judges the ask.

The report names hardware the way an operator knows it, not in hex,
and the three pieces of that cost differently. USB devices carry
their manufacturer and product strings in the hardware itself — the
kernel reads them at enumeration, so those names are free (an
undriven interface borrows its parent device's strings, since leaf
drivers bind interfaces and the strings live on the device). PCI
devices carry only numeric IDs; the names lspci prints come from
the pci.ids database, which liken vendors as a pinned flat file
(the hwdata domain), the same shape as shipping the full
modules.alias: a small image cost so the status can say "Red Hat,
Inc. Virtio 1.0 GPU" instead of "1af4:1050". PCI class codes are a
small spec-defined enum that lives as a Go table, no database
needed. The pci.ids dependency stays soft — the reporter falls back
to numeric IDs when the file is missing — and hwdata's notice and
source mirror joined the licensing domain like every other vendored
pin.

The second drill (2026-07-18) ran the whole user story on the dev
cluster, one machine, no udev anywhere: a stick hot-plugged over
QMP surfaced in status.hardware.unclaimed within seconds, named
from its own strings, candidates uas and usb_storage from the full
alias table, message naming the spec.modules edit; the edit staged,
the conductor granted the reboot, and the next boot loaded the
driver and bound the stick before Kubernetes came up — after which
the walk reports nothing, which is the point. Two findings came
home. The condition question above was settled by QEMU's own VGA
adapter, unclaimed on every guest. And status.hardware.blockDevices
was a boot-time snapshot: the rebooted walk ran before the stick's
SCSI probe finished, so /dev/sda served pods while the inventory
didn't list it. The uevent watcher now refreshes the disk inventory
in the same republish it does for unclaimed devices — the probe's
own uevents are the doorbell — so blockDevices is as live as the
report, and a hot-plugged disk whose driver is already declared
appears there within seconds of the plug. Proving that surfaced its
own lesson: coalescing uevent bursts by waiting for quiet is a trap
on a Kubernetes node, whose container churn emits uevents more or
less continuously (one crash-looping pod held the watcher captive
for minutes), so the settle has a hard ceiling and simply walks
during the noise — walks are cheap and idempotent, and honesty
beats coalescing.

## The Kubernetes half: DRA

Workloads reach hardware through dynamic resource allocation
(resource.k8s.io, GA in the Kubernetes liken runs), and DRA's object
model absorbs most of what this milestone would otherwise have had
to invent:

* **ResourceSlice is the inventory.** A per-node driver publishes
  each usable device with typed attributes — vendor, product,
  serial, class, decorated with hwdata's names. This is where bulk
  hardware belongs, purpose-built for churn and garbage-collected
  with the node; it is why Machine.status never carries a census.
  The split is natural: slices carry what works, status.unclaimed
  carries what doesn't and what would fix it. A device whose driver
  isn't loaded never reaches a slice — spec.modules stays the gate.
* **DeviceClass is the purpose vocabulary.** A cluster-scoped name
  (`zigbee`, `ups`, `transcode`) with CEL selectors over device
  attributes. The hex vendor IDs live in exactly one object, owned
  by the deployment, and every workload manifest speaks only the
  name. Pinning to one physical unit is a serial-number selector; a
  capability class ("any VAAPI-capable render node") is an attribute
  expression. Both of the shapes real deployments need — fungible by
  capability (Jellyfin wants any transcode device) and pinned by
  identity (NUT wants the UPS on this wall) — are the same
  mechanism.
* **Claims do the delivery.** A pod references a claim, the
  scheduler matches it against the slices (so "run this where the
  hardware is" is ordinary scheduling — no node labels, no
  nodeSelector to keep in agreement with physical reality), and at
  container creation the driver answers with CDI specs naming
  whatever the node is called this boot. No privileged pods, no
  hostPath /dev mounts, no enumeration-order names in anyone's
  YAML.

What liken owns shrinks to the driver: a small program that watches
sysfs and uevents, publishes ResourceSlices, and answers the
kubelet's prepare calls with CDI specs. That is the same watcher the
reporting half builds — one listener, two outputs — and its likely
home is the machine operator, which already runs on every node with
API access; the memory envelope argues against a second daemon.

An earlier version of this design put the naming on the Machine spec
(a spec.devices map binding role names to matchers, storage-claiming
style). DRA supersedes it: DeviceClass already is naming-by-purpose,
at the scope the names actually live (a purpose is deployment
vocabulary, not a fact about one machine), and the scheduler's
matching replaces the spec's claim-and-refuse pass. The refusal
semantics that make storage claiming strict aren't load-bearing for
devices — allocating one of two matching dongles destroys nothing —
so the extra Machine API surface wasn't earning its place.

The udev translation still holds, with one clause sharpened: match
rules become DeviceClass selectors (upstream built the rule engine,
liken doesn't), SYMLINK's stable name becomes the class name
resolved to a node path at injection time, OWNER/GROUP/MODE dissolve
into which pod holds the claim, and the event bus is the uevent
listener. The OS declines the host-policy daemon; the API grows the
vocabulary; a reconciler closes the loop.

Three hosting questions are settled. The driver lives inside the
machine operator — one more goroutine in a process already on every
node, and the memory envelope has no room for a second daemon. It is
standing equipment, not a feature slug: slugs exist to keep optional
heft out of the default footprint, and there is no heft here to opt
out of — a machine that cannot report its own hardware gaps is
failing at its basic job. And liken ships no DeviceClasses: a class
encodes a deployment's purposes, so liken shipping one would be
guessing at someone else's vocabulary. The documentation teaches the
shape; deployments write their own. Still open for the design pass:
how much attribute vocabulary liken standardizes versus documents.

Re-plug semantics — what a standing allocation means when its
device disappears — has an upstream answer arriving rather than a
liken design to invent: DRA's device-health and device-taints
features (alpha and maturing in the Kubernetes liken runs) let a
driver report a published device unhealthy and taint it, steering
new claims away and surfacing the failure in the claiming pod's own
status. The full lifecycle then has three stages, each reported by
the right API to the right audience: unclaimed in Machine status
(the kernel can't drive it; an operator can fix it), published in a
ResourceSlice (claimable), and tainted (was claimable, currently
sick; the workload's problem to tolerate or leave). The driver's
uevent watcher already sees the remove events this needs — the same
listener, feeding a third output — so the driver interface should
carry health from day one, enabled as the feature matures. The proving
workloads are already picked: a transcode claim against a render
node (the lab can fake this today with virtio-gpu), and an
identity-pinned claim against a real USB device when one is on the
desk.

One open question closed itself harder than expected: module
loading doesn't ride the k3s-restart tier, it converges with no
disruption at all. An *additive* spec.modules edit — storage
untouched, nothing retracted — stages the manifest for durability
and then asks init, over a third intent file beside the reboot and
restart intents, to load the additions into the running kernel.
Init re-derives the staged manifest's live-applicability for itself
(the same shared drift functions the operator used, so the two can
never disagree) and refuses anything that would need a boot;
promotion comes after the loads, so a module that panics the kernel
leaves its manifest staged for the rejection machinery rather than
enshrined as proven. There is no policy gate and no reboot turn,
the same terms as the sysctls the operator reconciles live: the
gates exist for disruptions, and this isn't one. Retraction keeps
the reboot tier, because loading is one-way. The drill: plug a
stick, watch it report unclaimed, declare usb-storage, and the
device is claimed five seconds later — same boot, nothing drained,
the console narrating "spec applied in place: usb-storage loaded
without a reboot" and then "now driven by usb-storage" as the
resident driver binds the already-plugged hardware, exactly the
device-first order the first drills proved.

Also open: real GPU compute stacks, which no emulation can stand in
for. Firmware and everything else the image must carry is milestone
32's.
