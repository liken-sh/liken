---
title: Approve a firmware update
weight: 27
description: "Approve one release of a firmware component that firmware-operator discovered, by setting spec.update.version and pinning the cabinet's URL and SHA-256 on its Firmware, and limit the approval to some machines with spec.machines. Read the Approved condition, its reasons Offered, Installed, CabinetDiffers, NotOffered, and MachinesInvalid, the Version, Approved, and Pending columns, and the Events. Withdraw an approval. Use when a person wants a board's UEFI firmware, the Secure Boot revocation list, or another device to move to a newer release, or when an approval is False."
---

# Approve a firmware update

At the end of this guide, a discovered `Firmware` records which
release you approve, pinned to its cabinet file, and its status shows
which machines wait for it. `firmware-operator` installs nothing yet:
the approval records your decision, and a later release applies it.

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
NAME                        VALID    LOADED   RUNNING   FAILED   CONFLICT   MACHINES   BEHIND   VERSION    APPROVED   PENDING   AGE
microsoft-dbx-x64-kek2023                                                   1          1        20260707   True       1         2d
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

## Read the Events

The operator posts an `Event` each time `Approved` changes, in the
namespace `default`. A change to `True` is `Normal`, and a change to
`False` is a `Warning`, so a withdrawn release shows in
`kubectl describe firmware <name>` and in
`kubectl get events --field-selector type=Warning`.

## Withdraw an approval

Remove `version` and `cabinet` from `spec.update`, and `spec.machines`
with them. The operator removes the `Approved` condition, and
`PENDING` returns to 0.

An approved `Firmware` is one you changed, so the operator never
deletes it, even when its last entry leaves. Delete it yourself when
you no longer need it. If a scan still finds the component, the
operator creates the object again with no approval.
