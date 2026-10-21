---
name: approve-an-update
description: "Approve one release of a firmware component that firmware-operator discovered, by setting spec.update.version and pinning the cabinet's URL and SHA-256 on its Firmware, and limit the approval to some machines with spec.machines. Read the Approved condition, its reasons Offered, Installed, CabinetDiffers, NotOffered, and MachinesInvalid, the Version, Approved, Pending, and Rehearsed columns, the rehearsal of the install on each machine in status.rehearsals and the Rehearsed condition, and the Events. Withdraw an approval. Use when a person wants a board's UEFI firmware, the Secure Boot revocation list, or another device to move to a newer release, or when an approval is False."
---

This skill is the guide at https://liken.sh/firmware/docs/guides/approve-an-update/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Approve a firmware update

At the end of this guide, a discovered `Firmware` records which
release you approve, pinned to its cabinet file, and its status shows
which machines wait for it and exactly what the install would write
on each one. `firmware-operator` installs nothing yet: it rehearses
the install against a copy of each machine's firmware variables, and
a later release applies it.

You need a discovered `Firmware` whose `status.machines` lists a
release. [Find firmware updates](https://liken.sh/firmware/docs/guides/find-updates/) shows how to read
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
NAME                        VALID    LOADED   RUNNING   FAILED   CONFLICT   MACHINES   BEHIND   VERSION    APPROVED   PENDING   REHEARSED   AGE
microsoft-dbx-x64-kek2023                                                   1          1        20260707   True       1         True        2d
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
liken-1   Passed   the install appends 17260 bytes to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f, and the variable store has 67174 bytes free
```

A Secure Boot database update, such as a `dbx` update, passes when the
install writes one authenticated append to its own variable and
nothing else, and the variable store has room for it. The record's
`append` gives the variable, the length of the write, and its SHA-256.
The firmware verifies the append's signature and merges the new
entries into the list at the write, so the variable grows by less than
the append.

The `Rehearsed` condition sums the records, and the `REHEARSED` column
shows it:

| Status | Reason | Meaning |
|---|---|---|
| `True` | `Passed` | The install passed its rehearsal on every machine the approval waits for. |
| `False` | `Refused` | The install would write more than one authenticated append, such as a boot variable or a file on the ESP, or the store has no room. A capsule update, such as a board's UEFI firmware, is refused for now. |
| `False` | `Failed` | A rehearsal could not run, such as a cabinet whose SHA-256 is not the one you pinned. |
| `Unknown` | `Rehearsing` | A machine waits for its rehearsal. |

The message names the machines and says why.
[Troubleshoot](https://liken.sh/firmware/docs/guides/troubleshoot/#rehearsals) covers each case.

## Read the Events

The operator posts an `Event` each time `Approved` or `Rehearsed`
changes, in the namespace `default`. A change to `True` is `Normal`,
and a change to `False` is a `Warning`, so a withdrawn release shows in
`kubectl describe firmware <name>` and in
`kubectl get events --field-selector type=Warning`. It also posts
`RehearsalStarted` when a rehearsal pod starts, `RehearsalPassed` when
one passes, and a `Warning` `RehearsalRefused` or `RehearsalFailed`
when one does not.

## Withdraw an approval

Remove `version` and `cabinet` from `spec.update`, and `spec.machines`
with them. The operator removes the `Approved` and `Rehearsed`
conditions and the records in `status.rehearsals`, and `PENDING`
returns to 0.

An approved `Firmware` is one you changed, so the operator never
deletes it, even when its last entry leaves. Delete it yourself when
you no longer need it. If a scan still finds the component, the
operator creates the object again with no approval.
