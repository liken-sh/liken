# Updating the machine's own firmware

Milestone 33 — Not started; waits on bare-metal experience (32)

The firmware that milestone 32 ships is payload that the kernel
consumes: modules, driver blobs, microcode, all inert bytes. This
milestone covers the other direction: updating the firmware that the
machine itself runs, UEFI itself, NIC NVRAM, and SSD and dock
firmware. This is the territory that fwupd and the Linux Vendor
Firmware Service own.

This work is deliberately not part of batteries-included, because
fwupd is not inert payload. It is an agent, with a daemon's memory
cost, a live trust relationship with LVFS (vendor-signed downloads
that sit outside liken's own digest chain), and a job, staging EFI
capsules that the firmware applies during a reboot, that reaches into
the boot chain liken guards most carefully: the ESP, BootNext, the
slot machinery, and the one-shot trial arrangements of milestones 12
and 30. Shipping this without a design would put an outside actor
inside liken's most guarded machinery.

The eventual shape should be a feature slug (`fwupd: {}`), because the
integration is genuinely worthwhile: a firmware update is a staged
change that needs exactly the rolling-reboot orchestration that liken
already has, with budgets, one leader at a time, and proving on the
way back up. Declaring the desired firmware state on the Machine and
letting the rollout conductor sequence the applying reboots works the
same way as OS upgrades, at a lower layer. Designing this means
answering how capsule staging coexists with the slots' boot entries,
and what "proven" means for an update that the firmware applies
itself.

Until this design exists, a simpler answer works today: fwupd runs as
a privileged workload, and a deployment that needs it can run one
without any involvement from liken.
