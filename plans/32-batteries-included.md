# Batteries included

Milestone 32 — Not started; the design settled during milestone 11's
drills

One milestone owns everything the image must carry so that hardware
runs correctly without extra configuration: the kernel's loadable
modules, the firmware blobs that drivers need, and CPU microcode. The
deliverable is a published release whose generic image boots a
physical machine, with a real NIC, a real disk controller, and a
console on whatever GPU is soldered to the board, with nothing to
compose and nothing to rebuild. Every liken machine so far is virtual
(the dev cluster's QEMU guests, the liken.sh node on Linode), and
virtual machines need none of this. This milestone is what stands
between the channel's releases and the first bare-metal install.

The same principle runs throughout, applied here to hardware support
instead of to the feature vocabulary: enabling something is a runtime
act, never an image rebuild. Payloads ship inert and cost only disk
space. The OS runs from a read-only image on the boot slot, so none of
this touches the 1 GB memory envelope that milestone 29 made the
lab's standing proof.

## Kernel modules: the whole tree

Today the image prunes the kernel's modules down to the boot set, plus
whatever the baked machine manifests declare. This works only for
images built beside their manifests. A machine installed from the
public channel would find that no spec.modules edit can load a module
that its release never carried, and "wait for a release" is no answer
to "I plugged in a serial adapter". So the image should ship the
kernel build's entire module tree, about 170 MiB of already-compressed
modules, inert until declared. This turns milestone 11's user story
(status names the missing driver, the operator declares it, a reboot
applies it) into a pure edit on any machine.

Shipping everything removes machinery, rather than adding it. The
build's union-of-declared pruning step goes away, and one deliberate
inconsistency in the image is fixed: milestone 11 already ships the
kernel's complete modules.alias file beside a pruned module tree,
because its report has to name drivers that the image does not carry.
With the whole tree aboard, the alias table and the modules it names
finally describe the same system.

## Driver firmware: derived, not curated

Real hardware's drivers load blobs from /lib/firmware at probe time,
directly, with no udev involved, reading compressed files without
help. On metal, the need for this is not exotic: many NICs will not
link without their blob, and a machine whose network link needs
firmware that liken did not ship never reaches its cluster.

The full linux-firmware tree is about 743 MiB, and most of it
describes hardware that an x86 server kernel cannot drive (ARM SoCs,
phone parts, astronomy cameras). liken does not curate its way out of
this; it derives the set it needs instead. Every module names the
firmware it may request (in MODULE_FIRMWARE, readable with modinfo),
so the set to ship is the union over the module tree described above,
defined by the kernel build, not by anyone's judgment. Measured
against the current kernel pin, this derived set comes to about 206
MiB after deduplication, and half of that is a single directory:
nouveau's NVIDIA GSP blobs, 103 MiB that serve display paths a
headless OS does not use.

There is one named exception to pure derivation: the image ships
without nvidia, leaving out about 103 MiB of firmware, while keeping
the small GPU families (amdgpu, i915, xe, radeon, about 33 MiB
together, which are what make a console work on ordinary machines).
liken has no GPU-compute design yet. When it grows one, that future
milestone can re-decide this. Until then, the composable-release
design (milestone 22) is the option for anyone who needs more: an
nvidia-inclusive community image is a rebuild with one more directory,
not a fork.

Derivation gives a floor, not an exhaustive count: a few drivers
construct firmware names at runtime, and a request for a blob the
image lacks fails into kmsg, which the log relay already ships. This
case is reportable under the same say-what-would-fix-it rule as an
unclaimed device.

## CPU microcode

Microcode is load-bearing for security, not optional: Spectre-class
mitigations silently degrade on stale microcode, and Intel
increasingly forbids loading updates late. The loading convention is
its own: an uncompressed cpio holding
kernel/x86/microcode/GenuineIntel.bin and AuthenticAMD.bin, placed
ahead of the real initrd, at the point where the kernel looks before
decompressing anything. liken's boot entries already carry multiple
initrd= lines (the deployment layer already travels that way), so
microcode is one more line, first in order: a vendored artifact with
its own pin and fetch, never recomposed when the OS updates. QEMU's
-kernel path takes a single initrd, so the lab variant uses build-time
concatenation, which is how the early-cpio format is defined anyway.

Both vendors ship unconditionally: Intel's blob is 21 MiB, and AMD's
is one. Nothing that small needs a decision either way. On licensing,
microcode and most firmware are redistributable binaries under their
own terms (not GPL). They need notices entries in the licensing
domain, but no source-mirror obligation, because no source exists to
mirror. hwdata's pci.ids (milestone 11's naming database) follows the
same vendored-pin pattern.

## The budget

The additions total about 295 MiB (170 for the module tree, about 103
for firmware, 22 for microcode). This puts the whole slot payload,
system image, kernel, boot archive, microcode, and layer combined,
near 419 MiB against today's 512 Mi slots. That fits, but with thin
headroom, so two guards come with it. First, the scaffold's and dev
cluster's default slot size grows to 1 Gi, while the fleet is still
small enough that changing defaults costs nothing. Second, the release
build checks its artifact sizes against the declared slot size, so an
image that outgrows its slots fails the build, rather than surprising
someone at install time.

## Out of scope, deliberately

Updating the machine's own firmware (UEFI capsules, NIC NVRAM, SSD
firmware, through fwupd/LVFS) is a different job entirely. The items
this milestone adds are inert bytes, while fwupd is an agent whose
work reaches into the boot chain that liken owns. That work gets its
own milestone (33-firmware-updates.md), after bare metal exists to
learn from. The TPM needs no blobs (that belongs to the hardening
tier). IPMI/BMC sensors are just kernel modules that the tree above
already carries. ACPI quirks belong to the kernel.

Only real metal can prove some things: that the early cpio applies
(the kernel reports the microcode revision it runs), that a
firmware-hungry NIC links, and, eventually, with a GPU and its own
milestone, that a compute stack starts correctly. The shipping
mechanics all work correctly in the lab; proving them needs a
machine.
