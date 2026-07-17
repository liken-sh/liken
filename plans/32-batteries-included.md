# Batteries included

Milestone 32 — Not started; the design settled during milestone 11's
drills

One milestone owns everything the image must carry for hardware to
just work: the kernel's loadable modules, the firmware blobs drivers
demand, and CPU microcode. The deliverable is a published release
whose generic image boots a physical machine — a real NIC, a real
disk controller, a console on whatever GPU is soldered to the board
— with nothing to compose and nothing to rebuild. Every liken
machine so far is virtual (the dev cluster's QEMU guests, the
liken.sh node on Linode), and virtual machines need none of this;
this milestone is what stands between the channel's releases and the
first bare-metal install.

The principle throughout is the feature vocabulary's, applied to
hardware support: enabling is a runtime act, never an image rebuild.
Payloads ship inert and cost only disk — the OS runs from a
read-only image on the boot slot, so none of this touches the 1 GB
memory envelope that milestone 29 made the lab's standing proof.

## Kernel modules: the whole tree

Today the image prunes the kernel's modules to the boot set plus
whatever the baked machine manifests declare. That works only for
images built beside their manifests; a machine installed from the
public channel would find that no spec.modules edit can load a
module its release never carried, and "wait for a release" is no
answer to "I plugged in a serial adapter". So the image ships the
kernel build's entire module tree — ~170 MiB of already-compressed
modules, inert until declared, making milestone 11's user story
(status names the missing driver, the operator declares it, a
reboot applies it) a pure edit on any machine.

Shipping everything deletes machinery rather than adding it: the
build's union-of-declared pruning goes away, and the full
modules.alias table — which milestone 11's unclaimed-hardware
report needs to name candidate drivers — comes along for free.

## Driver firmware: derived, not curated

Real hardware's drivers load blobs from /lib/firmware at probe time
(directly — no udev involved, compressed files understood), and on
metal the need is not exotic: many NICs will not link without their
blob, and a machine whose uplink needs firmware liken didn't ship
never reaches its cluster.

The full linux-firmware tree is ~743 MiB and mostly describes
hardware an x86 server kernel cannot drive (ARM SoCs, phone parts,
astronomy cameras). liken does not curate its way out; it derives.
Every module names the firmware it may request (MODULE_FIRMWARE,
readable by modinfo), so the set to ship is the union over the
module tree above — defined by the kernel build, not by anyone's
judgment. Measured against the current kernel pin, that derived set
deduplicates to ~206 MiB, half of which is a single directory:
nouveau's NVIDIA GSP blobs, 103 MiB serving display paths a
headless OS does not walk.

The one named exception to pure derivation: ship without nvidia,
about 103 MiB of firmware, keeping the small GPU families (amdgpu,
i915, xe, radeon — ~33 MiB together, and they are what makes a
console work on ordinary machines). liken has no GPU-compute story
yet; when it grows one, that milestone re-decides. Until then the
composable-release design (milestone 22) is the door for anyone who
needs more: an nvidia-inclusive community image is a rebuild with
one more directory, not a fork.

Derivation is a floor, not an exhaustive census: a few drivers
construct firmware names at runtime, and a request for a blob the
image lacks fails into kmsg, which the log relay already ships —
reportable with the same say-what-would-fix-it obligation as an
unclaimed device.

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
Nothing that small is worth a decision. Licensing: microcode and
most firmware are redistributable binaries with terms (not GPL) —
notices entries in the licensing domain, but no source-mirror
obligation, because no source exists to mirror. hwdata's pci.ids
(milestone 11's naming database) rides the same vendored-pin
pattern.

## The budget

The additions land at ~295 MiB (170 module tree, ~103 firmware,
22 microcode), putting the whole slot payload — system image,
kernel, boot archive, microcode, layer — near 419 MiB against
today's 512 Mi slots. That fits with thin headroom, so two guards
ride along: the scaffold's and dev cluster's default slot size
grows to 1 Gi while the fleet is small enough that defaults are
free to change, and the release build checks its artifact sizes
against the declared slot size, so an image outgrowing its slots is
a red build, never an install-time surprise.

## Out of scope, deliberately

Updating the machine's own firmware — UEFI capsules, NIC NVRAM, SSD
firmware (fwupd/LVFS) — is a different job: liken already owns the
reboot orchestration such updates want, which makes fwupd a
plausible future feature slug, and that is exactly why it should
wait for the feature vocabulary rather than ride this milestone.
The TPM needs no blobs (hardening tier); IPMI/BMC sensors are just
kernel modules the tree above already carries; ACPI quirks are the
kernel's problem.

What only the metal can prove: that the early cpio applies (the
kernel reports the microcode revision it runs), that a
firmware-hungry NIC links, and eventually — with a GPU and its
milestone — that a compute stack stands up. The shipping mechanics
all rehearse fine in the lab; the proof needs a machine.
