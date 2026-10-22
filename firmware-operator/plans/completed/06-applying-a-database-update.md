# 06, Applying a Secure Boot database update

Proposed on 2026-10-21. Built on 2026-10-22 and drilled on the lab's
`node-5` under OVMF with Microsoft's keys
([As built](#as-built), [The lab drills](#the-lab-drills)). The real
write on liken-1 or stick-1 has not run, and neither has a refusal by
real firmware through the operator. This plan writes an approved
`dbx`, `db`, or `KEK` update to a machine, inside a reboot turn that
the OS grants through a `Disruption`, and proves it from the next
scan. It follows
[plan 05](05-rehearsal.md), whose rehearsal records the one
append that the update makes, and it needs `liken`'s
[milestone 88](../../../liken/plans/88-disruptions.md)
for the turn, in its second stage, which adds `spec.hold`. It needs
nothing else from the OS, because a database
update writes no variable that `init` reads or writes
([milestone 33](../../../liken/plans/33-firmware-updates.md)). Capsules
follow in plans [07](../07-rehearsing-a-capsule.md) and
[08](../08-applying-a-capsule.md).

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
   operator creates a `Disruption` with `kind: Reboot`, `hold: true`,
   and the requester `firmware.liken.sh/firmware-operator`. A
   `Firmware` asks for one machine at a time. The machine's
   `MachineDisruptionBudget`s limit the fleet, so the operator adds no
   budget of its own. A database update has no urgency, so by default
   the operator files the request only when another `Disruption` is
   pending for the machine, usually its next release, and the update
   shares that reboot. A deadline on the approval files it on its own
   when no other turn comes.
3. **Write.** When the machine is in its turn and drained, the
   operator starts an apply pod on it. The pod runs the rehearsal again against
   a fresh copy, so the bytes come from the machine's variables as
   they are now. It checks that the append's SHA-256 is the one in
   `status.rehearsals`. It then writes that append to the real
   variable in one `write` through a file opened with `O_APPEND`, and
   reads the variable back. A different SHA-256 means the variables
   changed since the rehearsal: the pod writes nothing, and the
   operator rehearses again after the reboot.
4. **Release the hold.** The pod's result goes into status, and the
   operator clears `hold`. The machine reboots, because a database
   update takes effect at the next boot, and the turn's workloads are
   already gone. The reboot also applies whatever else the turn
   holds.
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
([plan 05's field risks](05-rehearsal.md#field-risks)).

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

## As built

The operator follows the steps above, with three changes that the
build and the drills made.

- **The request goes in at once.** The operator files its
  `Disruption` as soon as a machine's rehearsal passes on the boot it
  runs, with `hold: true` and no `hash`, so `machine-operator` names
  the running boot. A person chose this over waiting for the
  machine's next turn: an update that waits for a release can wait
  without bound, and a request that arrives while a release is pending
  shares that turn anyway. The request carries the label
  `firmware.liken.sh/firmware` and names the `Firmware` as its owner.
- **An apply that does not say whether it wrote reboots the
  machine.** The pod's own answer is `Written`, `Refused`, or
  `Failed`, and only the last two say that nothing was written. A pod
  that is gone, runs past its deadline, crashes, or answers something
  the operator does not know may have written before it stopped, and a
  force-deleted pod's container can outlive its API object. Such an
  end is `Uncertain`: the operator removes the hold, the machine
  reboots in its turn, and the next scan ends the record or makes it
  `Failed` with `so the append was not written`. Withdrawing the turn
  instead would leave the machine running with variables that its
  kernel does not enforce until a reboot that nobody planned, which is
  the state the hold exists to prevent.
- **A deleted request comes back after a write.** A person who
  deletes the request while the apply pod runs ends the turn, and
  `machine-operator` uncordons the machine with no reboot. When the
  record then reads `Written` or `Uncertain` and the machine still
  runs the boot of the write, the operator files the request again
  with no hold, and the machine takes another turn under its budgets.

`status.applies` holds one record for each pending machine that asked
for a turn: `Waiting`, `Writing`, `Written`, `Uncertain`, `Refused`,
or `Failed`. The `Applied` condition sums them, with the reasons
`Written`, `Refused`, `Failed`, `Uncertain`, and `Applying`. The
`Event`s are `ApplyRequested`, `ApplyStarted`, `ApplyWritten`, and the
`Warning`s `ApplyRefused`, `ApplyFailed`, and `ApplyUncertain`. No
rehearsal starts on a machine while its record is live. A `Refused` or
`Failed` record stays until a person withdraws the approval and
approves it again.

The apply pod is not privileged. It drops every capability, adds
`DAC_READ_SEARCH` and `LINUX_IMMUTABLE`, and mounts `/sys/firmware`
read-write. That answered the first open question: on the lab, this
set wrote the append. `scan apply` rehearses again against a copy,
checks the append's SHA-256, clears the variable's immutable flag,
writes the append in one `write`, and sets the flag again.

The second open question, a person who approves again while a
machine waits for its turn, is not answered. The approval's version
and cabinet key the records, so an edit that keeps both keeps the
record.

## The lab drills

Run on 2026-10-22 on the lab fleet at a development build of `liken`
`2026.10.22-907` with `spec.hold`, and development builds of
`firmware-operator`. `node-5` booted OVMF's SMM build from Debian's
`ovmf-generic` `2026.08+ds-2` on a `q35` machine, with Microsoft's
`PK`, both `KEK`s, and the `db` certificates enrolled, Secure Boot off,
and `dbx` seeded with Microsoft's signed update of 2026-04-02
(`make -C dev-cluster enroll-keys`). fwupd read `dbx` 20260402 and
offered 20260707 on `microsoft-dbx-x64-kek2023`, as on liken-1. Each
run reset the variable store, and the standing approval of 20260707
ran the whole flow again. The proof of each run is the scan of the
next boot, and the variable store read on the laptop with
`virt-fw-vars`.

1. **The whole flow.** From the approval to `Installed` took 38
   seconds: the rehearsal passed with the same 17,260-byte append as
   on liken-1, the request was granted and held, the apply pod wrote,
   the hold was released, `node-5` rebooted, and the scan read
   20260707. In the store, `dbx` grew from 21,292 to 21,416 bytes,
   and only `dbx` and the firmware's monotonic counter changed.
2. **The operator killed after the hold.** The new leader read the
   pod's result, released the hold, and the machine reached
   `Installed`. No second apply pod started.
3. **The apply pod force-deleted while it ran.** On the build before
   `Uncertain`, the record went `Failed` and the request was
   withdrawn, so `node-5` uncordoned with no reboot. The store showed
   no write, because the pod stopped before it wrote, but the case
   made the gap plain. On the build with `Uncertain`, the record went
   `Uncertain`, `node-5` rebooted 18 seconds after the delete, and the
   scan still read 20260402, so the record ended `Failed` with `so the
   append was not written`.
4. **A person's retry.** After a `Failed` record, withdrawing the
   approval and approving it again ran the flow to `Installed` in 53
   seconds.
5. **The request deleted the moment the turn held.** The apply pod had
   already started. On the build before the fix, the pod wrote,
   `node-5` stayed uncordoned with no reboot, and the record read
   `Written` with no request. The fixed operator, started on that
   state, filed the request again, and `node-5` rebooted and reached
   `Installed` 33 seconds later. The same race from a clean start
   ended the same way, with the same `dbx` bytes as run 1.
