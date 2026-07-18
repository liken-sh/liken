# Updating the machine's own firmware

Milestone 33 — Not started; waits on bare-metal experience (32)

The firmware milestone 32 ships is payload the kernel consumes —
modules, driver blobs, microcode, all inert bytes. This milestone is
about the other direction: updating the firmware the *machine* runs,
UEFI itself, NIC NVRAM, SSD and dock firmware, the territory
fwupd and the Linux Vendor Firmware Service own.

It is deliberately not part of batteries-included, because fwupd is
not a battery: it is an agent, with a daemon's memory cost, a live
trust relationship with LVFS (vendor-signed downloads outside
liken's digest chain), and a job — staging EFI capsules the firmware
applies during a reboot — that reaches into the boot chain liken
most carefully owns: the ESP, BootNext, the slot machinery, the
one-shot trial arrangements of milestones 12 and 30. Shipping that
un-designed would put an outside actor inside liken's most guarded
machinery.

The shape it should eventually take is a feature slug (`fwupd: {}`),
because the integration is genuinely attractive: a firmware update
is a staged change that wants exactly the rolling-reboot
orchestration liken already has — budgets, one leader at a time,
proving on the way back up. Declaring desired firmware state on the
Machine and letting the rollout conductor sequence the applying
reboots is the same story as OS upgrades, told about a lower layer.
Designing that means answering how capsule staging coexists with
the slots' boot entries and what "proven" means for an update the
firmware applies itself.

Until then, the exercise-for-the-reader answer works today: fwupd
runs as a privileged workload, and a deployment that needs it can
run one without liken's involvement.
