# Upgrades under BIOS

Milestone 30 — Not started

liken's declarative upgrades (milestone 12) actuate through UEFI
firmware: the operator writes the new release into the inactive slot,
sets BootNext to try it exactly once, and promotes it into BootOrder
only when the new boot proves itself. That design leans on the
firmware for its two best properties — the one-shot trial and the
automatic fallback — and it assumes the firmware exists.

The liken.sh deployment broke that assumption in the most useful way
possible: Linode boots guests BIOS-style only, no UEFI available at
any price, and we chose to stay (liken.sh/README.md tells that story).
The machine boots today through its own GRUB — first stage in the MBR,
a BIOS boot partition, and a grub.cfg on the active slot — but nothing
can flip which slot boots next, so release upgrades on that machine
currently mean reinstalling the system disk. BIOS machines are not a
Linode quirk; they are old servers, cheap VMs, and other clouds'
legacy tiers, and an OS that can only upgrade itself where UEFI
exists is narrower than liken means to be.

The milestone: teach the upgrade path a second actuator. Where UEFI
writes firmware variables, BIOS rewrites what GRUB reads. GRUB's
environment block — a fixed-size file GRUB can read and write from
its own config, designed for exactly this kind of boot bookkeeping —
can carry the BootNext analogue: boot the new slot once, and unless
the new system marks itself proven, the next boot falls back to the
old one. Promotion is then an ordinary edit to the default entry.
The mechanics liken already owns (slots, digests, the staged/proven
lifecycle) don't change at all; only the last step, "make the
firmware prefer the new slot," grows a second dialect.

Open questions, deliberately unanswered here: how a machine knows
which regime it is in (the presence of efivarfs is probably the whole
test); where grub.cfg and the environment block should live so that
both slots can reach them (today grub.cfg sits on slot A, planted by
the liken.sh Makefile — a shared home may be more honest); whether
the installer should learn to lay down GRUB itself instead of leaving
it to a deployment's Makefile; and whether liken writes the
environment block directly (it is a 1 KiB block with a documented
format) or ships grub-editenv to do it.

One more capability belongs in this milestone's orbit: a BIOS machine
should notice and heal its own boot sectors. The liken.sh node's boot
code has been zeroed twice by Linode operations entirely outside the
machine (image deploys sanitize the MBR, and a failed deploy step can
do it again an hour later), and each time the fix was 440 bytes a
human restored over a rescue boot. A machine that owns its bootloader
can verify those bytes on every boot and put them back itself — the
same self-reliance the storage roles already practice, applied to the
one region of the disk nothing else watches.
