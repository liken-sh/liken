# Updating the machine's own firmware

Milestone 33 — Proposed. It would add fwupd as a feature slug, so a
firmware update would use the rolling-reboot orchestration that liken
already has.

The firmware that milestone 32 ships is payload that the kernel
consumes: modules, driver blobs, and microcode, all inert bytes. This
milestone covers the other direction, an update to the firmware that
the machine itself runs: UEFI, NIC NVRAM, and SSD and dock firmware.
fwupd and the Linux Vendor Firmware Service own this area. The work
waits for experience with bare metal from milestone 32.

The work is not part of milestone 32's payload, because fwupd is not
inert payload. It is an agent. It has a daemon's memory cost and a
live trust relationship with LVFS, whose vendor-signed downloads sit
outside liken's own digest chain. Its job also reaches into the boot
chain that liken guards most carefully: it stages EFI capsules that
the firmware applies during a reboot, which touches the ESP, BootNext,
the slot machinery, and the one-shot trial arrangements of milestones
12 and 30. To ship this without a design would put an outside actor
inside liken's most guarded machinery.

The shape should be a feature slug (`fwupd: {}`), because the
integration is worthwhile. A firmware update is a staged change, and
it needs the rolling-reboot orchestration that liken already has, with
budgets, one leader at a time, and proving on the way back up. The
Machine would declare the firmware state it wants, and the rollout
conductor would sequence the reboots that apply it, the same way it
does for OS upgrades but at a lower layer. The design must answer how
capsule staging coexists with the slots' boot entries, and what
"proven" means for an update that the firmware applies itself.

Until this design exists, a simpler answer works today: fwupd runs as
a privileged workload, and a deployment that needs it can run one
without any involvement from liken.
