# Firmware guests for `firmware-operator` drills

Contents:

* A guest with Secure Boot keys
* A guest with capsule firmware
* The test catalog

Run every command here from `liken/`, as in `SKILL.md`. Stop a guest
by the PID of its QEMU process, as the "Start and stop guests" section
of `SKILL.md` shows, and never with `pkill -f`.

## A guest with Secure Boot keys

Use a guest with Secure Boot keys for drills of `firmware-operator`'s
database updates. These drills need a variable store that checks
signatures the way a real board does. Stop the guest, and enroll
Microsoft's keys in its variable store with Secure Boot off. Secure
Boot stays off because `liken`'s boot chain is not signed, and
liken-1 runs the same way. Then start the guest on the SMM build of
OVMF. `FIRMWARE=secureboot` selects that build, with a `q35` machine:

```bash
make -C dev-cluster enroll-keys NODE=node-5
setsid make -C dev-cluster run NODE=node-5 FIRMWARE=secureboot \
  CONSOLE=file:guests/node-5/console.log </dev/null >/dev/null 2>&1 &
```

`enroll-keys` seeds `dbx` with an older signed update from
Microsoft, from the LVFS cabinet that `DBX_SEED` in
`dev-cluster/Makefile` pins. So fwupd reports the version of that
update and offers a newer one, as on liken-1. When fwupd
does not recognize a `dbx`, it reports no version, and no catalog
release applies to it. The console shows `Secure boot disabled`.

To return the guest to the plain firmware, stop it, run
`make -C dev-cluster reset-nvram NODE=node-5`, and start it with the
default `FIRMWARE`. Run `reset-nvram` only while the guest is off,
because QEMU writes the variable store back when the guest exits.

## A guest with capsule firmware

Use a guest with capsule firmware for drills of `firmware-operator`'s
capsule updates. Debian's OVMF has no firmware device that fwupd can
update. So `FIRMWARE=capsule` boots an OVMF with one test device,
which `dev-cluster/firmware/capsule/build.sh` builds from the same
edk2 release. The device's version is the UEFI variable
`LabFmpVersion`, which starts at 1. The ESRT lists the device under
the GUID `b0792605-c842-408f-adf2-edfb07638e83`. The guest uses its
own `OVMF_VARS.fd`, so `liken`'s boot entries carry over, and the
guest boots from its disk as before.

Build the firmware once, which takes a few minutes. Then stop the
guest and start it on the new firmware:

```bash
dev-cluster/firmware/capsule/build.sh
pgrep -x qemu-system-x86 -a | grep 'node-[5]' | awk '{print $1}'
kill <pid>
setsid make -C dev-cluster run NODE=node-5 FIRMWARE=capsule \
  CONSOLE=file:guests/node-5/console.log </dev/null >/dev/null 2>&1 &
```

The build is a DEBUG build, and QEMU saves the firmware's debug log
to `dev-cluster/guests/<node>/ovmf-debug.log`. When FmpDxe rejects a
capsule, it writes the reason to that log. This firmware accepts a
capsule only through fwupd's EFI updater and `BootNext`. It rejects a
capsule that sets `PersistAcrossReset`, and most vendor capsules set
it, so only the lab's own capsules install on this firmware.
`dev-cluster/firmware/capsule/build.sh` explains why.

`FIRMWARE=capsule-on-disk` boots the same firmware with support for
capsule-on-disk. `CAPSULE_ON_DISK=1 dev-cluster/firmware/capsule/build.sh`
builds it into `dev-cluster/firmware/capsule/dist/on-disk/`. With
this firmware, fwupd stages a capsule as a file in
`\EFI\UpdateCapsule` and sets a bit in `OsIndications`. It creates no
boot entry and sets no `BootNext`. When the firmware accepted the
file, its log shows `capsule on disk: Success`. Run the two builds one
after the other, because they share one edk2 tree.

When you change a file that a build reads, such as `build.sh` or the
patch, the next `make run` of every capsule guest builds the firmware
again. Start one capsule guest, and wait until its QEMU runs. Only
then start the others, or the builds collide in the edk2 tree. The
same applies to the OS image after a change under `liken/`.

## The test catalog

The test catalog offers versions of the test device, 2 and 3 by
default. `dev-cluster/firmware/capsule/catalog.sh` builds it, and `make serve`
serves it next to the release channel. A `FirmwareCatalog` on the lab
points at `http://10.0.2.2:8017/firmware/firmware.xml.zst`. The
operator reads the jcat from the same URL with `.jcat` added.

fwupd rejects a catalog that is not signed by a key in
`/etc/pki/fwupd-metadata`, with the error
`signatures were required, but none supplied`. So a lab key signs
this catalog. `catalog.sh` creates the key once, in
`dev-cluster/firmware/capsule/signing/`, which git ignores. A scan
pod needs the certificate `signing/lab-catalog.pem` from that
directory.

Each run of `catalog.sh` signs the capsules again, so the SHA-256 of
each cabinet changes. A `Firmware` approval that pins the old SHA-256
then no longer matches. fwupd offers only a version above the version
that a guest runs. So a guest that installed version 3 needs a catalog
with higher versions:
`VERSIONS="4 5" dev-cluster/firmware/capsule/catalog.sh`.
