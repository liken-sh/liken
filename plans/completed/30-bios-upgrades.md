# Upgrades under BIOS

Milestone 30 — Completed. A machine that boots through BIOS upgrades
with the same one-shot trial and fallback that a UEFI machine uses.

liken's declarative upgrades (milestone 12) act through UEFI firmware.
The operator writes the new release into the inactive slot. The
firmware sets BootNext to try the new slot one time. The new slot
moves into BootOrder only after the new boot proves itself. This
design uses the firmware for two properties, the one-shot trial and
the automatic fallback, and it assumes that the firmware exists.

The liken.sh deployment has no UEFI firmware. Linode boots guests in
BIOS style only, and no Linode option gives a guest UEFI. The project
stays on Linode (liken.sh/README.md gives the reasons). BIOS machines
are not only a Linode condition. They are old servers, low-cost
virtual machines, and other clouds' legacy tiers. An OS that can
upgrade itself only where UEFI exists does not meet liken's goal.

This milestone adds a second actuator to the upgrade path. A UEFI
machine writes firmware variables. A BIOS machine rewrites what GRUB
reads. The proving lifecycle needs three actions from a firmware: try
a slot one time, continue to prefer the proven slot, and verify that
the preference holds. These three actions are now behind a small seam
(init/actuator.go).

The UEFI dialect uses BootNext and BootOrder, and the GRUB dialect
uses the environment block. `try_slot` gives the one-shot trial,
because grub.cfg reads it before it loads one byte of a kernel, in the
same way that firmware reads BootNext. `default_slot` gives the
standing preference. A `fallback=1` menu entry makes a slot that does
not load fall through to the other slot instead of stopping at a
prompt. The mechanics that liken already had, the slots, the digests,
and the staged and proven lifecycle, did not change.

The plan's open questions have these answers. The regime test looks
for /sys/firmware/efi, the same test that the installer and the facts
report use. GRUB's configuration and environment block are on their
own small filesystem: the `bootHome` storage role, FAT32, with the
label LIKEN-BOOT. It sits beside `biosBoot`, the raw partition that
holds GRUB's core image. A Machine spec that declares these two roles
declares that the machine boots through GRUB, so there is no separate
firmware field.

The installer writes the whole chain, and the liken.sh Makefile no
longer plants GRUB by hand. The chain starts from `grub-boot.img` and
`grub-core.img`. The grub/ domain vendors these two files from
Ubuntu's archive, and every release bundle carries them. liken writes
the environment block directly, because the block is 1 KiB with a
documented format. The tests compare the codec's fixtures with
grub-editenv's own output.

The healing capability landed with this milestone. Linode writes zeros
over MBR boot code under a running machine, so a heal at boot only is
not sufficient: a machine that goes down with a zeroed MBR never
starts again to heal itself. To assert the proven slot, liken now
derives the MBR's boot code, GRUB's core image, and grub.cfg again
from the proven slot's own artifacts, and writes again whatever does
not agree. It does this on every boot, and again before every reboot.

The lab drills both firmware regimes. `FIRMWARE=uefi` is the default.
`FIRMWARE=bios` asks QEMU for nothing more, which gives the lab
SeaBIOS. CI runs two smoke drills on every push. `smoke-uefi` boots
the image with -kernel. `smoke-bios` installs node-1 onto a blank disk
and boots the installed disk through the full GRUB chain.

The closing drills passed on the dev cluster: a forward roll onto the
inactive slot, a release broken on purpose that panicked, fell back,
and was rejected durably, and a boot-sector heal on a running machine
that would have failed on its next reboot.

To provision the liken.sh Linode again with all of this is follow-on
work. The deployment's media now installs under SeaBIOS with no root
privileges and no bootloader planted by hand.
