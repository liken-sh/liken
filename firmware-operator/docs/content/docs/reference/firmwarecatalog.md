---
title: FirmwareCatalog
weight: 20
toc: true
---

<!-- Generated from deploy/firmwarecatalog-crd.yaml by crdref. Do not edit. -->

# `FirmwareCatalog`

## spec

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--url"></span>`url` | string | yes | The catalog's unversioned URL, such as `https://cdn.fwupd.org/downloads/firmware.xml.zst`. The jcat is at the same URL with `.jcat` added, and it lists the versioned file in the same directory, such as `firmware-09138-stable.xml.zst`. `firmware-operator` and each scan pod read the versioned file, because a CDN can serve an older copy at the unversioned URL for hours. A mirror for a cluster with no route to the internet must serve all three files under its own URL. Pattern: `^https?://`. |
| <span id="spec--trust"></span>`trust` | [object](#spectrust) | no | The certificates that verify a catalog that the LVFS did not sign, such as a vendor's own catalog. fwupd refreshes only from a catalog whose jcat it can verify with a trusted key, and the scan image trusts only the LVFS's keys. Each scan, rehearsal, and apply pod mounts the ConfigMap in `configMapRef` and, before it reads the catalog, copies every key of the ConfigMap into fwupd's trusted keys, next to the LVFS's keys. fwupd reads a key's file extension to tell what kind of certificate it is, so name each key for its format, such as `signer.pem`. Omit this field for the LVFS. |

### spec.trust

The certificates that verify a catalog that the LVFS did not sign, such as a vendor's own catalog. fwupd refreshes only from a catalog whose jcat it can verify with a trusted key, and the scan image trusts only the LVFS's keys. Each scan, rehearsal, and apply pod mounts the ConfigMap in `configMapRef` and, before it reads the catalog, copies every key of the ConfigMap into fwupd's trusted keys, next to the LVFS's keys. fwupd reads a key's file extension to tell what kind of certificate it is, so name each key for its format, such as `signer.pem`. Omit this field for the LVFS.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectrust--configmapref"></span>`configMapRef` | [object](#spectrustconfigmapref) | yes | The ConfigMap, in the namespace of `firmware-operator`, that contains the certificates. When the ConfigMap does not exist, the pod does not start and its scan fails at its deadline. |

#### spec.trust.configMapRef

The ConfigMap, in the namespace of `firmware-operator`, that contains the certificates. When the ConfigMap does not exist, the pod does not start and its scan fails at its deadline.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectrustconfigmapref--name"></span>`name` | string | yes |  |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--file"></span>`file` | string | no | The versioned file that the jcat named at the last successful read. The scans read this file. A failed read leaves it unchanged. |
| <span id="status--sha256"></span>`sha256` | string | no | The SHA-256 of `file`, from the jcat. `firmware-operator` verifies the file against it. |
| <span id="status--etag"></span>`etag` | string | no | The `ETag` of the jcat at the last successful read. The next read sends it in `If-None-Match`, so when the jcat has not changed the server returns `304 Not Modified` with no body. |
| <span id="status--checked"></span>`checked` | string | no | When `firmware-operator` last read the jcat. The LVFS publishes no feed of its changes, so `firmware-operator` reads the jcat again at the interval of its `--catalog-interval` flag, one hour by default. It also reads the jcat at once when the spec of the `FirmwareCatalog` changes or a scan reports `CatalogMoved`. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` with the reason `Fetched` when the last read of the jcat and of the file it lists succeeded. It is `False` with the reason `JcatUnreadable` or `FileUnreadable` when a read failed, and `ChecksumMismatch` when the file's SHA-256 differs from the jcat's. The message gives the error. No scan starts while `Ready` is `False`. |
| <span id="status--machines"></span>`machines` | [\[\]object](#statusmachines) | no | The last scan of each machine against this catalog. `firmware-operator` scans a Ready machine again when the machine reboots, when the device names in its `ResourceSlice` objects change, when the scan image changes, or when the catalog's file changes and offers different releases for the GUIDs that the machine reported. After a `Failed` scan, it does not scan the machine again until one of these changes. |

### status.conditions[]

`Ready` is `True` with the reason `Fetched` when the last read of the jcat and of the file it lists succeeded. It is `False` with the reason `JcatUnreadable` or `FileUnreadable` when a read failed, and `ChecksumMismatch` when the file's SHA-256 differs from the jcat's. The message gives the error. No scan starts while `Ready` is `False`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition is true, false, or not yet known. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | A one-word reason for the status. |
| <span id="statusconditions--message"></span>`message` | string | no | A sentence that explains the reason, for a person to read. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition was computed from. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the status last changed. |

### status.machines[]

The last scan of each machine against this catalog. `firmware-operator` scans a Ready machine again when the machine reboots, when the device names in its `ResourceSlice` objects change, when the scan image changes, or when the catalog's file changes and offers different releases for the GUIDs that the machine reported. After a `Failed` scan, it does not scan the machine again until one of these changes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmachines--machine"></span>`machine` | string | yes | The name of the `Machine`, which is also the name of its node. |
| <span id="statusmachines--boot"></span>`boot` | string | no | The `status.boot.time` of the `Machine` when the scan started. |
| <span id="statusmachines--devices"></span>`devices` | string | yes | A hash of the device names in the machine's `ResourceSlice` objects when the scan started. The hash leaves out device numbers, so unplugging a device and plugging it in again does not change it. |
| <span id="statusmachines--file"></span>`file` | string | yes | The catalog file that the scan read. |
| <span id="statusmachines--image"></span>`image` | string | no | The scan image that ran the scan. A newer image can find devices and releases that an older one did not, so `firmware-operator` scans the machine again when its scan image changes. |
| <span id="statusmachines--releases"></span>`releases` | string | no | A hash of the releases that `file` offers for `guids`. When the catalog moves to a new file with the same hash, `firmware-operator` writes the new file to `file` and does not scan again. |
| <span id="statusmachines--guids"></span>`guids` | []string | no | The GUIDs of every updatable device that the machine reported in its last scan that finished. |
| <span id="statusmachines--result"></span>`result` | string | yes | `Running` while the scan pod runs. `Scanned` when the scan finished, and the `Firmware` objects that `firmware-operator` created describe its devices. `Failed` when the scan failed, did not finish within ten minutes of the pod's creation, or its pod was deleted. `CatalogMoved` when the jcat named a different file from the one the scan was given, and `firmware-operator` then reads the jcat at once. One of: `Running`, `Scanned`, `Failed`, `CatalogMoved`. |
| <span id="statusmachines--message"></span>`message` | string | no | A summary of the result, such as `4 devices, no updates`, or the reason the scan failed. |
| <span id="statusmachines--time"></span>`time` | string | yes | When `result` was last set. |
