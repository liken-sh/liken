---
title: Approve a firmware update
weight: 27
description: "Approve one release of a firmware component that firmware-operator discovered by setting spec.update.version and pinning the cabinet's URL and SHA-256 on its Firmware, and limit the approval to some machines with spec.machines. Read the Approved, Rehearsed, and Applied conditions and columns, status.rehearsals and status.applies, and the Disruption with spec.hold that holds each machine's reboot turn while the apply pod writes. Withdraw an approval. Use when a person wants a board's UEFI firmware, the Secure Boot revocation list, or another device to move to a newer release, when an approval is False, or when a machine waits for its turn to write an update."
---

# Approve a firmware update

Approving a release tells `firmware-operator` to install it. At the end
of this guide, a discovered `Firmware` records the release you approved,
pinned to its cabinet file, and its status shows which machines still
need the release and what the install would write on each one.

After you approve, `firmware-operator` rehearses the install against a
copy of each machine's firmware variables. For a Secure Boot database
update (`dbx`, `db`, or `KEK`), it then writes the update on each
machine during the machine's reboot turn, and the next scan confirms
it. For a capsule, such as a board's UEFI firmware, it stages the
capsule during the turn. The reboot then boots fwupd's updater once,
and the updater hands the capsule to the firmware.

You need a discovered `Firmware` whose `status.machines` lists a
release. [Find firmware updates](../find-updates/) shows how to read
them.

## Choose the release

A discovered `Firmware` refers to one catalog component. Each entry in
`status.machines` lists the releases that a catalog offers for that
component's device on one machine. Read the releases:

```sh
kubectl get firmware microsoft-dbx-x64-kek2023 -o jsonpath='{range .status.machines[*]}{.machine}{"\t"}{.version}{"\t"}{.releases[0].version}{"\t"}{.releases[0].cabinet.sha256}{"\n"}{end}'
```

```
liken-1   20260402   20260707   1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5
```

On `liken-1`, the Secure Boot revocation list (`dbx`) is at version
20260402, and the LVFS catalog offers 20260707. Run
`kubectl get firmware microsoft-dbx-x64-kek2023 -o yaml` and copy the
release's `version`, `cabinet.url`, and `cabinet.sha256` from the full
entry.

A `Firmware` whose name is a device name and a hash of its GUIDs, such
as `uefi-ca-7420e8dc`, has no release to approve, and the API server
refuses a `version` on it.

## Approve it

Add `version` and `cabinet` to the object's `spec.update`:

```yaml
apiVersion: firmware.liken.sh/v1alpha1
kind: Firmware
metadata:
  name: microsoft-dbx-x64-kek2023
spec:
  update:
    component: com.microsoft.dbx.x64-kek2023.firmware
    version: "20260707"
    cabinet:
      url: https://r2.fwupd.org/lvfs-prod/1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5-DBXUpdate-20260707-x64.cab
      sha256: 1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5
```

```sh
kubectl apply --server-side --force-conflicts -f microsoft-dbx-x64-kek2023.yaml
```

The operator created the object, so `--server-side` with
`--force-conflicts` makes you the owner of the fields you set. You can
also use `kubectl edit`.

You approve the SHA-256 as well as the version, because a catalog can
serve a different cabinet under the same version. The operator installs
only the cabinet you pinned. Put quotes around a version that
YAML would read as a number, such as `"20260707"`.

To approve a release on only some machines, such as one machine first,
label those `Machine` objects and add `spec.machines`:

```sh
kubectl label machine liken-1 canary=true
```

```yaml
spec:
  update:
    component: com.microsoft.dbx.x64-kek2023.firmware
    version: "20260707"
    cabinet:
      url: https://r2.fwupd.org/lvfs-prod/1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5-DBXUpdate-20260707-x64.cab
      sha256: 1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5
  machines:
    matchLabels:
      canary: "true"
```

`spec.machines` limits an approval, so the API server refuses it on an
update with no `version`.

## Read the approval

```sh
kubectl get firmware microsoft-dbx-x64-kek2023
```

```
NAME                        VALID    LOADED   RUNNING   FAILED   CONFLICT   MACHINES   BEHIND   VERSION    APPROVED   PENDING   REHEARSED   APPLIED   AGE
microsoft-dbx-x64-kek2023                                                   1          1        20260707   True       1         True        Unknown   2d
```

`VERSION` is the version you approved. `APPROVED` is the `Approved`
condition. `PENDING` is the number of machines that still need the
release, which are the machines where a catalog offers the approved
version, in the cabinet you pinned, for one of the machine's devices.
`PENDING` falls to 0 as machines install the version.

The reason and message of the condition say which case applies and list
the machines:

```sh
kubectl get firmware microsoft-dbx-x64-kek2023 -o jsonpath='{.status.conditions[?(@.type=="Approved")]}'
```

| Status | Reason | Meaning |
|---|---|---|
| `True` | `Offered` | At least one machine waits for the version. |
| `True` | `Installed` | No machine waits, and at least one runs the version. |
| `False` | `CabinetDiffers` | A catalog offers the version only in a cabinet with another SHA-256. Check the SHA-256 you copied. If it is right, the catalog changed the file, and you decide whether to approve the new one. |
| `False` | `NotOffered` | No catalog offers the version to a machine the approval covers. The version is wrong, the catalog withdrew the release, or `spec.machines` selects no machine that has the device. |
| `False` | `MachinesInvalid` | `spec.machines` does not compile. The message gives the error. |

The operator reads only the spec, the entries in `status.machines`, and
the labels of each `Machine`, so the condition updates as soon as you
apply the approval, without a scan. If a catalog withdraws the release
or serves it in a different cabinet, the entries change at the next
scan, and `Approved` becomes `False` then.

## Read the rehearsal

`fwupd` has no dry run, and a firmware update cannot be undone. So
before any machine installs the release, the operator rehearses the
install on each pending machine, in a short pod in `liken-system` on
that machine. The pod copies the machine's
firmware variables, its ESRT, and its DMI tables into memory, runs
`fwupdtool install` with the pinned cabinet against the copy, and
compares the copy before and after. The pod mounts the machine's
`/sys/firmware` read-only, so the rehearsal cannot write the real
variables.

A rehearsal runs when the machine is `Ready` and its last scan read the
current file of the `FirmwareCatalog`. A machine rehearses once per
boot, and again after a new approval or a scan of a new catalog file.
`status.rehearsals` has one record for each pending machine:

```sh
kubectl get firmware microsoft-dbx-x64-kek2023 -o jsonpath='{range .status.rehearsals[*]}{.machine}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
```

```
liken-1   Passed   the install appends 17260 bytes to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f, and the variable store has 67149 bytes free
```

A Secure Boot database update, such as a `dbx` update, passes when the
install writes authenticated appends to its own variable and nothing
else, and the variable store has room for them. fwupd writes one
authenticated append for each payload in the cabinet. A `db` update of
the third-party and option ROM certificates has two payloads, and the
message then gives the number of writes. The record's `append` gives
the variable, the length of all the writes together, and their
SHA-256. The firmware verifies the signature of each write and merges
its new entries into the list, so the variable grows by less than the
append.

A capsule, such as a board's UEFI firmware, passes when the install
writes what fwupd stages for a capsule and nothing else:

- one boot entry for fwupd's updater, and `BootNext` set to that entry
- fwupd's own status variable
- the updater and the capsule under `\EFI\<ID>` on the ESP, where
  `<ID>` is the `ID` of the scan image's `os-release`

For capsule-on-disk, fwupd writes the capsule under
`\EFI\UpdateCapsule` and sets one bit of `OsIndications` instead.

The rehearsal copies the `\EFI` directory of the ESP that the
machine's next boot reads. `liken` links that ESP at
`/run/liken/proven-esp`, and it must have room for the files. The
record's `capsule` lists each write with its size and SHA-256. A
rehearsal cannot show whether the firmware accepts the capsule,
because the firmware applies it before any kernel loads.

The `Rehearsed` condition summarizes the records, and the `REHEARSED`
column shows it:

| Status | Reason | Meaning |
|---|---|---|
| `True` | `Passed` | The install passed its rehearsal on every machine the approval waits for. |
| `False` | `Refused` | A database update would write something other than authenticated appends to its own variable, such as a boot variable or a file on the ESP, or the store has no room. A capsule would write more than fwupd stages for one, or the ESP has no room. |
| `False` | `Failed` | A rehearsal could not run, such as a cabinet whose SHA-256 is not the one you pinned. |
| `Unknown` | `Rehearsing` | A machine waits for its rehearsal. |

The message names the machines and says why.
[Troubleshoot](../troubleshoot/#rehearsals) covers each case.

## Apply the update

The operator writes a Secure Boot database update during the machine's
reboot turn, after the drain, so no workload runs during the write, and
the reboot that follows makes the update take effect. The operator
requests the turn with a `Disruption` that sets `spec.hold`, which stops
the turn after the drain and before the reboot. Your approval of the
release also approves the write, so the operator asks for no second
approval of the update. A machine whose `rebootPolicy` is `Manual`
still waits for you to approve its reboot turn, as the steps below
show.

The drain can evict pods that the cluster's network needs, such as its
DNS. So the apply pod downloads everything it needs before the operator
requests the turn, because on a machine with little to drain the turn
can start within a second of the request. The write after the drain
needs no network.

On each machine:

1. **Prepare.** The operator starts an apply pod in `liken-system` on
   the machine. The record in `status.applies` reads `Waiting` and has
   no `disruption`. The pod loads the catalog, downloads the cabinet,
   runs the rehearsal again against a fresh copy, and checks that the
   append has the SHA-256 in the rehearsal's record. If it does not, the
   variables changed since the rehearsal, and the pod writes nothing.
   Then the pod prints a log line with `"phase":"prepared"`, creates the
   marker file `/run/apply/prepared`, and waits with no deadline. Its
   readiness probe, `scan prepared`, finds the marker, and the pod
   becomes `Ready`. If the pod ends or is deleted before it is `Ready`,
   it wrote nothing. The record reads `Failed`, and the operator does
   not request a turn.
2. **Request the turn.** When the pod is `Ready`, the operator creates
   the `Disruption` `<machine>.firmware-<firmware>` with the requester
   `firmware.liken.sh/firmware-operator`, the kind `Reboot`, and
   `spec.hold: true`, and records its name in `status.applies`. The
   machine's `MachineDisruptionBudget`s decide when it gets its turn,
   the same as for an OS release. The record still reads `Waiting`.
3. **Hold.** `machine-operator` drains the machine. Instead of
   rebooting, it sets the `Held` condition of the `Disruption` to
   `True`. The hold has no deadline. The `liken` drain does not evict a
   pod that tolerates the `node.kubernetes.io/unschedulable` taint, and the
   apply pod and the operator's own pod both tolerate it, so both stay
   on the machine.
4. **Write.** The operator tells the apply pod to write. It sets the
   pod's annotation `firmware.liken.sh/write` to `go` and
   `firmware.liken.sh/write-at` to the current time, and the record
   reads `Writing`. The pod reads its annotations through a downward API
   volume, so it needs no credentials. It reads the variable again, and
   writes nothing if the variable changed while the pod waited. Then it
   writes the parts of the append to the machine's variable one at a
   time, in fwupd's order, and reads the variable back. If the apply
   pod is gone when the hold starts, the operator starts a new one that
   already has the annotation. That pod prepares on the drained
   machine, so it needs the cluster's DNS there, and then writes at
   once.
5. **Reboot.** When the write succeeds, the record reads `Written` and
   the operator removes `spec.hold`. The turn continues, and
   `machine-operator` reboots the machine. If the firmware refuses the
   write or the write fails, the operator deletes the `Disruption`
   instead, and the machine leaves its turn without a reboot.
6. **Confirm.** The machine's first scan after the reboot reports the
   device's version. When the scan reads the approved version, the
   machine is no longer pending, and the operator removes its record
   and deletes the `Disruption`. `Approved` changes to `Installed` when
   no machine is pending. If the scan still offers the approved version, the record
   reads `Failed`.

A machine whose `rebootPolicy` is `Manual`, the default, waits for you
to approve each request for its turn. `liken approve-reboot` approves
only requests from `machine-operator` and reboot requests from a
person, so approve this request with `kubectl`:

```sh
kubectl patch disruption liken-1.firmware-microsoft-dbx-x64-kek2023 --type merge \
  -p "{\"spec\":{\"approvedHash\":\"$(kubectl get disruption liken-1.firmware-microsoft-dbx-x64-kek2023 -o jsonpath='{.spec.hash}')\"}}"
```

Read the `Disruption` and the records:

```sh
kubectl get disruptions -l firmware.liken.sh/firmware=microsoft-dbx-x64-kek2023
kubectl get firmware microsoft-dbx-x64-kek2023 -o jsonpath='{range .status.applies[*]}{.machine}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
```

```
liken-1   Written   wrote the 17260-byte append to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f, which grew from 21352 to 21476 bytes because the firmware merges the signed list into the stored one
```

The `Applied` condition summarizes the records, and the `APPLIED`
column shows it:

| Status | Reason | Meaning |
|---|---|---|
| `True` | `Written` | The firmware took the update on every machine the approval waits for. Each machine applies it at its reboot. |
| `False` | `Refused` | The firmware refused the first write of the append on a machine, and nothing changed there. For a capsule, a machine's `status.firmware.bootOrderKept` is not `true`. |
| `False` | `Failed` | A write could not run, an apply pod ended before the signal to write, a machine left its reboot turn before `machine-operator` held it, or a machine still reads the old version after its reboot. |
| `Unknown` | `Uncertain` | The apply pod on a machine ended after the signal without reporting whether it wrote, or the firmware accepted some of the writes of an append and refused the rest. The machine reboots in its turn, and its next scan shows the result. |
| `Unknown` | `Applying` | A machine waits for its rehearsal, its turn, or its write. |

An apply pod that ends before the signal wrote nothing. Its record
reads `Failed`, and the operator deletes the `Disruption`, so the
machine leaves its turn without a reboot.

The record reads `Uncertain` when the operator cannot tell whether the
append was written: the pod that received the signal was deleted,
crashed, or did not finish within 10 minutes of the signal, or the
firmware accepted part of the append and then refused or failed the
rest because the variable changed. Part of the append may be in the
variable, and the firmware applies it only at a reboot. So the operator
keeps the turn, and the machine reboots in it. The next scan then reads
the version. If it is the approved version, the record is removed. If
it is the old version, the record reads `Failed`.

After `Refused` or `Failed`, the operator does not write again on its
own. To try again, withdraw the approval and approve it again, which
removes the records.
[Troubleshoot](../troubleshoot/#applies) covers each case.

This needs a `liken` release whose `Disruption` has `spec.hold`, which
is 2026.10.22-001 or later. On an older release, `machine-operator`
reboots the machine without a hold, and the record reads `Failed` with
a message that says the `Disruption` is `Complete` and the machine left
its reboot turn before `machine-operator` held it. On a release with no
`Disruption` kind, the operator does not request a turn, and the
machines stay pending.

### A written update cannot be undone

A machine has one variable store, and the firmware keeps no earlier copy
of `dbx`, `db`, or `KEK`. If you withdraw the approval after a write,
the new entries stay in place. If the firmware resets Secure Boot to its
defaults, from its setup menu or on its own, it discards the appended
entries, and the next scan offers the release again.

### Apply a capsule

A capsule is applied in the same kind of turn, with these differences.

- **The machine must keep its boot order.** After fwupd's updater hands
  the capsule to the firmware, the firmware resets and boots the first
  entry of `BootOrder`, and `BootNext` no longer applies. So `liken`
  lets a reboot boot the updater only when the machine's
  `status.firmware.bootOrderKept` is `true`. It is `true` when the
  proven slot, the boot slot of the last release that booted and
  rejoined the cluster, was the first entry of `BootOrder` at the
  machine's last power-on. On any other machine the record reads
  `Refused` before the operator requests a turn.
- **fwupd writes to the machine itself.** After the signal, the apply
  pod runs fwupd's install against the machine's own variable store and
  the ESP that its next boot reads, which `liken` links at
  `/run/liken/proven-esp`. Then it compares what fwupd wrote with the
  record of the rehearsal. If the writes are the same, the record reads
  `Written`, and the operator removes `spec.hold` and sets
  `spec.keepBootNext`, so the reboot boots fwupd's updater once. If the
  writes differ, the record reads `Uncertain`. The operator then removes
  only `spec.hold`, the reboot boots the proven slot, and the capsule
  does not run.
- **The pod completes fwupd's boot entry.** fwupd finds the ESP's
  partition through udisks, which a pod does not have. So the boot entry
  of its updater, and the status variable that tells the updater where
  the capsule is, name no partition. The pod copies the partition from
  `liken`'s own boot entry for the same slot into both, in the rehearsal
  and in the apply.

The firmware applies the capsule before any kernel loads, so the next
scan is the only confirmation. It reads the device's version from the
firmware resource table. fwupd's updater entry and its files stay on the
machine after the update, and a later capsule uses them again.

A capsule changes firmware that runs before `liken` does. An update that
turns Secure Boot on or resets its keys stops `liken`'s unsigned boot
chain from loading, and no boot order repairs that. If the firmware or
the updater stops during the update, the machine stays off the network
until someone resets it. The record stays `Written`, and the machine's
phase is `Lost`.

## Read the Events

The operator posts an `Event` in the `default` namespace each time
`Approved`, `Rehearsed`, or `Applied` changes. A change to `True` is
`Normal`, and a change to `False` is a `Warning`. So a withdrawn release
shows in `kubectl describe firmware <name>` and in
`kubectl get events --field-selector type=Warning`.

It also posts these `Event`s about the rehearsal:

- `RehearsalStarted` when a rehearsal pod starts.
- `RehearsalPodRecreated` when the kubelet ended a rehearsal pod and
  the operator created the rehearsal's first next pod.
- `RehearsalPassed` when a rehearsal passes.
- `RehearsalCatalogMoved` when a rehearsal finds that LVFS published a
  new catalog since the machine's scan.
- `RehearsalRefused` or `RehearsalFailed`, as a `Warning`, when a
  rehearsal does not pass.

And these about the apply:

- `ApplyPreparing` when it starts the apply pod.
- `ApplyCatalogMoved` when the pod finds a new catalog and starts again.
- `ApplyRequested` when it requests the machine's turn.
- `ApplyStarted` when it tells the apply pod to write.
- `ApplyWritten` when the firmware accepted the append.
- `ApplyRefused` or `ApplyFailed`, as a `Warning`, when the firmware did
  not accept it.
- `ApplyUncertain`, as a `Warning`, when the pod ended without reporting
  whether it wrote.

## Withdraw an approval

Remove `version` and `cabinet` from `spec.update`, and remove
`spec.machines` with them. The operator removes the `Approved`,
`Rehearsed`, and `Applied` conditions and the records in
`status.rehearsals` and `status.applies`, and `PENDING` returns to 0. It
deletes each `Disruption` that still holds a machine's turn, so the
machine leaves its turn without a reboot, and it deletes each apply pod
that waits for a turn. A machine where the update is already written
keeps it. A machine whose turn already had its hold removed still
reboots.

You changed an approved `Firmware`, so the operator never deletes it,
even when its last entry is gone. Delete it yourself when you no longer
need it. If a scan still finds the component, the operator creates the
object again with no approval.
