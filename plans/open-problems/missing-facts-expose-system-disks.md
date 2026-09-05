# Protect system disks when facts are unavailable

Open bug. Review priority: high. If the machine operator cannot read its
facts, it can publish the machine's own disks as devices that workloads
may claim. Claim preparation does not recheck storage protection.

## Failure path

`reconcile` in [reconcile.go](../../machine-operator/reconcile.go) reads
`factsTree.Read()`. An error returns nil facts and produces a false
`FactsPublished` condition. The same pass still calls
`publishDeviceInventory` if reading the `Node` succeeded.

In [dra.go](../../machine-operator/dra.go), `platformBlocks(nil)` returns
an empty protection set. `inventoryDevices` then applies no storage-role
exclusions. A driven disk with deliverable device nodes can appear in the
`ResourceSlice` even when it contains system or state partitions.

`prepareClaim` in [draplugin.go](../../machine-operator/draplugin.go)
resolves the allocation against current sysfs data and writes the device
nodes into a CDI spec. It does not check whether those nodes now back a
storage role. The API reports the facts failure, but the device-access
path fails open.

Exploitation requires both unavailable facts and a workload authorized
to claim a matching `DeviceClass`. Raw disk access could expose stored
credentials or allow corruption of host storage. This is not a claim
that an ordinary pod can cause the facts failure or claim any disk
without the necessary workload permissions.

## Evidence

A temporary fake-sysfs and API fixture reproduced the path. A disk was
excluded with a protection set containing `sda: true`. With missing
facts, the same inventory functions offered it, and preparation wrote
`/dev/sda` into the CDI spec. No live cluster or physical device was used.

The intended storage exclusion is documented in
[the device reference](../../docs/content/docs/reference/devices.md).

## Proposed safeguards

- Treat storage protection as unknown until its completeness can be
  established. A non-nil facts object or a readable subset of roles is
  not sufficient evidence that the remaining disks are safe to offer.
- Withhold device offers whose safety cannot be established. Withdrawing
  the node's inventory is a conservative option when no reliable
  protection set is available. Merely skipping publication can leave an
  unsafe previous offer active.
- Recheck protection during preparation and CDI refresh. Publication
  can lag, API writes can fail, and an allocation may already exist.
  Unknown protection must not become permission to deliver a device.
- Keep legitimate memory-backed machines distinguishable from failed
  reads. A complete record with no disk-backed roles is different from
  an incomplete record whose exclusions are unknown.

Reading storage facts independently of unrelated facts is a possible
improvement. It is safe only if that read establishes the complete
storage-protection set, rather than preserving just the roles it could
read.

## Remedy scope

**Focused isolation safeguards.** This restores the existing promise
that system disks are never delivered to workloads. It needs no new
`DeviceClass` or claim format. A facts failure may prevent new device
use until protection is known; that refusal is preferable to granting
raw access to an unidentified disk.

## Tests needed

Cover unreadable and incomplete facts, a complete memory-backed machine,
and a previously published unsafe slice. Verify that preparation and
refresh refuse protected or uncertain devices even if allocation already
succeeded. Exercise API failure while withdrawing the offer, so local
delivery checks remain effective when publication cannot be repaired.
