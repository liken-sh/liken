# Booting real silicon

Milestone 32 — Not started; designed ahead of the hardware

Every liken machine so far is a virtual one: the dev cluster's QEMU
guests and the liken.sh node on Linode. Bare metal adds exactly one
new class of obligation, and it is all firmware: the blobs real
hardware demands that no hypervisor guest ever sees. The mechanics
were designed (and sized) during milestone 11's device-management
drills; this milestone is where they get proven on a physical
machine.

## CPU microcode

Security-load-bearing, not optional: Spectre-class mitigations
silently degrade on stale microcode, and Intel increasingly forbids
loading updates late. The loading convention is its own: an
*uncompressed* cpio holding kernel/x86/microcode/GenuineIntel.bin
and AuthenticAMD.bin, placed ahead of the real initrd, where the
kernel looks before decompressing anything. liken's boot entries
already carry multiple initrd= lines (the deployment layer rides
that way), so microcode is one more line, first in order — a
vendored artifact with its own pin and fetch, never recomposed when
the OS updates. QEMU's -kernel path takes a single initrd, so the
lab variant is build-time concatenation, which is how the early-cpio
format is defined anyway.

Both vendors ship unconditionally: Intel's is 21 MiB, AMD's is one.
Nothing that small is worth a decision. Licensing note: microcode is
redistributable binary with terms (not GPL) — a notices entry, no
source-mirror obligation, because no source exists to mirror.

## Driver firmware (linux-firmware)

Runtime blobs the kernel loads from /lib/firmware when a driver
probes — directly, no udev, compressed files understood. Real
hardware needs them where it hurts: many NICs won't link without
their blob (a machine whose uplink needs firmware liken didn't ship
never reaches its cluster), and modern GPU drivers won't even bind
without theirs.

The full linux-firmware tree is ~743 MiB and mostly describes
hardware an x86 server kernel cannot drive (ARM SoCs, phone parts,
astronomy cameras). liken does not curate its way out; it derives.
Every module names the firmware it may request (MODULE_FIRMWARE,
readable by modinfo), so the set to ship is the union over the
module tree the image already ships — defined by the kernel build,
not by anyone's judgment. Measured against the current kernel pin,
that derived set deduplicates to ~206 MiB, and half of it is one
directory: nouveau's NVIDIA GSP blobs, 103 MiB serving display
paths a headless OS does not walk.

The decision: ship the derived set minus nvidia, about 103 MiB, with
the small GPU families kept (amdgpu, i915, xe, radeon — ~33 MiB
together, and they are what makes a console work on ordinary
machines). This is one named exception to pure derivation, made for
a reason: liken has no GPU-compute story yet, and when it grows one,
that milestone re-decides. Until then the composable-release design
(milestone 22) is the door for anyone who needs it — an
nvidia-inclusive image is a rebuild with one more directory, not a
fork.

Derivation is a floor, not an exhaustive census: a few drivers
construct firmware names at runtime, and a request for a blob the
image lacks fails into kmsg, which the log relay already ships —
reportable the same way unclaimed devices are, with the same
say-what-would-fix-it obligation.

## The budget

The additions land at ~273 MiB (170 MiB module tree from milestone
11's batteries-included decision, ~103 MiB firmware), putting the
whole slot payload — system image, kernel, boot archive, microcode,
layer — near 419 MiB against today's 512 Mi slots. That fits with
thin headroom, so two guards ride along: the scaffold's and dev
cluster's default slot size grows to 1 Gi while the fleet is small
enough that defaults are free to change, and the release build
checks its artifact sizes against the declared slot size, so an
image outgrowing its slots is a red build, never an install-time
surprise.

## Out of scope, deliberately

Updating the machine's own firmware — UEFI capsules, NIC NVRAM, SSD
firmware (fwupd/LVFS) — is a different job: liken already owns the
reboot orchestration such updates want, which makes fwupd a
plausible future feature slug, and that is exactly why it should
wait for the feature vocabulary rather than ride this milestone.
The TPM needs no blobs (hardening tier); IPMI/BMC sensors are just
kernel modules the batteries-included tree already carries; ACPI
quirks are the kernel's problem.

What only the metal can prove: that the early cpio applies (the
kernel reports the microcode revision it runs), that a
firmware-hungry NIC links, and eventually — with a GPU and its
milestone — that a compute stack stands up. None of it is
observable under QEMU, which is why this milestone waits for a
machine instead of pretending the lab settled it.
