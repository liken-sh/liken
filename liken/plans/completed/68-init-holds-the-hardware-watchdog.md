# 68. Init holds the hardware watchdog

Milestone 68. Proposed 2026-09-25. A machine that hangs without a
panic stays hung until a person cuts its power, and it leaves no
record of why. This milestone closes that gap in two layers. The
kernel turns a lockup into a panic, which milestone 35 already
records. Init holds the chipset's hardware watchdog, which resets a
machine that the kernel itself cannot recover.

Status on 2026-11-17: closed. Part one is built for two of its three
values (41170ba8). `machine.OSSysctls` holds `kernel.softlockup_panic`
and `kernel.hardlockup_panic`, and on that day ten machines of a
production fleet that set them through `spec.sysctls` read both back as
`1` on kernel 7.3.1 with `SysctlsApplied` true. `kernel.nmi_watchdog`
is not in the table: the kernel's default is already `1`, the entry
only makes the value read back, and whether a QEMU guest with no
performance counter accepts the write was not checked. No drill of
this milestone has run. Parts two to five, the hardware watchdog, are
not built.
[Milestone 98](../98-init-holds-the-hardware-watchdog.md) holds them,
with the drills, the decisions, the open questions, and the workload's
lease that is left for later.

## The problem

Milestone 35 left one failure out: "a machine that stops without a
panic records nothing, and only a hardware watchdog finds it." A
production fleet on 2026-09-24 showed what that costs. An Intel N100
leader stopped at one moment with no warning. Its pods' logs ended in
the same second, its kernel log held no error, and its memory, CPU,
and temperature were normal up to the end. The machine did not
answer ARP, but its power LED was on and its link LED showed
traffic. It stayed that way for 43 hours, until a person cut its
power. `status.lastCrash` stayed empty, because the kernel never
panicked. The only trace was the SSD's unsafe-shutdown count, which
went up by one when the person cut the power.

The machine boots with `panic=10` and `kernel.panic_on_oops=1`, so a
panic or an oops reboots it in ten seconds. A lockup is neither. The
vendored kernel was 7.2.6 when this plan was written, and that build
had both lockup detectors, with `CONFIG_BOOTPARAM_SOFTLOCKUP_PANIC=0`
and without `CONFIG_BOOTPARAM_HARDLOCKUP_PANIC`. A detector that fires
prints a warning and the machine keeps its hang. `liken/kernel/VERSION`
names the kernel that `liken` vendors now, and the `config-*` file in
that release's modules package gives its current settings.

A hang below the kernel is worse. If a CPU stops completely, or the
firmware holds it in system management mode, no kernel code runs, and
no detector fires. Only a timer outside the CPU can act.

## Part one: a lockup becomes a panic

`machine/sysctldefaults.go` gains three entries:

| Parameter | Value | Why |
| --- | --- | --- |
| `kernel.softlockup_panic` | `1` | A CPU that stays in kernel mode for more than 20 seconds (twice `kernel.watchdog_thresh`) panics instead of printing a warning. |
| `kernel.hardlockup_panic` | `1` | A CPU that runs with interrupts off panics. The hard-lockup detector runs on the NMI watchdog (`CONFIG_HARDLOCKUP_DETECTOR_PERF=y`). |
| `kernel.nmi_watchdog` | `1` | The kernel's default is already `1`. The entry makes the value read back in `status.sysctls`, so a person can see that the hard-lockup detector runs. |

These are the same argument as `kernel.panic_on_oops`: a panic
reboots into the proven slot, and pstore carries the log across the
reboot. A hang keeps neither.

`kernel.hung_task_panic` stays off. A task that waits on a slow iSCSI
or NFS server trips it, and a storage outage would then reboot every
machine that mounts from that server.

**Evidence already in hand.** A production fleet of nine machines
applied these three values through `spec.sysctls` on 2026-09-25. All
three read back as `1` on every machine, `SysctlsApplied` stayed
true, and no machine had a pending disruption. The fleet covers four
chipsets: Intel Cannon Point-LP, Alder Lake-N, and Gemini Lake, and
an AMD FCH. So the values apply live on the shipped kernel, with no
reboot.
