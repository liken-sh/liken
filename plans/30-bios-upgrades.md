# Upgrades under BIOS

Milestone 30 — Landed

liken's declarative upgrades (milestone 12) actuate through UEFI
firmware: the operator writes the new release into the inactive slot,
sets BootNext to try it exactly once, and promotes it into BootOrder
only when the new boot proves itself. That design leans on the
firmware for its two best properties — the one-shot trial and the
automatic fallback — and it assumes the firmware exists.

The liken.sh deployment broke that assumption in the most useful way
possible: Linode boots guests BIOS-style only, no UEFI available at
any price, and we chose to stay (liken.sh/README.md tells that story).
BIOS machines are not a Linode quirk; they are old servers, cheap VMs,
and other clouds' legacy tiers, and an OS that can only upgrade itself
where UEFI exists is narrower than liken means to be.

The milestone taught the upgrade path a second actuator. Where UEFI
writes firmware variables, BIOS rewrites what GRUB reads. The three
acts the proving lifecycle needs from a firmware — try a slot once,
keep preferring the proven slot, verify that preference holds — now
live behind a small seam (init/actuator.go), with the UEFI dialect
speaking BootNext and BootOrder and the GRUB dialect speaking the
environment block: `try_slot` is the one-shot (grub.cfg consumes it
before loading a single kernel byte, exactly as firmware consumes
BootNext), `default_slot` the standing preference, and a `fallback=1`
menu so a slot that won't even load falls through instead of hanging
at a prompt. The mechanics liken already owned (slots, digests, the
staged/proven lifecycle) didn't change at all.

The open questions resolved as follows. The regime test is the
presence of /sys/firmware/efi, the same test the installer and the
facts report use. GRUB's config and environment block live on their
own small filesystem — the `bootHome` storage role, FAT32, labeled
LIKEN-BOOT — beside `biosBoot`, the raw partition holding GRUB's core
image; declaring those two roles in a Machine's spec *is* the
declaration that it boots through GRUB, with no separate firmware
field. The installer lays the whole chain down itself (the liken.sh
Makefile's hand-planted GRUB is gone), from `grub-boot.img` and
`grub-core.img` vendored from Ubuntu's archive by the grub/ domain
and carried in every release bundle. And liken writes the
environment block directly — it is a 1 KiB block with a documented
format, and the codec's fixtures are checked against grub-editenv's
own output.

The healing capability landed too, made sharper by a fact learned the
hard way: Linode zeroes MBR boot code *under running machines*, so
healing only at boot is not enough — a machine that goes down with a
zeroed MBR never comes back to heal it. Asserting the proven slot now
re-derives the MBR's boot code, GRUB's core image, and grub.cfg from
the proven slot's own artifacts and rewrites whatever disagrees, on
every boot and on the way down before every reboot.

The lab drills both firmware regimes (`FIRMWARE=uefi` is the default;
`FIRMWARE=bios` asks QEMU for nothing, which is how you get SeaBIOS),
and CI runs two smoke drills on every push: `smoke-uefi` boots the
image via -kernel, and `smoke-bios` installs node-1 onto a blank disk
and boots the installed disk through the whole GRUB chain. The
milestone's closing drills all passed on the dev cluster: a forward
roll onto the inactive slot, a deliberately broken release that
panicked, fell back, and was durably rejected, and a boot-sector heal
under a running machine that would otherwise have been its last
reboot.

Re-provisioning the liken.sh Linode with all of this is its own
follow-on work: the deployment's media now installs under SeaBIOS
with no root privileges and no hand-planted bootloader, ready to
found the cluster again.
