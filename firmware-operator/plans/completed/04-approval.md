# 04, Approval

Proposed on 2026-10-20. Built on 2026-10-20, and drilled on liken-1
the same day, under [The drill](#the-drill). This plan lets a person approve one version of a catalog
component for the machines that have it, and shows in status which
machines the approval waits for. It applies nothing:
[plan 05](../05-applying-updates.md) applies an approved version.
[Plan 03](03-discovery.md) creates the discovered `Firmware` objects
that this plan approves.

## The problem

Plan 03 shows, for each catalog component, the version each machine
runs and the releases each `FirmwareCatalog` offers. Nothing records
which of those releases a person wants. Plan 05 needs that record, and
it needs it pinned to one file: a release's version string is the
vendor's, and a catalog can serve a different cabinet under the same
version.

## The approval

A person sets `spec.update.version` on a `Firmware` for a catalog
component, and pins the cabinet from the release that
`status.machines` lists:

```yaml
spec:
  update:
    component: com.microsoft.dbx.x64-kek2023.firmware
    version: "20260707"
    cabinet:
      url: https://r2.fwupd.org/lvfs-prod/1bf2dd35…-DBXUpdate-20260707-x64.cab
      sha256: 1bf2dd354e35e86138478f17c83724be644e0dc4c2b82fa95a2cfc7ebfad13d5
  machines:
    matchLabels:
      canary: "true"
```

- `version` is the approval. No other field approves an update, and
  `Cluster` and `Machine` gain no field.
- `cabinet` is required with `version`, and refused without it. Its
  `sha256` pins the file the way a digest pins an image. Plan 05 checks
  the cabinet against it before anything reads the file. Its `url` is
  where plan 05 downloads from, and the approval does not compare it
  with the catalog's, so a mirror works.
- `version` needs `component`. A `Firmware` for a set of GUIDs covers
  devices that no catalog offers a release for, so it has nothing to
  approve.
- `spec.machines`, a label selector on `Machine` objects, limits the
  approval to some machines, such as one machine that a person labels
  first. With an
  update, it is allowed only with `version`, because it limits nothing
  else.
- An approved `Firmware` is not pristine, so the operator keeps it
  when its last entry leaves, the way plan 03 keeps any `Firmware` a
  person changed.

The API server enforces each rule of the spec with a CEL rule in the
CRD, so a wrong approval is refused at `kubectl apply`.

## The status

The operator answers an approval with the `Approved` condition and one
count. It reads only the spec, the entries plan 03 writes, and the
labels of each `Machine`, so it needs no new scan and no download.

A machine is covered when `spec.machines` selects it. A covered
machine is pending when a catalog offers its device the approved
version with the pinned SHA-256, and installed when its device runs the
approved version.

| Status | Reason | When |
|---|---|---|
| `True` | `Offered` | At least one covered machine is pending. |
| `True` | `Installed` | No covered machine is pending, and at least one runs the approved version. |
| `False` | `CabinetDiffers` | No covered machine is pending or installed, and a catalog offers the approved version with another SHA-256. |
| `False` | `NotOffered` | No covered machine is pending or installed, and no catalog offers the approved version. |
| `False` | `MachinesInvalid` | `spec.machines` does not compile. |

The message names the machines in each group. A `Firmware` with no
`version` has no `Approved` condition, and removing the `version`
removes it.

`status.counts.pending` is the number of pending machines. It is
plan 05's work list, and it falls to 0 as machines install the
version.

The operator posts an `Event` for each transition of `Approved`, as
for every condition: `Normal` for `True`, and `Warning` for `False`. A
later catalog that withdraws the approved release, or that serves it
in another cabinet, moves `Approved` to `False` at the next scan, so
the transition's `Warning` is the notice.

The printer columns gain `Version`, `Approved`, and `Pending`.

## Considered and set aside

- **Approval that the operator sets.** An option on the operator, or a
  field on a `FirmwareCatalog`, could approve each new release of
  chosen components on its own. Plan 05 has not written to a real
  machine yet, so nothing approves on its own until it has. A later
  plan can add it.
- **A field that keeps a component out of proposals.** A component a
  person never wants to update needs no field: a discovered `Firmware`
  with no `version` approves nothing. Deleting it does not help,
  because the next scan creates it again.
- **The approval on `Machine` or `Cluster`.** One machine can have one
  device of many components, and one component is on many machines.
  The `Firmware` is the object that one approval covers, so the field
  goes there.
- **A version with no pinned cabinet.** The catalog would decide which
  file a machine installs, at the time of the install, and a person
  would approve a string. The pin keeps the approval and the bytes
  together.

## Choices made in the build

- **The approval runs once each pass, after the scans.** It reads the
  entries that the pass's scans just wrote, so a scan and the approval
  it changes land in one pass. A spec edit wakes a pass, and so does a
  change of a `Machine`'s labels.
- **A machine with two devices of one component counts once.** It is
  pending while any of its devices waits, even when another device of
  the same component already runs the version.
- **The `url` is not compared.** Only the SHA-256 pins the file, so an
  approval can name a mirror. The CRD requires `https://`.
- **The schema refuses a `sha256` that is not 64 lowercase hex
  digits,** so a truncated copy of the hash fails at `kubectl apply`
  and does not reach `CabinetDiffers`.

## The drill

Run on 2026-10-20 on liken-1, with the operator at
`2026.10.20-001-dev-005-27b95762`, against the discovered
`microsoft-dbx-x64-kek2023`, whose one entry was liken-1's `dbx` at
20260402 with 20260707 offered. Each step was a `kubectl patch` of the
spec, and each answer arrived within the 4 seconds the drill waited.

- A `version` on `uefi-ca-7420e8dc`, a set of GUIDs, and a `sha256` of
  8 hex digits: the API server refused both, with the CRD's messages.
- `version: "20260707"` with the release's cabinet: `Approved` `True`,
  `Offered`, "a catalog offers 20260707 in the pinned cabinet to
  liken-1", and `PENDING` 1.
- The same version with another SHA-256: `False`, `CabinetDiffers`,
  and `PENDING` 0.
- The right cabinet, with `spec.machines` selecting a label that no
  `Machine` has: `False`, `NotOffered`. The drill labeled no `Machine`.
- `version`, `cabinet`, and `machines` removed: no `Approved`
  condition, and `PENDING` 0.
- The `Events` were a `Normal` `Offered`, a `Warning`
  `CabinetDiffers`, and a `Warning` `NotOffered`. The move from
  `CabinetDiffers` to `NotOffered` kept the `lastTransitionTime`,
  because the status stayed `False`.

The drill did not show `Installed`, or a release that a later catalog
withdraws, because both need the device's version to change. The tests
cover both.
