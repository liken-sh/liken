# 05, Applying updates

Proposed on 2026-10-20. Not built. This plan applies the version that
[plan 04](completed/04-approval.md) approves, on one machine at a time. Before
any machine writes, a rehearsal runs the install against a copy of
the machine's firmware variables and records in status exactly what
the install would write. The parts that write the boot chain stay in
the OS, in `liken`'s
[milestone 33](../../liken/plans/33-firmware-updates.md), which also
lists what the OS must build first. The rehearsal ran by hand on
liken-1, under [The rehearsal on liken-1](#the-rehearsal-on-liken-1).
Real writes ran on a lab guest under OVMF, under [The real writes
under OVMF](#the-real-writes-under-ovmf), and one real write ran on
stick-1, under [The real write on stick-1](#the-real-write-on-stick-1).

## The problem

A firmware update cannot be undone. A machine has two boot slots for
its OS, so a bad release falls back to the other slot, but a machine
has one variable store and one board firmware. fwupd has no dry run:
`fwupdtool install` writes the device. And a person who approves a
version in plan 04 sees a version string and a cabinet, not what the
install changes on the machine.

Two kinds of update reach `liken` machines from LVFS, and they write
different things:

- **The Secure Boot databases.** fwupd's `uefi_dbx`, `uefi_db`, and
  `uefi_kek` plugins add entries to `dbx`, `db`, or `KEK`. Every
  update that a catalog offered to liken-1 and stick-1 in the plan 03
  drills was of this kind.
- **Capsules.** fwupd's `uefi_capsule` plugin updates the board
  firmware, and other plugins update devices such as SSDs and docks.

## What each kind writes

These facts come from the source of fwupd 2.1.1, the version in the
scan image, and the rehearsal confirmed the first one.

A Secure Boot database update makes one write: an authenticated
append to its own variable, with the attributes `0x67` (append,
time-based authenticated, runtime, boot service, non-volatile). It
writes no `BootNext`, no `BootOrder`, no `Boot####`, and nothing on
the ESP. The firmware checks the append's signature against `KEK`, or
against `PK` for a `KEK` update, and refuses an append that does not
verify. fwupd marks the device as needing a reboot, and the new
entries take effect at the next boot.

A capsule writes the variables that milestone 33 is about. In NVRAM
mode, fwupd creates a boot entry named "Linux Firmware Updater" and
sets `BootNext` to it. With the `modify-bootorder` quirk, which fwupd
applies to Lenovo, Dell, ASUS, Framework, and HP machines, it also
appends the entry to `BootOrder`, and it never removes the entry.
Capsule-on-disk writes only `OsIndications`, and a file on the ESP of
the next boot option.

So the Secure Boot databases never touch a variable that `init`
reads or writes, and capsules do. This plan applies the first kind
before the second. Capsules wait for milestone 33's shim and its rule
of one change per reboot.

## The steps

1. **Rehearse.** When a machine's device runs another version than
   the approved one, the operator runs a rehearsal pod on that
   machine. The pod copies the firmware's variables and tables into
   memory and runs `fwupdtool install` against the copy. The operator
   compares the copy before and after, and records the difference in
   the machine's entry: each variable that changed, the bytes
   written, and the free space the variable store needs and has. A
   rehearsal that changed anything the kind does not allow fails the
   approval for that machine, and nothing writes.
2. **Request a turn.** A machine with a passed rehearsal asks the OS
   for a rollout turn, through the interface that milestone 33 adds.
3. **Apply.** The rollout conductor grants the turn and drains the
   machine. The write is the rehearsal's difference, and nothing
   else: for a Secure Boot database, the one authenticated append the
   rehearsal recorded. The machine reboots inside the turn.
4. **Prove.** The next scan of plan 03 shows the approved version, and
   the machine joins its cluster again. A failed write has no earlier
   state to return to, so the operator records it in status with an
   `Event`, and nothing runs it again on its own.

## The rehearsal

fwupd reads the firmware through three paths that the environment
variable `FWUPD_SYSFSFWDIR` redirects together: the variable store,
the ESRT, and the DMI tables. A rehearsal points it at a copy:

- The pod mounts the host's `/sys/firmware` read-only, with
  `recursiveReadOnly: Enabled`, so the `efivars` mount under it is
  read-only too. It is not privileged. It drops every capability and
  adds `DAC_READ_SEARCH`, which reading the variables needs.
- It copies `efi/efivars`, `efi/esrt`, and `dmi/tables` into an
  `emptyDir` in memory.
- It runs `fwupdtool refresh` with the catalog file the machine's
  last scan read, and its jcat, so fwupd offers the same releases the
  scan saw.
- It runs `fwupdtool install` with the approved cabinet and the
  device's fwupd ID, with `FWUPD_SYSFSFWDIR` at the copy,
  `FWUPD_UEFI_ESP_PATH` at an empty directory, and
  `FWUPD_LOCALSTATEDIR` in memory.
- It reports the name, size, and SHA-256 of each variable before and
  after, the files written under the ESP path, and fwupd's
  `RequiredFree`.

The operator then checks the difference against the kind. For a
Secure Boot database: exactly one variable changed, it is the
device's own variable, it grew, and nothing was written on the ESP.
The bytes it grew by are the append, and the apply writes those bytes
and no others.

fwupd exits 1 at the end of a database rehearsal, with `failed to
reload: SignatureListSize invalid: 0x0`. A plain file stores the raw
authenticated append, where firmware verifies it and merges its
entries into the list, so fwupd's read of the variable after its write
fails. The check reads the difference, not fwupd's exit code, and it
accepts this one message only after the write that the difference
shows.

A rehearsal cannot prove three things. The copy verifies no
signature, so it cannot show that the firmware accepts the append.
It cannot show what the firmware does with its store as it fills,
such as garbage collection. And it cannot show that the machine
boots after the write. The [proof before the first real
write](#proof-before-the-first-real-write) covers those.

## The rehearsal on liken-1

Run by hand on 2026-10-20, on liken-1 (a GMKtec NucBox G3S, BIOS 5.27
of 2025-08-26) at `liken` 2026.10.20-001, with the scan image
`ghcr.io/liken-sh/firmware-operator-scan:2026.10.20-001-dev-004-ce6d6341`.
Secure Boot is off, the machine is not in setup mode, and Microsoft's
`PK` is enrolled.

The update was `dbx` 20260402 to 20260707, of the component
`com.microsoft.dbx.x64-kek2023.firmware`, from the cabinet with the
SHA-256 `1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5`
(41,456 bytes).

- The pod copied 70 variables. After the install, it still had 70.
- `dbx` grew from 21,352 to 38,612 bytes: 17,260 bytes, which are 4
  bytes of attributes and the 17,256-byte authenticated payload. The
  other 69 variables were the same byte for byte. Nothing was written
  under the ESP path.
- The payload carries one signature list of 291 SHA-256 entries. Its
  signer is "Microsoft Windows UEFI Key Exchange Key", issued by
  "Microsoft Corporation KEK 2K CA 2023". liken-1's `KEK` holds that
  CA, valid until 2038, and "Microsoft Corporation KEK CA 2011", which
  expired in June 2026. So the firmware should accept the append. This
  was read from the certificates and not proved on the firmware.
- fwupd asked for 24,629 bytes free. liken-1's variable store is
  192K, with 65.6K free.
- The real `dbx` had the same SHA-256 before and after the rehearsal.

The 17,256 bytes are also the `size` that plan 03's status reports for
this release. The catalog's size is the size of the payload, not the
cabinet.

## The real writes under OVMF

Run by hand on 2026-10-20, on the lab's `node-1`, at a development
build of `liken` 2026.10.19-005, with the scan image of the rehearsal
on liken-1. The guest ran Ubuntu's OVMF 2025.11 build with SMM
(`OVMF_CODE_4M.secboot.fd`, on a `q35` machine with `smm=on`), the
build that verifies authenticated writes in protected code.
`virt-fw-vars` enrolled Microsoft's OEM `PK`, both `KEK`s, and the
`db` certificates, and set `SecureBootEnable` to false. It also seeded
`dbx` with liken-1's 20260402 list, so fwupd read the same version as
on liken-1. In the guest, `SecureBoot` and `SetupMode` both read 0,
the state of liken-1.

1. **The rehearsal.** The same pod as on liken-1 gave the same
   result: `dbx` grew from 21,352 to 38,612 bytes in the copy, the
   other 46 variables did not change, nothing was written under the
   ESP path, fwupd asked for 24,629 bytes free, and fwupd exited 1
   with the same message.
2. **A tampered append.** A privileged pod wrote the recorded
   17,260 bytes with one byte of the last hash changed. The write
   failed with `EACCES`, which is how the kernel reports the
   firmware's `EFI_SECURITY_VIOLATION`, and `dbx` did not change.
3. **The recorded append.** The same pod wrote the recorded bytes in
   one `write` through a file opened with `O_APPEND`. The write
   succeeded. `dbx` grew from 21,352 to 21,476 bytes, not by 17,260,
   because the firmware merges the signed list into the stored one
   and keeps no duplicate. The payload's 291 hashes held 2 that
   20260402 did not, and the new `dbx` holds 445 entries with no
   duplicate.
4. **The reboot.** A warm reboot kept the new `dbx`, byte for byte,
   and `liken` booted. fwupd, reading the real store, reported `dbx`
   version 20260707 and offered no `dbx` update.
5. **fwupd's own write.** On the 20260402 store again, a privileged
   pod ran `fwupdtool install` against the real store. It exited 0,
   changed only `dbx`, wrote nothing under the ESP path, and set the
   immutable flag again. The new `dbx` had the same SHA-256 as the
   recorded append's, `a87a88a3...`.

Three facts from these runs change the design:

- **The real store grows less than the copy.** The copy appends the
  signed list, and the firmware merges it. So the proof compares the
  version from the next scan, not the size or the bytes of `dbx`.
  fwupd's `RequiredFree` asked for much more room than the merged
  write took. The check stays, because it errs toward refusing a
  write.
- **fwupd clears the immutable flag only under `/sys`.** A path that
  does not start with `/sys` is not bare metal to fwupd
  (`fu-linux-efivars.c` in fwupd 2.1.1), so it skips the flag, and
  the write fails with `EPERM`. A pod that runs fwupd for real must
  mount `efivarfs` at `/sys/firmware/efi/efivars`. The rehearsal is
  not affected, because its copy has no flags.
- **The write is one `write` call.** `efivarfs` refuses a seek, so
  `dd` fails before it writes. The 4 attribute bytes and the payload
  go in one call, as fwupd does.

This OVMF build also enrolls `KEK` 2023 by itself at boot when the
store holds `KEK` 2011 alone. So a guest cannot run the case of a
`KEK` that does not chain to the signer (field risk 3). The tampered
append shows that the firmware checks the signature, and the `KEK`
case waits for a firmware that does not change its own keys.

## The revocation check

Run by hand on 2026-10-20. A `liken` machine's firmware loads two EFI
binaries: `vmlinuz`, through the slots' boot entries, and
`systemd-bootx64.efi`, as the fallback loader on each slot and as the
stick's menu. The check read both from release 2026.10.20-001 on
the channel, and checked each file's SHA-256 against the release's
`release.yaml`.

- Both binaries are unsigned: each has an empty certificate table. A
  `dbx` entry that names a certificate cannot match them.
- Their Authenticode SHA-256s are `74ba3130...` for `vmlinuz` and
  `b9ebe4b0...` for `systemd-bootx64.efi`. The `signify` library and
  a direct reading of the PE layout gave the same hashes. On Ubuntu's
  signed shim, the same code gave the digest that Microsoft's
  signature carries, so the hashing is correct.
- liken-1's `dbx` 20260402 holds 443 entries, and the 20260707
  payload holds 291. All of them are SHA-256 hashes, and neither
  binary's hash is among them.
- The nine older releases in a workstation's release directory, from
  2026.10.09-078 to 2026.10.10-912, ship the same two files byte for
  byte, so the result covers them too. That covers a machine whose
  other slot holds one of them.

So the 20260707 `dbx` revokes no `liken` binary of these releases.
With Secure Boot off, a revoked binary would still boot, so this check
matters only once `liken` signs its boot chain. Then the operator runs
it before each approval, on both slots of each machine.

## The real write on stick-1

Run by hand on 2026-10-20, on stick-1, at `liken` 2026.10.20-001, with
`firmware-operator` 2026.10.20-001-dev-007-3eb6a9df. stick-1 is in
user mode with Secure Boot off. Its `PK` is "DO NOT TRUST - AMI Test
PK", the AMI test key that many boards shipped with. A `dbx` update
is signed by `KEK`, so the `PK` does not affect it. Its `KEK` holds
"Microsoft Corporation KEK CA 2011" alone.

The update was `dbx` 20160809 to 20260402, of the component
`com.microsoft.dbx.x64.firmware`, signed by `KEK` 2011, from the
cabinet with the SHA-256
`9edea8bc287bf9bf4659856b28cf421f330eb2f658c163eab0a03512a98c0e78`.
The steps were the ones in [The steps](#the-steps), each by hand:

1. **Approve.** stick-1 got the label `canary: "true"`, and
   `microsoft-dbx-x64` got the version, the pinned cabinet, and
   `spec.machines` selecting that label. `Approved` read `True` with
   the reason `Offered`, and `PENDING` read 1.
2. **Rehearse.** The unprivileged pod on stick-1 copied 61 variables.
   Only `dbx` changed, from 3,728 to 28,361 bytes: a 24,633-byte
   append with the attributes `0x67`. Nothing was written under the
   ESP path. fwupd asked for 24,629 bytes free, and the store of 128K
   had 57K free.
3. **Check the revocation.** The payload's 443 SHA-256 hashes did not
   include `liken`'s `vmlinuz` or `systemd-bootx64.efi`.
4. **Write.** A privileged pod on stick-1 wrote the recorded append
   in one `write`. The firmware accepted it. `dbx` grew from 3,728 to
   23,724 bytes, and its entries from 77 to 493: the 416 hashes that
   were new, with no duplicate. The store's free space fell from 57K
   to 38K.
5. **Reboot.** The annotation `liken.sh/request-reboot` drained
   stick-1 and rebooted it, and stick-1 was `Ready` again within 95
   seconds of the request.
6. **Prove.** The next scan, within 20 seconds of `Ready`, reported
   `dbx` 20260402 on stick-1. `Approved` moved to `True` with the
   reason `Installed`, `PENDING` and `BEHIND` read 0, and the operator
   posted a `Normal` `Installed` `Event`.

The approval and the label stay on the testbed as a record.

## What fwupd does not check on a `liken` machine

- **Revocation of the running boot chain.** Before a `dbx` update,
  fwupd checks that the new entries do not revoke a binary that the
  machine boots. It finds those binaries through UDisks and the
  entries of `BootOrder`. A `liken` machine has no UDisks, and with
  `FWUPD_UEFI_ESP_PATH` fwupd makes a volume with no binaries, so the
  check passes with nothing checked. `--force` does not skip the check,
  but here there is nothing to skip. `liken`'s binaries are unsigned
  today, so `liken` machines boot with Secure Boot off, and a firmware
  with Secure Boot off enforces no `dbx` entry. So no `dbx` update can
  stop a `liken` machine from booting today. When `liken` signs its boot chain, the operator must hash
  both slots' EFI binaries with Authenticode and refuse an update
  whose entries name one of them.
- **Trust of the cabinet.** `fwupdtool` checks the catalog's signature
  when it reads the catalog, but only the daemon enforces `OnlyTrusted`
  on a cabinet. The approval's pinned SHA-256, which the operator
  checks before the pod reads the cabinet, is the trust.
- **History.** fwupd records its history in `/var/lib/fwupd`, which a
  pod loses when it ends, so `fwupdtool get-results` has nothing to
  read after a reboot. The proof reads the next scan's version
  instead.

## Field risks

Read in fwupd's issues and in vendors' reports, not measured. The most
likely first:

1. A variable store too full for the append. The rehearsal records
   `RequiredFree` against the free space, so the operator refuses this
   before a write.
2. Checks of the ESP failing. These belong to capsules.
3. A `KEK` that does not chain to the update's signer. The firmware
   refuses the append, and nothing changes. The two `dbx` components
   in plan 03 exist because of this: each is signed by a different
   `KEK`.
4. A TPM PCR 7 measurement that changes with `dbx`. It breaks disk
   encryption sealed to PCR 7. `liken` seals nothing to it today.
5. A firmware that resets Secure Boot to its defaults and discards
   the appended entries.
6. A rare brick on old or white-label firmware. The reports name
   machines from Fujitsu, Toshiba, and Framework.
7. A capsule's boot entry left in `BootOrder`, and its `BootNext`
   competing with a release trial. Milestone 33 covers both.
8. A status of failed for an update that applied
   ([fwupd#11119](https://github.com/fwupd/fwupd/issues/11119)). A
   status that the operator trusted would retry the update. The proof
   reads the version from the next scan, not fwupd's status.

## Proof before the first real write

Each step runs only after the one before it passes.

1. **The rehearsal on a real machine.** Done on liken-1, above.
2. **Real writes under OVMF.** Done for `dbx`, under [The real
   writes under OVMF](#the-real-writes-under-ovmf). The guest ran with
   Secure Boot off and Microsoft's keys enrolled, as liken-1 does,
   because OVMF with Secure Boot on refuses `liken`'s unsigned kernel.
   No `db` or `KEK` cabinet was offered to liken-1 or to the guest, so
   none ran.
3. **The revocation check.** Done, under [The revocation
   check](#the-revocation-check).
4. **One real machine.** Done on stick-1, under [The real write on
   stick-1](#the-real-write-on-stick-1). stick-1 went first instead of
   liken-1, because its update was the harder one: a `dbx` ten years
   old, a store half full, and a payload signed by `KEK` 2011.

Capsules come after milestone 33, with their own proof.

## Questions for the full design

- **Who makes the write.** Milestone 33 makes `init` the only process
  that writes the variable store, and its shim turns fwupd's writes
  into requests. The rehearsal is already the shim's first half: its
  difference is the request. The open choice is the second half. `init`
  can write the recorded append when the OS's interface carries it,
  which keeps one writer. Or the apply pod can write it, with
  `efivarfs` read-write inside the turn, because a database update
  touches no variable `init` uses. The first is the likely choice.
  In the OVMF runs, the recorded append and fwupd's own write left
  the same `dbx`, so either writer gives the same result.
- **The turn interface.** Milestone 33 has to give a component a way
  to request a rollout turn for a machine and to see it granted.
- **When to rehearse.** A rehearsal after the approval shows what the
  approved version writes. A rehearsal of each machine's newest
  release before any approval would show it to the person who
  decides, at the cost of a download of each cabinet on each machine.
- **Which ESP.** For capsules. A `liken` machine has two boot slots,
  each an ESP. The shim has to choose the slot the capsule goes into,
  and keep the release trials of `init` working.

## Considered and set aside

- **`fwupdtool install-blob`.** It writes a payload with no cabinet,
  so it checks no signature and records no history. The approval pins
  the cabinet, and the rehearsal reads the payload from it.
- **The fwupd daemon.** It enforces `OnlyTrusted`, but it holds memory
  on every machine all the time, and it writes the variable store on
  its own schedule.
- **Capsules first.** They write `BootNext` and `BootOrder`, which
  `init` owns, and none was offered to the testbed in the drills.
