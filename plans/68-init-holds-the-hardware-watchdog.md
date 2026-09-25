# 68. Init holds the hardware watchdog

Milestone 68. Proposed 2026-09-25. A machine that hangs without a
panic stays hung until a person cuts its power, and it leaves no
record of why. This milestone closes that gap in two layers. The
kernel turns a lockup into a panic, which milestone 35 already
records. Init holds the chipset's hardware watchdog, which resets a
machine that the kernel itself cannot recover.

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
vendored kernel (7.2.0) builds both lockup detectors, but it builds
them with `CONFIG_BOOTPARAM_SOFTLOCKUP_PANIC=0` and without
`CONFIG_BOOTPARAM_HARDLOCKUP_PANIC`. A detector that fires prints a
warning and the machine keeps its hang.

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

## Part two: the drivers

The chipset's watchdog is a countdown timer that runs outside the CPU.
When the count reaches zero, the chipset resets the machine, the same
as the reset button. The kernel exposes it as `/dev/watchdog0`
through the watchdog core (`CONFIG_WATCHDOG_CORE=y`). The drivers
ship as modules in the vendored kernel:

| Chipset | Parent module | Watchdog module |
| --- | --- | --- |
| Intel, SMBus generation (Cannon Point and later) | `i2c_i801` | `iTCO_wdt` |
| Intel, LPC generation (Gemini Lake and earlier) | `lpc_ich` | `iTCO_wdt` |
| AMD FCH | none | `sp5100_tco` |
| QEMU and KVM guests | none | `i6300esb` |

On Intel, the timer is not a PCI device of its own. The SMBus or LPC
driver reads the timer's address from the chipset and creates a
platform device for `iTCO_wdt`, so the parent must load first. The
hardware report already names these parents: on the machines above,
`status.hardware.unclaimed` lists `i2c_i801`, `lpc_ich`, or
`sp5100_tco` as the candidate for the SMBus or LPC controller.

All five modules go into `image/etc/liken/modules.conf`, parents
before `iTCO_wdt`. The protection is a property of the OS, not of a
workload, so it belongs in the fixed list with `efi-pstore`, not in
each Machine's `spec.modules`. A PCI driver with no matching device
loads and binds nothing. `sp5100_tco` on an Intel machine refuses
with `ENODEV`, and init reports that in one console line, the same as
`efi-pstore` on a BIOS machine. The image build ships the five module
files through the same list.

Some firmware locks the timer off with the chipset's `NO_REBOOT`
strap. `iTCO_wdt` then refuses to register and prints `unable to reset
NO_REBOOT flag, device disabled by hardware/BIOS`. The machine then
has no `/dev/watchdog0`, and init reports the watchdog as absent
(part four). Part one still protects that machine from a kernel
lockup.

## Part three: init resets the timer

Init opens `/dev/watchdog0` after the fixed module list loads and
before k3s starts. It sets the timeout with `WDIOC_SETTIMEOUT`, then
resets the count with `WDIOC_KEEPALIVE` at a quarter of the timeout.
It reads the driver's identity and its boot status with
`WDIOC_GETSUPPORT` and `WDIOC_GETBOOTSTATUS` before the first reset.

**Init holds the device, not a pod.** A `DaemonSet`, the machine
operator included, runs only after k3s, containerd, and the scheduler
work, which leaves the boot and the shutdown uncovered. It also closes
the device on every drain, image update, and eviction. With a magic
close, each one stops the timer and removes the protection. Without
one, the chipset resets the machine in the middle of a drain, before
init makes the disks read-only. Init runs from the first second of
boot to the reboot syscall, and it depends on nothing that the
cluster provides.

**The reset proves that init runs, and nothing more.** The goroutine
that resets the count runs when the kernel schedules tasks and the Go
runtime runs init. That is the failure this milestone targets. Init
does not make the reset depend on k3s, the API, or the node's
condition. A cluster-wide outage, for example an etcd quorum loss,
would then stop the resets on every machine at once, and the whole
fleet would reset together while it had nothing wrong with its
kernels.

**The timeout is 60 seconds.** It must exceed the kernel's own path,
so that a kernel lockup produces a panic and a `lastCrash` record
before the chipset acts. That path is 20 seconds to detect a soft
lockup, then the ten seconds of `panic=10`, 30 seconds in all. At 60
seconds the chipset acts only when the kernel cannot. The driver converts the
requested timeout to the chipset's own count, and init reads the
value back from the ioctl, because a driver can round it.

**The timer is not a machine-plane component.** A component restarts
on failure and stops in `plane.shutdown`. The timer must do neither.
Init starts its goroutine directly, and the shutdown sequence below
controls it.

## Part four: the shutdown and the power-off

**A reboot keeps the timer armed.** `rebootMachine` stops the resets
before `killEverything` and sets the timeout to a shutdown budget of
two minutes with one final `WDIOC_SETTIMEOUT`. The device stays open
with no magic close. If the grace period, the plane shutdown, the
quiesce, or the sync hangs, the chipset resets the machine when the
budget runs out. This changes one documented behavior: a failed
reboot syscall no longer leaves the machine waiting for a person. The
chipset resets it.

**A power-off disarms the timer.** `powerOff` serves the fail-stop
refusal and the installer. A fail-stop powers off on purpose, because
the boot could not tell which configuration is its own. A chipset
reset would boot that configuration again. So `powerOff` writes `V`
and closes the device, the magic close, before it quiesces the disks.
The vendored kernel does not set `CONFIG_WATCHDOG_NOWAYOUT`, so the
driver stops the timer.

**The installer and the hardware report do not open the device.**
They run from the stick and change nothing that a reset protects.

## Part five: what the next boot reports

`WDIOC_GETBOOTSTATUS` returns `WDIOF_CARDRESET` when the timer reset
the machine. `iTCO_wdt` reads the chipset's second-timeout status bit
for this. `sp5100_tco` and `i6300esb` read their own equivalents,
and drills one and five confirm each driver's report. The bit clears when the driver reads it, so init preserves the fact
on `machineState` in the same crash store that milestone 35 uses, as
a record named for the boot that read it. Each boot derives the
summary from the store again, under the reconstructibility rule.

Two new status fields carry it:

* `status.watchdog` describes this boot: the driver's identity, the
  timeout, and one of three states. `Armed` means init resets the
  timer. `Absent` means the machine has no watchdog device.
  `Refused` means a device exists but init could not open or
  configure it, and the message gives the kernel's error text.
* `status.lastWatchdogReset` gives the time of the boot that read
  the bit and the driver that reported it. The reset time itself is
  unknown, because nothing ran at that moment to write it down.

Neither field changes `Ready`. A machine with no watchdog works, and
a fleet of such machines is a normal case, not a fault. The schema
change in `api/` is the necessary half, because the API server prunes
any field that the schema does not declare.

## The drills

1. **A starved timer resets a QEMU guest.** The lab adds `-device
   i6300esb` to `QEMU_EXTRA`. QEMU's default watchdog action is a
   reset. A `FAULT=starve-watchdog` release opens the device and never
   resets it. The guest must reset within 60 seconds, and the next
   boot must report `lastWatchdogReset` and no `lastCrash`.
2. **A hung shutdown resets a QEMU guest.** A `FAULT=hang-shutdown`
   release blocks in `rebootMachine` after the quiesce. The guest must
   reset within the two-minute budget.
3. **A fail-stop stays off.** A guest boots a manifest that it cannot
   claim, fail-stops, and powers off. It must stay off for longer than
   the timeout.
4. **The lockup values read back.** Every guest reports the three
   values from part one in `status.sysctls`. The lockup paths are
   kernel code, and this milestone does not drill a real lockup.
5. **The real chipsets.** One machine of each chipset above boots the
   release: `status.watchdog` must show `Armed` or `Absent`, with the
   `NO_REBOOT` refusal named when it is `Absent`. On one testbed
   machine per chipset, the starve release must reset the board, and
   the next boot must report it.

## Decisions

* **Init, not a `DaemonSet`.** Part three gives the reasons: coverage
  from boot to reboot, no dependency on the cluster, and no false
  resets from a pod's normal life.
* **No health gate.** A gate on k3s or the API turns a cluster outage
  into a fleet-wide reset.
* **The fixed module list, not `spec.modules`.** The protection is the
  OS's, and a person must not need to know a chipset's driver name to
  get it.
* **60 seconds, then two minutes at shutdown.** The first value leaves
  the kernel room to panic first. The second covers the ten-second
  grace, the ten-second plane shutdown, and a slow sync.
* **A power-off disarms, and a reboot does not.** A fail-stop is the
  one case where the machine must stay off.

## Open questions

* **An opt-out.** A board whose firmware resets the timer wrongly, or
  a machine under a debugger, may need init to leave the device
  closed. A `spec.watchdog.enabled` field on the Machine is the likely
  shape. This milestone ships without one until a board needs it.
* **More than one watchdog.** A machine with two devices gets
  `/dev/watchdog0` only. No machine in the lab has two.

## Left for later: a workload's lease on the timer

A workload could define what healthy means for its own machine. A
media player, for example, could declare the machine broken when its
display pipeline hangs, even though the kernel runs normally.

`/dev/watchdog0` opens for one process at a time, which matches an
exclusive DRA allocation, but the pod must not hold the device. Every
reason in part three applies to it. The shape that keeps init's
coverage is a lease:

* A pod claims a `watchdog` device through the machine operator's DRA
  driver. The claim delivers a lease, not the device node.
* Init keeps resetting the hardware timer as before. While a lease
  exists, init also watches it. If the pod stops renewing the lease,
  init reboots the machine through `rebootMachine` and records the
  claim's name as the reason.
* A pod that releases its claim ends the lease, and the machine
  returns to the baseline.

A bad pod can then reboot only its own machine, only cleanly, and
only with a recorded reason. The hardware timer still acts when init
itself cannot. This needs its own milestone, after this one is
proven.
