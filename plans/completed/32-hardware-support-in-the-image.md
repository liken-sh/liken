# Hardware support in the image

Milestone 32 — Completed. The image carries the whole kernel module
tree, the firmware that those modules request, and CPU microcode, so a
physical machine runs correctly with no extra configuration.

One milestone owns everything the image must carry so that hardware
runs correctly without extra configuration: the kernel's loadable
modules, the firmware blobs that drivers need, and CPU microcode. The
design settled during milestone 11's drills. The deliverable is a
published release whose generic image boots a physical machine, with a
real NIC, a real disk controller, and a console on the GPU that is on
the board, with nothing to compose and nothing to rebuild. Every liken
machine before this milestone is virtual, the dev cluster's QEMU
guests and the liken.sh node on Linode, and a virtual machine needs
none of this. This milestone is the last work between the channel's
releases and the first bare-metal install.

The same rule applies here as to the feature vocabulary: you enable a
capability at runtime, never with an image rebuild. The payloads ship
inert and cost only disk space. The OS runs from a read-only image on
the boot slot, so none of this uses the 1 GB memory envelope that
milestone 29 made the lab's standing proof.

## Kernel modules: the whole tree

The image pruned the kernel's modules to the boot set, plus the
modules that the baked machine manifests declared. This works only for
an image built beside its manifests. On a machine installed from the
public channel, no spec.modules edit can load a module that its
release does not carry, and an operator who connects a serial adapter
must then wait for a new release. Thus the image ships the kernel
build's whole module tree, about 170 MiB of already-compressed
modules, inert until a manifest declares one. Milestone 11's user
story, that status names the missing driver, the operator declares it,
and a reboot applies it, becomes an edit on any machine.

To ship everything removes machinery instead of adding it. The build's
union-of-declared pruning step is gone. It also corrects one
inconsistency in the image: milestone 11 ships the kernel's complete
modules.alias file beside a pruned module tree, because its report
must name drivers that the image does not carry. With the whole tree
in the image, the alias table and the modules it names describe the
same system.

## Driver firmware: derived, not curated

A driver on real hardware loads its blobs from /lib/firmware at probe
time, directly, with no udev, and it reads compressed files without
help. On metal this need is common: many NICs do not link without
their blob, and a machine whose network link needs firmware that liken
did not ship never reaches its cluster.

The full linux-firmware tree is about 743 MiB, and most of it
describes hardware that an x86 server kernel cannot drive, such as ARM
SoCs, phone parts, and astronomy cameras. liken derives the set it
needs instead of curating one. Every module names the firmware it can
request in MODULE_FIRMWARE, which modinfo reads, so the set to ship is
the union over the module tree above. The kernel build defines that
set, not a person's judgement. Measured against the current kernel
pin, this derived set is about 206 MiB after deduplication, and half
of that is one directory: nouveau's NVIDIA GSP blobs, 103 MiB that
serve display paths a headless OS does not use.

The derivation has one exception. The image ships without nvidia,
which leaves out about 103 MiB of firmware, and it keeps the small GPU
families, amdgpu, i915, xe, and radeon, about 33 MiB together, which
make a console work on an ordinary machine. liken has no GPU-compute
design yet. A future milestone can decide this again when liken has
one. Until then, the composable-release design (milestone 22) is the
option for a person who needs more, because an nvidia-inclusive
community image is a rebuild with one more directory, not a fork.

The derivation gives a floor, not a full count. A few drivers
construct firmware names at runtime. A request for a blob that the
image does not have fails into kmsg, which the log relay ships. This
case is reportable under the same rule as an unclaimed device: say
what would correct it.

## CPU microcode

Microcode is necessary for security. Spectre-class mitigations degrade
without a message on stale microcode, and Intel forbids a late load of
an update more and more. The loading convention is its own: an
uncompressed cpio that holds kernel/x86/microcode/GenuineIntel.bin and
AuthenticAMD.bin, put ahead of the real initrd, at the point where the
kernel looks before it decompresses anything. liken's boot entries
already carry more than one initrd= line, because the deployment layer
travels that way, so microcode is one more line, first in order: a
vendored artifact with its own pin and fetch, never recomposed when
the OS updates. QEMU's -kernel path takes one initrd, so the lab
variant concatenates at build time, which is how the early-cpio format
is defined.

The image ships both vendors' blobs always, because Intel's blob is 21
MiB and AMD's is one. Microcode and most firmware are redistributable
binaries under their own terms, not the GPL. They need notices entries
in the licensing domain, but no source mirror, because no source
exists to mirror. hwdata's pci.ids, milestone 11's naming database,
uses the same vendored-pin pattern.

## The budget

The additions are about 295 MiB in total: 170 for the module tree,
about 103 for firmware, and 22 for microcode. The whole slot payload,
the system image, the kernel, the boot archive, the microcode, and the
layer, is then near 419 MiB against the 512 Mi slots then in use. That
fits, but with thin headroom, so two guards come with it. First, the
scaffold's and the dev cluster's default slot size grows to 1 Gi,
while the fleet is small enough that a change of defaults costs
nothing. Second, the release build compares its artifact sizes with
the declared slot size, so an image that is too large for its slots
fails the build instead of failing at install time.

## Out of scope

An update to the machine's own firmware, that is UEFI capsules, NIC
NVRAM, and SSD firmware through fwupd and LVFS, is a different job.
The items this milestone adds are inert bytes. fwupd is an agent whose
work reaches into the boot chain that liken owns. That work has its
own milestone (plans/33-firmware-updates.md), and it waits until bare
metal exists.

The TPM needs no blobs, and it belongs to the hardening tier. IPMI and
BMC sensors are kernel modules that the tree above carries. ACPI
quirks belong to the kernel.

Only real metal can prove three things: that the early cpio applies,
which the kernel shows when it reports the microcode revision it runs,
that a NIC which needs firmware links, and, with a GPU and its own
milestone, that a compute stack starts correctly. The shipping
mechanics all work correctly in the lab. The proof needs a machine.

## What landed (measured)

Release 2026.07.20-001 ships the milestone. The measured numbers
differ from the estimates above. The firmware set is 133 MiB in the
image, not 103, because the newer upstream release is larger, and
because glob declarations, which modern Qualcomm Wi-Fi uses, add files
that the estimate did not count. The blobs ship uncompressed, because
the squashfs compresses and deduplicates them. The system image is 375
MiB, and the slot payload is about 440 MiB.

Two design points settled differently from the text above.
linux-firmware does get a source mirror, because some of its blobs
carry the GPL, so the release workflow mirrors the one verified
upstream tarball, which carries the source that exists. Microcode is
the notices-only case. Status gained two fields instead of one: the
microcode pin, and the revision that the CPUs report, so metal can
prove that the early cpio applied.

The 1 Gi slot growth reached the whole fleet, liken.sh's node-1
included. Its machineEphemeral decreased to 512Mi so that the 3 GiB
system image still holds every role. The lab pays for its own
composition: -kernel guests now default to 4 GB, because the composed
initrd carries the whole OS in RAM. Every Ready proof runs from disk
at 1 GB, the arrangement that real machines use.
