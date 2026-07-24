# The installer speaks, and the hardware reports

Milestone 36 — Done

Milestone 35 closed with liken's first boot on real hardware, and the
machine taught three lessons in one afternoon. The lab's kernel builds
the virtio drivers in, so the lab never exercises the module path that
every real controller needs. A person stands at an install, but the
installer's terminal states were tuned for unattended lab guests: a
one-second message, then power off. And the hardest question a new
machine asks — which modules, which interface names, which disk paths
— had no answer short of three blind install cycles.

This milestone gives the installer a voice and gives the operator the
answers before the first install. It has five parts.

## The three-entry menu

The installer stick's menu grows from one entry per machine to two,
plus one entry for the stick itself:

    install as liken-1
    wipe and reinstall as liken-1
    liken hardware report

The first entry is `liken.install`, unchanged. The second is
`liken.reinstall`, which already exists in init but was reachable only
through systemd-boot's "e" key. The third is new: `liken.report`, a
boot that changes nothing on the machine.

The menu already runs with `timeout menu-force`: a person must pick,
forever. That is the consent model for the reinstall entry too. A
person who picks "wipe and reinstall as liken-1" at the keyboard has
said what the hand-edited `liken.reinstall` word used to say.

The report entry carries no `liken.machine=` identity, because the
report describes the hardware, not a machine in the deployment. Each
machine's two entries sort together, and the report entry sorts last.

## The hardware report

The report boot answers the question the testbed could not: what does
this machine need in its manifest? It produces one file,
`hardware-report.yaml`, on the root of the installation stick. The
file is a proposed machine manifest with generous comments: a valid
`Machine` document whose `spec.modules` names the drivers this
hardware wants, whose storage section lists every disk, and whose
comments carry the evidence — the PCI device each module claims, each
disk's size, model, and device path, each network interface's name,
MAC, and link state.

The report does not guess the interface names. It loads the drivers it
recommends and observes what appears. The names are only real after
the driver binds: `eth0` does not exist until `r8169` loads. The
modules come from the payload the stick already carries — the install
boot mounts `liken.sqfs` for its module tree, and the report boot does
the same. Loading a module changes only RAM, so the report keeps its
promise to change nothing on the machine.

The recommendation walks soft dependencies. The testbed's NIC needed
`realtek` loaded before `r8169`, a `softdep` relation that
`modules.dep` does not record. The report reads each module's
`softdep` information and recommends the full ordered list, so the
proposal says `[realtek, r8169]`, not `r8169` alone.

The last step prints the whole proposal to the console, with a note
that says where the file was written, and holds. When the operator
presses Enter, the machine reboots. The flow for a new machine is
three boots: report, then edit `machine.yaml` at a desk, then install.

## Attended boots end at a held console

The console hold from the first hardware install was never about
failure. The rule underneath is: a menu pick makes a boot attended,
and every terminal state of an attended boot ends at a held console.
The person proved they were present when they picked the entry.

Today the rule is inverted: a failed install holds the console, but a
successful one prints its message and powers off within the same
second. On real hardware, a dark screen and a dead machine could mean
"done" or "never started".

Every terminal state of the three menu entries now holds:

* Install and reinstall, success: "installed to slot A; remove the
  stick, then press Enter to power off; the next power-on boots from
  the disk." The stick-removal instruction is load-bearing: the stick
  is first in the boot order, so a power-on with the stick still in
  lands back at the menu.
* Install and reinstall, failure: already holds; unchanged.
* Report: "this report was written to the stick as
  hardware-report.yaml; press Enter to reboot."

Unattended boots — from disk, upgrade slots — keep their abrupt
semantics on purpose. Nobody watches them, and `panic=10` with the
fall-back slot is their recovery story. The hold already gives up when
no console opens, so a truly headless install still terminates.

## The unclaimed-hardware report learns softdeps

The running node's unclaimed-hardware report found the testbed's NIC
driver by modalias and said "declare r8169 in spec.modules". That
advice was correct and incomplete: without `realtek` declared first,
the NIC binds to the generic PHY. The report now walks the same
softdep information the hardware report uses, and its advice names
the full ordered list.

The declared-module loader itself stays explicit. It loads what a
manifest declares, in the declared order, and nothing else. liken has
no udev for the same reason: the manifest is the whole truth about
what a machine runs. The softdep knowledge improves the advice, not
the loader.

## The lab boots what hardware boots

All three hardware faults had one shape: the vendored kernel builds
virtio in (`CONFIG_VIRTIO_BLK=y`), so the lab never loads a storage or
network module, and a missing driver is invisible until a real machine
boots. QEMU can present the same hardware classes a real machine has:
an AHCI SATA controller (`CONFIG_SATA_AHCI=m`) and an e1000 NIC
(`CONFIG_E1000=m`).

A new smoke guest boots with its disks on AHCI and its uplink on
e1000, and its manifest declares `e1000` in `spec.modules` — the same
flow a real operator walks. The guest proves the boot-path storage
modules load, the storage wait outlasts link training, and the
declared-module path brings a real NIC up. The smoke also boots the
report entry and checks the proposal file on the stick names `e1000`
and the AHCI disk.

## The manual

The install guide becomes the story of a first machine: boot the
report, read `hardware-report.yaml`, write the manifest, boot the
install. Each terminal state's held message appears in the guide, so
the page describes exactly what the operator sees. The reinstall entry
gets its place in the guide for the second install of the same
machine.

## Slices

1. Attended terminal states: the hold moves from the failure paths to
   every terminal state of an attended boot, with the messages above.
2. Softdep reading in init, and the unclaimed-hardware advice names
   the full ordered list.
3. The report boot: `liken.report` in init, the proposal file, the
   console print, the hold.
4. The menu: two entries per machine plus the report entry, in
   `image/stick.go`.
5. The parity smoke guest in the dev-cluster lab.
6. The manual: the install guide rewritten around the three-boot flow.
