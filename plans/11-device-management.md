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

## Devices by purpose: the Kubernetes half

Direction, not yet design. Kubernetes has two generations of
machinery for handing hardware to workloads: device plugins (a
DaemonSet advertises a named, counted resource; the kubelet injects
the device node when a pod requests one) and dynamic resource
allocation (devices published with structured attributes, claims
selecting on them, GA in the Kubernetes liken currently runs). A
bare counted resource is not expressive enough for what real
deployments do with leaf hardware — a UPS on USB for NUT, a Zigbee
coordinator for zigbee2mqtt. Those workloads want *that specific
device*, stably, not "any serial port"; and raw attribute selectors
in workload manifests would leak hardware fingerprints (hex vendor
IDs) into every deployment's YAML.

The liken-shaped answer is the storage-claiming move a third time:
naming on the Machine spec, by purpose.

    spec:
      devices:
        zigbee:
          usb: { vendor: "10c4", product: "ea60" }
        ups:
          usb: { vendor: "0463" }

The machine matches each named role against the probed bus — exactly
one match or a loud refusal — status reports what each role
resolved to, and the role's *name* becomes the schedulable
vocabulary: the zigbee2mqtt pod claims `zigbee`, with no hex
anywhere in a workload manifest. Because the naming happens on the
spec, the plugin-versus-DRA choice demotes to an implementation
detail, and "run this wherever the Zigbee stick is" is scheduling,
not host configuration.

Seen whole, this is udev's policy layer rebuilt as Kubernetes API,
clause for clause: the match rules become spec matchers, SYMLINK
becomes the role name (resolved to this boot's node path at
container-injection time), OWNER/GROUP/MODE dissolve into which pod
holds the claim, RUN hooks become reconcilers, and the event bus is
the uevent listener the reporting half already builds. The OS
declined the host-policy daemon; the API grows the vocabulary; a
reconciler closes the loop — the same relocation storage claiming
performed on fstab. The open questions for its design pass: whether
role names are cluster vocabulary with per-machine bindings (the
features/registries split, again), what a claim conveys for a
device exposing several nodes, and re-resolution when hardware is
re-plugged mid-flight.

Also open: whether module loading can ride the k3s-restart
convergence tier instead of a reboot (loading is live-capable — the
drills proved device-first binding — so it could plausibly converge
even lighter); and real GPU compute stacks, which no emulation can
stand in for. Firmware and everything else the image must carry is
milestone 32's.
