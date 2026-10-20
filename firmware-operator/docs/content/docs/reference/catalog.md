---
title: Catalog
weight: 20
toc: true
---

<!-- Generated from deploy/catalog-crd.yaml by crdref. Do not edit. -->

# `Catalog`

## spec

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--url"></span>`url` | string | yes | The catalog's unversioned URL, such as `https://cdn.fwupd.org/downloads/firmware.xml.zst`. The jcat is at the same URL with `.jcat` added, and names the versioned file, such as `firmware-09138-stable.xml.zst`, in the same directory. `firmware-operator` and each scan pod read the versioned file, because a CDN can serve an older copy at the unversioned URL for hours. A mirror for a cluster with no route to the internet serves the same three files under its own URL. Pattern: `^https?://`. |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--file"></span>`file` | string | no | The versioned file the jcat named at the last read that succeeded. The scans read this file. A read that fails keeps it. |
| <span id="status--sha256"></span>`sha256` | string | no | The SHA-256 of `file`, from the jcat. `firmware-operator` checked the file against it. |
| <span id="status--etag"></span>`etag` | string | no | The `ETag` of the jcat at the last read that succeeded. The next read sends it, so a jcat that did not change costs one small answer. |
| <span id="status--checked"></span>`checked` | string | no | When `firmware-operator` last read the jcat. It reads it again an hour later, because LVFS publishes no feed of its changes, and at once when the `Catalog` changes or a scan answers `CatalogMoved`. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` with the reason `Fetched` when the last read of the jcat and of the file it names succeeded. It is `False` with the reason `JcatUnreadable`, `FileUnreadable`, or `ChecksumMismatch` when a read failed or the file's SHA-256 differs from the jcat's, and the message gives the error. No scan starts while `Ready` is `False`. |
| <span id="status--machines"></span>`machines` | [\[\]object](#statusmachines) | no | The record of each machine's last scan against this catalog. `firmware-operator` scans a Ready machine again when the time it booted changes, when the set of device names in its `ResourceSlice` changes, or when the catalog names a new file that offers other releases for the GUIDs the machine reported. Each attempt runs once, so a `Failed` scan waits for one of the three to change. |

### status.conditions[]

`Ready` is `True` with the reason `Fetched` when the last read of the jcat and of the file it names succeeded. It is `False` with the reason `JcatUnreadable`, `FileUnreadable`, or `ChecksumMismatch` when a read failed or the file's SHA-256 differs from the jcat's, and the message gives the error. No scan starts while `Ready` is `False`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition holds. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word for the cause. |
| <span id="statusconditions--message"></span>`message` | string | no | The cause, for a person to read. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The generation of the spec that the condition answers. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the status last changed. |

### status.machines[]

The record of each machine's last scan against this catalog. `firmware-operator` scans a Ready machine again when the time it booted changes, when the set of device names in its `ResourceSlice` changes, or when the catalog names a new file that offers other releases for the GUIDs the machine reported. Each attempt runs once, so a `Failed` scan waits for one of the three to change.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmachines--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusmachines--boot"></span>`boot` | string | no | The `Machine`'s `status.boot.time` when the scan started. |
| <span id="statusmachines--devices"></span>`devices` | string | yes | A hash of the device names in the machine's `ResourceSlice` when the scan started. A device number that changes on a replug does not change it. |
| <span id="statusmachines--file"></span>`file` | string | yes | The catalog file the scan read. |
| <span id="statusmachines--releases"></span>`releases` | string | no | A hash of the releases that `file` offers for `guids`. When the catalog names a new file with the same hash, `firmware-operator` records the new file and does not scan. |
| <span id="statusmachines--guids"></span>`guids` | []string | no | The GUIDs of every updatable device the machine reported in its last scan that answered. |
| <span id="statusmachines--result"></span>`result` | string | yes | `Running` while the scan pod runs. `Scanned` when the scan answered, and the discovered `Firmware` objects hold its devices. `Failed` when the scan failed, did not finish within ten minutes, or its pod is gone. `CatalogMoved` when the jcat named another file than the one the scan was given, and `firmware-operator` then reads the jcat at once. One of: `Running`, `Scanned`, `Failed`, `CatalogMoved`. |
| <span id="statusmachines--message"></span>`message` | string | no | What the scan found, such as `4 devices, no updates`, or why it failed. |
| <span id="statusmachines--time"></span>`time` | string | yes | When the record got its result. |
