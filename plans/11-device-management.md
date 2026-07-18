# Device management

Milestone 11 — Explored in the lab; design settling, nothing built

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
budget and the full modules.alias table this report needs to name
its candidates.

What gets built is the reporting: a netlink uevent listener in init
and a sysfs walk at boot (the same coldplug replay udev does, but for
observation), feeding console lines and the Machine's status with
the same facts. For each device no driver has claimed, the status
names the candidate modules from the full `modules.alias` table, so
the message reads "declare ftdi_sio" or "this image doesn't carry
it" — the status vocabulary's rule that an observation should say
what would fix it.

Status stays small by reporting the gap, never the census. A machine
whose hardware is fully driven reports nothing — like conditions,
absence is the healthy state. The full device inventory is not
status material: anyone who needs it can read `/sys` from a
workload, which is how the drills themselves observed the machine.

The report should name hardware the way an operator knows it, not in
hex, and the three pieces of that cost differently. USB devices
carry their manufacturer and product strings in the hardware itself
— the kernel reads them at enumeration, so those names are free. PCI
devices carry only numeric IDs; the names lspci prints come from the
pci.ids database (the hwdata project, ~1.2 MB of plain text), which
liken vendors as a pinned flat file in the first phase, the same
shape as shipping the full modules.alias: a small image cost so the
status can say "Red Hat Virtio GPU" instead of "1af4:1050". PCI
class codes are a small spec-defined enum that lives as a Go table,
no database needed. The pci.ids dependency stays soft — the reporter
falls back to numeric IDs when the file is missing — and hwdata's
notices join the licensing domain like every other vendored pin.

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

Open for the design pass: the driver's exact hosting (inside the
machine operator versus beside it) and whether it is a feature slug
or standing equipment; how much attribute vocabulary liken
standardizes versus documents; whether liken ships any DeviceClasses
or only teaches them; and re-plug semantics — what a standing
allocation means when its device re-enumerates. The proving
workloads are already picked: a transcode claim against a render
node (the lab can fake this today with virtio-gpu), and an
identity-pinned claim against a real USB device when one is on the
desk.

Also open: whether module loading can ride the k3s-restart
convergence tier instead of a reboot (loading is live-capable — the
drills proved device-first binding — so it could plausibly converge
even lighter); and real GPU compute stacks, which no emulation can
stand in for. Firmware and everything else the image must carry is
milestone 32's.
