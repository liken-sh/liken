# A hung guest, and a `BOOT=kernel` guest on old code

Run every command here from `liken/`, as in `SKILL.md`. A path in a
`QEMU_EXTRA` value is relative to `dev-cluster/`.

## A guest that hangs after `reboot: machine restart`

When a guest hangs after `reboot: machine restart`, the fault is in
the firmware. Some edk2 releases, which
`dev-cluster/firmware/fetch.sh` names, read data that a kernel left
in memory as an IGVM memory map after a warm reset. It then
loops forever in PciHostBridgeDxe, before systemd-boot runs. So the
lab pins Debian's OVMF in `dev-cluster/firmware/`.
`dev-cluster/firmware/fetch.sh` explains the mechanism, and
`plans/completed/88-disruptions.md` records the evidence.

If a guest hangs anyway, check that its QEMU uses
`dev-cluster/firmware/dist/<version>/OVMF_CODE_4M.fd`, where
`<version>` is the content of `dev-cluster/firmware/VERSION`. Then
power-cycle the guest: kill its QEMU process and `make run` it again.

To read the CPU registers of a hung guest, start it with
`QEMU_EXTRA="-qmp unix:guests/<node>/qmp,server=on,wait=off"`, and
send `info registers -a` over QMP. The socket path is relative to
`dev-cluster/` because a socket path must be shorter than 108 bytes.

## A `BOOT=kernel` guest that reboots onto old code

QEMU reads `-kernel` and `-initrd` once, when the process starts. On
every guest reset, it restores them from its own ROM cache. So a
`BOOT=kernel` guest that reboots comes back on the initramfs that it
started with, even after make rebuilt the file. To give such a guest
new code, kill its QEMU process and start it again. A `BOOT=disk`
guest does not have this problem, because it loads from its own
slots. `make install` also boots through `-kernel`, but it powers off
at the end, so each install starts a new QEMU process.
