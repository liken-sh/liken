# Upgrades under BIOS

Milestone 30 — Landed

liken's declarative upgrades (milestone 12) act through UEFI firmware:
the operator writes the new release into the inactive slot, sets
BootNext to try it exactly once, and promotes it into BootOrder only
after the new boot proves itself. This design depends on the firmware
for its two best properties, the one-shot trial and the automatic
fallback, and it assumes that the firmware exists.

The liken.sh deployment broke that assumption, in a useful way.
Linode boots guests in BIOS style only, with no UEFI available at any
price, and the project chose to stay on Linode anyway
(liken.sh/README.md tells that story). BIOS machines are not a Linode
quirk. They are old servers, cheap virtual machines, and other
clouds' legacy tiers, and an OS that can only upgrade itself where
UEFI exists is narrower than liken means to be.

This milestone taught the upgrade path a second actuator. Where UEFI
writes firmware variables, BIOS rewrites what GRUB reads. The proving
lifecycle needs three actions from a firmware: try a slot once, keep
preferring the proven slot, and verify that the preference holds.
These three actions now live behind a small seam (init/actuator.go).
The UEFI dialect speaks BootNext and BootOrder, and the GRUB dialect
speaks the environment block. `try_slot` provides the one-shot trial
(grub.cfg consumes it before loading a single kernel byte, exactly as
firmware consumes BootNext). `default_slot` provides the standing
preference. A `fallback=1` menu entry makes a slot that will not even
load fall through instead of hanging at a prompt. The mechanics that
liken already owned (slots, digests, the staged/proven lifecycle) did
not change at all.

The open questions resolved as follows. The regime test checks for the
presence of /sys/firmware/efi, the same test that the installer and
the facts report already use. GRUB's configuration and environment
block live on their own small filesystem, the `bootHome` storage role,
FAT32, labeled LIKEN-BOOT, beside `biosBoot`, the raw partition
holding GRUB's core image. Declaring these two roles in a Machine's
spec is itself the declaration that the machine boots through GRUB,
with no separate firmware field needed. The installer lays down the
whole chain itself; the liken.sh Makefile's hand-planted GRUB step is
gone. The chain starts from `grub-boot.img` and `grub-core.img`,
vendored from Ubuntu's archive by the grub/ domain and carried in
every release bundle. liken writes the environment block directly: it
is a 1 KiB block with a documented format, and the codec's fixtures
are checked against grub-editenv's own output.

The healing capability landed too, sharpened by a fact learned the
hard way: Linode zeroes MBR boot code under running machines. So
healing only at boot is not enough, because a machine that goes down
with a zeroed MBR would never come back to heal it. Asserting the
proven slot now re-derives the MBR's boot code, GRUB's core image, and
grub.cfg from the proven slot's own artifacts, and rewrites whatever
disagrees. It does this on every boot, and again on the way down,
before every reboot.

The lab drills both firmware regimes. `FIRMWARE=uefi` is the default,
and `FIRMWARE=bios` asks QEMU for nothing extra, which is how the lab
gets SeaBIOS. CI runs two smoke drills on every push: `smoke-uefi`
boots the image using -kernel, and `smoke-bios` installs node-1 onto a
blank disk and boots the installed disk through the whole GRUB chain.
The milestone's closing drills all passed on the dev cluster: a
forward roll onto the inactive slot; a deliberately broken release
that panicked, fell back, and was durably rejected; and a boot-sector
heal performed on a running machine that would otherwise have failed
on its next reboot.

Re-provisioning the liken.sh Linode with all of this is its own
follow-on work. The deployment's media now installs under SeaBIOS with
no root privileges and no hand-planted bootloader, ready to found the
cluster again.
