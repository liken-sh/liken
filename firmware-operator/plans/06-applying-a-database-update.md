# 06, Applying a Secure Boot database update

Proposed on 2026-10-21. Not built. This plan writes an approved
`dbx`, `db`, or `KEK` update to a machine, inside a reboot turn that
the OS grants, and proves it from the next scan. It follows
[plan 05](completed/05-rehearsal.md), whose rehearsal records the one
append that the update makes, and it needs `liken`'s
[milestone 88](../../liken/plans/88-a-component-takes-a-reboot-turn.md)
for the turn. It needs nothing else from the OS, because a database
update writes no variable that `init` reads or writes
([milestone 33](../../liken/plans/33-firmware-updates.md)). Capsules
follow in plans [07](07-rehearsing-a-capsule.md) and
[08](08-applying-a-capsule.md).

## The problem

Plan 05 proved each part of a database write by hand: the rehearsal
in the operator, the real writes under OVMF, the refusal of a
tampered append, and one real write on stick-1. On stick-1 a person
ran each step: wrote the bytes from a privileged pod, set the
`liken.sh/request-reboot` annotation, and read the next scan. The
operator has to run the same steps, on one machine at a time, with a
drained machine for the write and a record of each result.

## The steps

1. **Rehearse.** Plan 05, as built. A machine whose rehearsal did
   not pass gets no turn.
2. **Ask for a turn.** For each machine whose rehearsal passed, the
   operator asks for a turn through milestone 88. A `Firmware` asks
   for one machine at a time. The conductor's budget limits the fleet,
   so the operator adds no budget of its own.
3. **Write.** When the turn reports the machine drained, the operator
   starts an apply pod on it. The pod runs the rehearsal again against
   a fresh copy, so the bytes come from the machine's variables as
   they are now. It checks that the append's SHA-256 is the one in
   `status.rehearsals`. It then writes that append to the real
   variable in one `write` through a file opened with `O_APPEND`, and
   reads the variable back. A different SHA-256 means the variables
   changed since the rehearsal: the pod writes nothing, and the
   operator rehearses again after the reboot.
4. **Release the turn.** The pod's result goes into status, and the
   operator releases the turn with it. The machine reboots, because a
   database update takes effect at the next boot, and the turn's
   workloads are already gone.
5. **Prove.** The next scan after the reboot reports the device's
   version. Plan 04's `Approved` condition moves to `Installed` when
   every selected machine reports the approved version, as it did on
   stick-1. The proof reads the version, not the size of the
   variable, because the firmware merges the signed list into the
   stored one and keeps no duplicate.

A failed write has no earlier state to return to, and a firmware
that refuses an append changes nothing. Either way, the operator
records the result in `status.applies`, posts an `Event`, and does
not try again on its own. A person retries by
approving again.

## Who writes the bytes

The apply pod writes the recorded append itself. The alternative is
`fwupdtool install` against the real variable store.

- **The recorded append** is exactly what the rehearsal checked and
  what status records. Its write is one system call, the same that
  the OVMF runs and stick-1 used. The firmware verifies the signature
  either way.
- **`fwupdtool install`** wrote the same `dbx` in the OVMF run, byte
  for byte. But it reads the cabinet again, writes on its own terms,
  and clears the immutable flag only when the store is at
  `/sys/firmware/efi/efivars`. The pod would need that path, and the
  rehearsal's check would cover a write that a second program makes.

The recorded append is the likely choice.

## The apply pod

The pod runs `scan apply` from the scan image, on the machine, after
the drain. It tolerates the cordon. It mounts the machine's
`/sys/firmware` with `efivarfs` read-write, and it needs the capability
to clear the variable's immutable flag, `CAP_LINUX_IMMUTABLE`. The
stick-1 write ran from a privileged pod. Whether the smaller set is
enough is not measured. The label `firmware.liken.sh/apply` selects
the pods for the operator's watch and for a NetworkPolicy that allows
DNS and HTTPS out, for the catalog and the cabinet.

## Status and Events

- `status.applies` holds one record for each machine in a turn or
  after one: `Waiting` for the turn, `Writing`, `Written`, `Refused`
  by the firmware, or `Failed`, with the append's SHA-256 and the
  boot that wrote it. The scan's version closes the record.
- The `Applied` condition is `True` when every selected machine wrote
  and reports the approved version, `False` when one failed, and
  `Unknown` while one waits or writes.
- `ApplyStarted`, `ApplyWritten`, `ApplyRefused`, and `ApplyFailed`
  `Event`s, beside the condition's transitions.

The names are a proposal and change with the build.

## The manual

The guide that approves an update gains a section on the turn and
the write, and the troubleshooting guide gains the failures above. The
manual states plainly that a written update cannot be undone. It also
states the risk of a firmware that resets Secure Boot to its
defaults, which discards the appended entries
([plan 05's field risks](completed/05-rehearsal.md#field-risks)).

## Proof

On stick-1 if a catalog offers it a newer `dbx`. Otherwise on liken-1,
whose approval of `dbx` 20260707 on `microsoft-dbx-x64-kek2023`
rehearsed and passed on 2026-10-20. The proof is the version in the
next scan, the `Installed` reason, and the `Event`s in order. A second
run writes a tampered append through the same pod under OVMF on the
lab's `node-1`, and expects the firmware's refusal in status.

## Open questions

- Whether `CAP_LINUX_IMMUTABLE` and a read-write mount are enough
  without a privileged pod.
- Whether a machine with a passed rehearsal that a person approves
  again, before its turn, keeps its place.
