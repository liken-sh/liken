---
title: Approve a firmware update
weight: 27
description: "Approve one release of a firmware component that firmware-operator discovered, by setting spec.update.version and pinning the cabinet's URL and SHA-256 on its Firmware, and limit the approval to some machines with spec.machines. Read the Approved condition, its reasons Offered, Installed, CabinetDiffers, NotOffered, and MachinesInvalid, the Version, Approved, Pending, Rehearsed, and Applied columns, the rehearsal of the install on each machine in status.rehearsals and the Rehearsed condition, the apply of a Secure Boot database update in each machine's held reboot turn through a Disruption with spec.hold, the apply pod that prepares before the drain and writes on the signal of its annotation firmware.liken.sh/write, status.applies and the Applied condition, the reboot, the proof by the next scan, and the Events. Withdraw an approval. Use when a person wants a board's UEFI firmware, the Secure Boot revocation list, or another device to move to a newer release, when an approval is False, or when a machine waits for its turn to write an update."
---

# Approve a firmware update

At the end of this guide, a discovered `Firmware` records which
release you approve, pinned to its cabinet file, and its status shows
which machines wait for it and exactly what the install would write
on each one. `firmware-operator` rehearses the install against a copy
of each machine's firmware variables. For a Secure Boot database
update (`dbx`, `db`, or `KEK`), it then writes the update on each
machine in the machine's reboot turn, and the next scan proves it. A
written update cannot be undone. The operator applies no capsule
update, such as a board's UEFI firmware, yet.

You need a discovered `Firmware` whose `status.machines` lists a
release. [Find firmware updates](../find-updates/) shows how to read
them.

## Choose the release

A discovered `Firmware` names one catalog component, and each entry in
`status.machines` lists the releases a catalog offers to one machine's
device. Read the releases:

```sh
kubectl get firmware microsoft-dbx-x64-kek2023 -o jsonpath='{range .status.machines[*]}{.machine}{"\t"}{.version}{"\t"}{.releases[0].version}{"\t"}{.releases[0].cabinet.sha256}{"\n"}{end}'
```

```
liken-1   20260402   20260707   1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5
```

liken-1's Secure Boot revocation list (`dbx`) is at 20260402, and the
LVFS catalog offers 20260707. Copy the release's `version` and its
`cabinet.url` and `cabinet.sha256` from the full entry, with
`kubectl get firmware microsoft-dbx-x64-kek2023 -o yaml`.

A `Firmware` named by a device and a hash of its GUIDs, such as
`uefi-ca-7420e8dc`, has no release to approve, and the API server
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
`--force-conflicts` takes ownership of the fields you set. A
`kubectl edit` of the object works too.

The SHA-256 is the approval as much as the version is. A catalog can
serve another cabinet under the same version, and that cabinet does
not match your approval. Quote a version that YAML would read as a
number, such as `"20260707"`.

To approve a release on some machines only, such as one machine
first, label those `Machine` objects and add `spec.machines`:

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

The API server refuses `spec.machines` on an update with no
`version`, because it limits only an approval.

## Read the approval

```sh
kubectl get firmware microsoft-dbx-x64-kek2023
```

```
NAME                        VALID    LOADED   RUNNING   FAILED   CONFLICT   MACHINES   BEHIND   VERSION    APPROVED   PENDING   REHEARSED   APPLIED   AGE
microsoft-dbx-x64-kek2023                                                   1          1        20260707   True       1         True        Unknown   2d
```

`VERSION` is the version you approved. `APPROVED` is the `Approved`
condition, and `PENDING` counts the machines the approval waits for:
a catalog offers one of the machine's devices the approved version, in
the cabinet you pinned. `PENDING` falls to 0 as machines install the
version.

The condition's reason and message say which case holds, and name the
machines:

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

The operator reads only the spec, the entries in `status.machines`,
and the labels of each `Machine`. So the approval answers at once, with
no scan. A catalog that withdraws the release, or serves it in another
cabinet, changes the entries at the next scan, and the approval moves
to `False` then.

## Read the rehearsal

`fwupd` has no dry run, and a firmware update cannot be undone. So
before any machine installs the release, the operator rehearses the
install on each machine that `PENDING` counts. A rehearsal is a short
pod in `liken-system` on that machine. It copies the machine's
firmware variables, its ESRT, and its DMI tables into memory, and runs
`fwupdtool install` with the pinned cabinet against the copy. Then it
compares the copy before and after. The pod mounts the machine's
`/sys/firmware` read-only, so the rehearsal cannot write the real
variables.

A rehearsal runs when the machine is `Ready` and its last scan read
the `FirmwareCatalog`'s current file. It runs once for each boot of
the machine, so a reboot, a new approval, or a scan of a new catalog
file runs it again. `status.rehearsals` holds one record for each
pending machine:

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
the third-party and option ROM certificates holds two, and the
message then gives the number of writes. The record's `append` gives
the variable, the length of all the writes together, and their
SHA-256. The firmware verifies the signature of each write and merges
its new entries into the list, so the variable grows by less than the
append.

The `Rehearsed` condition sums the records, and the `REHEARSED` column
shows it:

| Status | Reason | Meaning |
|---|---|---|
| `True` | `Passed` | The install passed its rehearsal on every machine the approval waits for. |
| `False` | `Refused` | The install would write something other than authenticated appends to its own variable, such as a boot variable or a file on the ESP, or the store has no room. A capsule update, such as a board's UEFI firmware, is refused for now. |
| `False` | `Failed` | A rehearsal could not run, such as a cabinet whose SHA-256 is not the one you pinned. |
| `Unknown` | `Rehearsing` | A machine waits for its rehearsal. |

The message names the machines and says why.
[Troubleshoot](../troubleshoot/#rehearsals) covers each case.

## Apply the update

A Secure Boot database update takes effect at the machine's next
boot, and the operator writes it while no workload runs on the
machine. `liken` gives both through the machine's reboot turn. So as
soon as a machine's rehearsal passes on the boot it runs, the operator
starts an apply pod on that machine, and when the pod has prepared,
it asks for the machine's turn. It files a `Disruption` with
`spec.hold`, which stops the turn after the drain and before the
reboot. You approve nothing more: the approval of the
release is the approval of the write.

The drain can evict pods that the cluster's network needs, such as
its DNS. So the apply pod does all its network work when it starts,
while the cluster is whole, and the operator asks for the turn only
after that work is done. On a machine with little to drain, the turn
can hold within a second of the request. The pod then waits on the
machine. It writes only when the operator sends it the signal, after
the drain, and the write needs no network.

The steps on each machine:

1. **The prepare.** The operator starts an apply pod in
   `liken-system` on the machine, and the record in `status.applies`
   reads `Waiting`, with no `disruption`. The pod loads the catalog,
   downloads the cabinet, runs the rehearsal again against a fresh
   copy, and checks that the append has the SHA-256 in the record. A
   different SHA-256 means the variables changed since the rehearsal,
   and the pod writes nothing. Then the pod prints a line with
   `"phase":"prepared"` to its log, creates the marker file
   `/run/apply/prepared`, and waits, with no deadline. Its readiness
   probe, `scan prepared`, finds the marker, and the pod turns `Ready`.
   A pod that ends or is gone before it is `Ready` wrote nothing: the
   record reads `Failed`, and the operator asks for no turn.
2. **The request.** When the pod is `Ready`, the operator creates the
   `Disruption` `<machine>.firmware-<firmware>`, with the requester
   `firmware.liken.sh/firmware-operator`, the kind `Reboot`, and
   `spec.hold: true`, and names it in the record. The machine's
   `MachineDisruptionBudget`s decide when it gets its turn, the same
   as for a release. The record still reads `Waiting`.
3. **The hold.** `machine-operator` drains the machine, and sets the
   `Disruption`'s `Held` condition to `True` instead of rebooting. A
   hold has no deadline. The apply pod and the operator's own pod each
   tolerate the cordon, `node.kubernetes.io/unschedulable`, by its key,
   and a `liken` drain that skips such a pod leaves both on the
   machine.
4. **The write.** The operator sends the apply pod the signal: it sets
   the pod's annotation `firmware.liken.sh/write` to `go`, and
   `firmware.liken.sh/write-at` to the time. The record reads
   `Writing`. The pod reads its annotations through a downward API
   volume, so it needs no credentials. It reads the variable again,
   and writes nothing when the variable changed while the pod waited.
   Then it writes each authenticated write of the append to the
   machine's variable, in fwupd's order and each in its own write
   call, and reads the variable back. The firmware verifies each
   write's signature over that write's own entries, and merges them
   into the list, so the variable grows by less than the append. When the apply pod is gone
   at the hold, such as when a drain evicted it, the operator starts a
   new one that carries the signal. That pod prepares on the drained
   machine, so it needs the cluster's DNS there, and then writes at
   once.
5. **The reboot.** When the write succeeds, the record reads
   `Written`, and the operator removes `spec.hold`. The turn goes on,
   and `machine-operator` reboots the machine. When the firmware
   refuses the write, or the write fails, the operator deletes the
   `Disruption` instead, and the machine leaves its turn with no
   reboot.
6. **The proof.** The machine's first scan after the reboot reports
   the device's version. When it reads the approved version, the
   machine is no longer pending, its record leaves, the operator
   deletes the `Disruption`, and `Approved` moves to `Installed` once
   no machine waits. When the scan still offers the approved version,
   the record reads `Failed`.

A machine whose `rebootPolicy` is `Manual`, the default, waits for
you to approve each request for its turn. `liken approve-reboot`
approves only `machine-operator`'s own request and a person's reboot
request, so approve the operator's request with `kubectl`:

```sh
kubectl patch disruption liken-1.firmware-microsoft-dbx-x64-kek2023 --type merge \
  -p "{\"spec\":{\"approvedHash\":\"$(kubectl get disruption liken-1.firmware-microsoft-dbx-x64-kek2023 -o jsonpath='{.spec.hash}')\"}}"
```

Read the turn and the records:

```sh
kubectl get disruptions -l firmware.liken.sh/firmware=microsoft-dbx-x64-kek2023
kubectl get firmware microsoft-dbx-x64-kek2023 -o jsonpath='{range .status.applies[*]}{.machine}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
```

```
liken-1   Written   wrote the 17260-byte append to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f, which grew from 21352 to 21476 bytes; the firmware merges the signed list into the stored one
```

The `Applied` condition sums the records, and the `APPLIED` column
shows it:

| Status | Reason | Meaning |
|---|---|---|
| `True` | `Written` | The firmware took the update on every machine the approval waits for. Each machine applies it at its reboot. |
| `False` | `Refused` | The firmware refused the first write of the append on a machine, and nothing changed there. |
| `False` | `Failed` | A write could not run, an apply pod ended before the signal to write, a machine left its turn before the hold, or a machine still reads the old version after its reboot. |
| `Unknown` | `Uncertain` | The apply pod on a machine ended after the signal without saying whether it wrote, or the firmware accepted some of the writes of an append and not the rest. The machine reboots in its turn, and its next scan answers. |
| `Unknown` | `Applying` | A machine waits for its rehearsal, its turn, or its write. |

An apply pod that ends before the signal wrote nothing, because the
pod writes only after the signal. Its record reads `Failed`, and the
operator deletes the `Disruption`, so the machine leaves its turn with
no reboot. When the operator cannot tell whether an append was
written, because the apply pod with the signal was deleted, ran past
its deadline of 10 minutes from the signal, or crashed, it does not
withdraw the turn. The same is true when the firmware accepted a write
of the append and then refused or failed a later one, because the
variable changed. A write may have landed before the pod stopped,
and the kernel enforces it only after a reboot, so the machine reboots
in the same turn. The next scan then reads the version: the approved
one ends the record, and the old one makes it `Failed`.

The operator does not write again on its own after `Refused` or
`Failed`. To try again, withdraw the approval and approve it again,
which removes the records.
[Troubleshoot](../troubleshoot/#applies) covers each case.

The turn needs a `liken` release whose `Disruption` has
`spec.hold`. On an older release, `machine-operator` reboots the
machine without a hold, and the record reads `Failed` with
`the machine left its turn before the hold took effect`. On a release
with no `Disruption` kind, the operator asks for no turn, and the
machines stay pending.

### A written update cannot be undone

A machine has one variable store, and the firmware keeps no earlier
copy of `dbx`, `db`, or `KEK`. Withdrawing the approval after a write
leaves the new entries in place. A firmware that resets Secure Boot to
its defaults, from its setup menu or on its own, discards the appended
entries, and the next scan then offers the release again.

## Read the Events

The operator posts an `Event` each time `Approved`, `Rehearsed`, or
`Applied` changes, in the namespace `default`. A change to `True` is
`Normal`, and a change to `False` is a `Warning`, so a withdrawn
release shows in `kubectl describe firmware <name>` and in
`kubectl get events --field-selector type=Warning`. It also posts
`RehearsalStarted` when a rehearsal pod starts, `RehearsalPassed` when
one passes, `RehearsalCatalogMoved` when one finds that LVFS published
a new catalog since the machine's scan, and a `Warning`
`RehearsalRefused` or `RehearsalFailed` when one does not pass. It
posts `ApplyPreparing` when it starts the apply pod,
`ApplyCatalogMoved` when the pod finds a new catalog and starts again, `ApplyRequested` when it asks for the machine's turn,
`ApplyStarted` when it sends the apply pod the signal to write,
`ApplyWritten` when the firmware took the append, a `Warning`
`ApplyRefused` or `ApplyFailed` when it did not, and a `Warning`
`ApplyUncertain` when the pod ended without saying.

## Withdraw an approval

Remove `version` and `cabinet` from `spec.update`, and `spec.machines`
with them. The operator removes the `Approved`, `Rehearsed`, and
`Applied` conditions and the records in `status.rehearsals` and
`status.applies`, and `PENDING` returns to 0. It deletes each
`Disruption` that still holds a machine's turn, so the machine leaves
its turn with no reboot, and each apply pod that waits for a turn. A machine whose update is already written
keeps it, and a turn whose hold the operator already removed still
reboots the machine.

An approved `Firmware` is one you changed, so the operator never
deletes it, even when its last entry leaves. Delete it yourself when
you no longer need it. If a scan still finds the component, the
operator creates the object again with no approval.
