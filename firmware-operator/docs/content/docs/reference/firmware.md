---
title: Firmware
weight: 10
toc: true
---

<!-- Generated from deploy/firmware-crd.yaml by crdref. Do not edit. -->

# `Firmware`

## spec

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--selector"></span>`selector` | string | no | A CEL expression that selects the devices an upload loads. It uses the same variables as a `DeviceClass` selector, for example `device.attributes["liken.sh"].vendor == "1618" && device.attributes["liken.sh"].product == "0920"`. `firmware-operator` compiles it in the CEL environment that Kubernetes uses for dynamic resource allocation, so it behaves as it does in a `DeviceClass`. When it does not compile, `Valid` is `False` and the rule runs on no machine. Required with `upload`. Refused with `update`. |
| <span id="spec--machines"></span>`machines` | [object](#specmachines) | no | A label selector on `Machine` objects. An upload runs only on the machines whose labels match it, and an approval in `update.version` covers only those machines. When it is not set, the upload runs on every machine and the approval covers every machine. Refused with an `update` that has no `version`. |
| <span id="spec--upload"></span>`upload` | [object](#specupload) | no | The pod that writes firmware into a device that has none of its own. `firmware-operator` runs it once each time a matched device appears, on the device's machine, with a claim on that device. |
| <span id="spec--update"></span>`update` | [object](#specupdate) | no | Makes the rule cover the firmware of devices that keep their own, such as a board's UEFI firmware or an SSD. `firmware-operator` creates a `Firmware` with this field for each catalog component that a scan finds a release of, and for each set of GUIDs of an updatable device that has no release. It labels each one `firmware.liken.sh/discovered`. `status.machines` lists the version that each machine runs and the releases that each `FirmwareCatalog` offers. To approve one release, set `version` and `cabinet`. `firmware-operator` then rehearses the install on each machine and records the result in `status.rehearsals`. After a rehearsal passes, it writes the update in the machine's reboot turn and records that in `status.applies`. |

### spec.machines

A label selector on `Machine` objects. An upload runs only on the machines whose labels match it, and an approval in `update.version` covers only those machines. When it is not set, the upload runs on every machine and the approval covers every machine. Refused with an `update` that has no `version`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specmachines--matchlabels"></span>`matchLabels` | map[string]string | no | Labels that a `Machine` must have, each with this value. |
| <span id="specmachines--matchexpressions"></span>`matchExpressions` | [\[\]object](#specmachinesmatchexpressions) | no | Requirements on a `Machine`'s labels. A machine must meet all of them. |

#### spec.machines.matchExpressions[]

Requirements on a `Machine`'s labels. A machine must meet all of them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specmachinesmatchexpressions--key"></span>`key` | string | yes | The label's key. |
| <span id="specmachinesmatchexpressions--operator"></span>`operator` | string | yes | How the label's values must compare with `values`. One of: `In`, `NotIn`, `Exists`, `DoesNotExist`. |
| <span id="specmachinesmatchexpressions--values"></span>`values` | []string | no | The values for `In` and `NotIn`. Empty for `Exists` and `DoesNotExist`. |

### spec.upload

The pod that writes firmware into a device that has none of its own. `firmware-operator` runs it once each time a matched device appears, on the device's machine, with a claim on that device.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specupload--image"></span>`image` | string | yes | The image that has the loader and the firmware files, usually the vendor's image, such as `ghcr.io/liken-sh/indi-qhy`. The pod runs it as user 1000, with all capabilities dropped and a read-only root file system. |
| <span id="specupload--command"></span>`command` | []string | yes | The loader's command, such as `[fxload, -t, fx2, -I, /lib/firmware/qhy/QHY5II.HEX, -D, $(LIKEN_DEVICE)]`. The pod sets `LIKEN_DEVICE` to the device's usbfs node, `/dev/bus/usb/<bus>/<device>`, and Kubernetes expands `$(LIKEN_DEVICE)` in the command. The upload succeeds when the command exits 0 and the bare device leaves the machine's `ResourceSlice` within one minute. |
| <span id="specupload--args"></span>`args` | []string | no | Arguments for the command. Kubernetes expands `$(LIKEN_DEVICE)` in them too. Optional. |

### spec.update

Makes the rule cover the firmware of devices that keep their own, such as a board's UEFI firmware or an SSD. `firmware-operator` creates a `Firmware` with this field for each catalog component that a scan finds a release of, and for each set of GUIDs of an updatable device that has no release. It labels each one `firmware.liken.sh/discovered`. `status.machines` lists the version that each machine runs and the releases that each `FirmwareCatalog` offers. To approve one release, set `version` and `cabinet`. `firmware-operator` then rehearses the install on each machine and records the result in `status.rehearsals`. After a rehearsal passes, it writes the update in the machine's reboot turn and records that in `status.applies`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specupdate--component"></span>`component` | string | no | The ID of the catalog component, such as `org.linuxfoundation.dbx.x64.firmware`. A device belongs to the rule when a catalog offers it a release of this component. After the device installs the newest release, no catalog offers it a release, but it stays in this rule, because `firmware-operator` matches its entry by fwupd's device ID. `firmware-operator` names a discovered `Firmware` after this ID, without the top-level domain and without a `firmware` part, such as `microsoft-dbx-x64`. When another `Firmware` already has that name, it uses the whole ID in lowercase. |
| <span id="specupdate--guids"></span>`guids` | []string | no | The GUIDs that fwupd reports for a device that no catalog offers a release for, sorted. A device belongs to the rule when its set of GUIDs is exactly this set, until a catalog offers it a release. `firmware-operator` names a discovered `Firmware` after the device's name and the first eight hex digits of a hash of this set, such as `uefi-ca-7420e8dc`. |
| <span id="specupdate--version"></span>`version` | string | no | The version of the component that you approve, as `status.machines` lists it in a release, such as `20260707`. Optional. Needs `component` and `cabinet`. `firmware-operator` reports on it in the `Approved` condition and in `status.counts.pending`. When it is not set, the `Firmware` approves nothing. |
| <span id="specupdate--cabinet"></span>`cabinet` | [object](#specupdatecabinet) | no | The cabinet file of the approved release, copied from the release in `status.machines`. Required with `version`. |

#### spec.update.cabinet

The cabinet file of the approved release, copied from the release in `status.machines`. Required with `version`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specupdatecabinet--url"></span>`url` | string | yes | Where the cabinet downloads from. It does not have to match the catalog's URL, so you can use a mirror, even over plain HTTP, because each pod checks the cabinet against `sha256` before it uses it. Pattern: `^https?://`. |
| <span id="specupdatecabinet--sha256"></span>`sha256` | string | yes | The cabinet's SHA-256, in lowercase hex. When a catalog offers the version only in a cabinet with another SHA-256, the approval does not match it, and `Approved` is `False` with `CabinetDiffers`. Pattern: `^[0-9a-f]{64}$`. |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the status describes. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | An upload has `Valid`. It is `True` with the reason `Compiled` when the selector and `spec.machines` compile. It is `False` with the reason `SelectorInvalid` when either one does not compile, and the message gives the error. A rule that is not valid runs on no machine. An update with `version` has `Approved`, over the machines that `spec.machines` covers. It is `True` with the reason `Offered` when a catalog offers the version in the pinned cabinet to at least one machine. It is `True` with `Installed` when no machine is offered it and at least one machine runs it. It is `False` with `CabinetDiffers` when a catalog offers the version only in another cabinet, with `NotOffered` when no catalog offers it, and with `MachinesInvalid` when `spec.machines` does not compile. The message lists the machines. An approval that a catalog offers to at least one machine also has `Rehearsed`, over the records in `rehearsals`. It is `True` with the reason `Passed` when the install passed its rehearsal on every machine the approval waits for. It is `False` with `Refused` when a rehearsal shows that the install writes something that a rehearsal does not allow, or that the variable store or the ESP has no room for it. It is `False` with `Failed` when a rehearsal could not run. It is `Unknown` with `Rehearsing` while a machine waits for its rehearsal, including a machine whose rehearsal found that the catalog moved. The message lists the machines. An approval with at least one record in `applies` also has `Applied`. It is `True` with the reason `Written` when the firmware took the update on every machine the approval waits for. It is `False` with `Refused` when the firmware refused the write on a machine, or when a machine must take a capsule and its `status.firmware.bootOrderKept` is not `true`. It is `False` with `Failed` when a write could not run, an apply pod ended before the signal to write, a machine left its reboot turn before `machine-operator` held it, or a machine still reads the old version after its reboot. It is `Unknown` with `Uncertain` when the operator cannot tell whether a write took effect, until the scan after the machine's reboot shows it. It is `Unknown` with `Applying` while a machine waits for its rehearsal, its turn, or its write. The message lists the machines. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | One entry for each port, meaning a machine and a USB address, where the rule matched a device. The entry describes the latest appearance of a device in that port, so the list grows with the number of ports and not with the number of uploads. A `Loaded` entry stays after the bare device leaves. |
| <span id="status--machines"></span>`machines` | [\[\]object](#statusmachines) | no | One entry for each device of each machine that the update rule covers, from that machine's latest scan against each `FirmwareCatalog`. A scan replaces its machine's entries. An entry is removed only when the machine's next scan does not report the device, or when its `Machine` is deleted, so a machine that is off keeps its entries. |
| <span id="status--rehearsals"></span>`rehearsals` | [\[\]object](#statusrehearsals) | no | One record for each machine that an approval waits for, from the latest rehearsal of the approved install on that machine. A rehearsal runs the install against an in-memory copy of the machine's firmware variables and records what the install would write. It runs once for each combination of the machine's boot, the device, the approved version and cabinet, and the catalog file. A record is removed when its machine is no longer pending or when the approval changes. |
| <span id="status--applies"></span>`applies` | [\[\]object](#statusapplies) | no | One record for each machine that an approval waits for and whose rehearsal passed on the boot it runs. The operator starts an apply pod on the machine. The pod rehearses the install again before the drain, while the cluster's DNS still works, and then waits. When that preparation is done, the pod's readiness probe makes it `Ready`, and the operator creates a `Disruption` with `spec.hold`, so the machine's reboot turn stops after the drain. When the turn is held, the operator sets the pod's annotation `firmware.liken.sh/write` to `go`. The pod then writes the recorded append to the real variable, or stages the capsule, with no network. The operator then removes `spec.hold`, so the machine reboots. The machine's next scan shows whether the write took effect. A record is removed when its machine is no longer pending or when the approval changes. The operator does not write again after a record with the result `Refused` or `Failed`. To try again, withdraw the approval and approve it again. That removes the record, and the machine is rehearsed again and requests its turn again. When an apply pod finds that the catalog's jcat lists a different file from the one the machine's scan read, it writes nothing and requests no turn. The operator removes its record, reads the jcat again at once, and starts the apply again with the new file. |
| <span id="status--counts"></span>`counts` | [object](#statuscounts) | no | The counts for the rule's own kind. An upload has the number of entries in `devices` with each result. An update has the number of machines that have an entry in `status.machines`, the number that a catalog offers a release, and the number that an approval waits for. `firmware-operator` writes a rule's own counts even when they are 0, and never the counts of the other kind, so the printer columns of the other kind stay empty. |

### status.conditions[]

An upload has `Valid`. It is `True` with the reason `Compiled` when the selector and `spec.machines` compile. It is `False` with the reason `SelectorInvalid` when either one does not compile, and the message gives the error. A rule that is not valid runs on no machine. An update with `version` has `Approved`, over the machines that `spec.machines` covers. It is `True` with the reason `Offered` when a catalog offers the version in the pinned cabinet to at least one machine. It is `True` with `Installed` when no machine is offered it and at least one machine runs it. It is `False` with `CabinetDiffers` when a catalog offers the version only in another cabinet, with `NotOffered` when no catalog offers it, and with `MachinesInvalid` when `spec.machines` does not compile. The message lists the machines. An approval that a catalog offers to at least one machine also has `Rehearsed`, over the records in `rehearsals`. It is `True` with the reason `Passed` when the install passed its rehearsal on every machine the approval waits for. It is `False` with `Refused` when a rehearsal shows that the install writes something that a rehearsal does not allow, or that the variable store or the ESP has no room for it. It is `False` with `Failed` when a rehearsal could not run. It is `Unknown` with `Rehearsing` while a machine waits for its rehearsal, including a machine whose rehearsal found that the catalog moved. The message lists the machines. An approval with at least one record in `applies` also has `Applied`. It is `True` with the reason `Written` when the firmware took the update on every machine the approval waits for. It is `False` with `Refused` when the firmware refused the write on a machine, or when a machine must take a capsule and its `status.firmware.bootOrderKept` is not `true`. It is `False` with `Failed` when a write could not run, an apply pod ended before the signal to write, a machine left its reboot turn before `machine-operator` held it, or a machine still reads the old version after its reboot. It is `Unknown` with `Uncertain` when the operator cannot tell whether a write took effect, until the scan after the machine's reboot shows it. It is `Unknown` with `Applying` while a machine waits for its rehearsal, its turn, or its write. The message lists the machines.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition is true, false, or not yet known. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word for the cause. |
| <span id="statusconditions--message"></span>`message` | string | no | The cause, for a person to read. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition describes. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the status last changed. |

### status.devices[]

One entry for each port, meaning a machine and a USB address, where the rule matched a device. The entry describes the latest appearance of a device in that port, so the list grows with the number of ports and not with the number of uploads. A `Loaded` entry stays after the bare device leaves.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdevices--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusdevices--device"></span>`device` | string | yes | The device's name in the machine's `ResourceSlice`. |
| <span id="statusdevices--address"></span>`address` | string | yes | The USB port path, such as `1-4.1.2`. |
| <span id="statusdevices--devicenumber"></span>`deviceNumber` | integer | yes | The device number that the kernel gave this appearance. A new number means a new appearance, after a replug or a power cycle, and gets a new upload. |
| <span id="statusdevices--generation"></span>`generation` | integer | yes | The `metadata.generation` of the rule that this entry ran. A new generation gets a new upload. |
| <span id="statusdevices--result"></span>`result` | string | yes | `Running` while the upload pod runs. `Loaded` when the loader exited 0 and the bare device left the slice. `Failed` when the loader failed, the device stayed in the slice for a minute after the loader exited 0, the pod did not finish within five minutes, or the pod is gone. `Conflict` when another rule matches the same device, so neither rule runs. A `Failed` upload does not run again until the device appears again or the rule changes. One of: `Running`, `Loaded`, `Failed`, `Conflict`. |
| <span id="statusdevices--message"></span>`message` | string | no | The reason for the result, such as the loader's exit code and the last line of its log. |
| <span id="statusdevices--time"></span>`time` | string | yes | When the entry got its result. |

### status.machines[]

One entry for each device of each machine that the update rule covers, from that machine's latest scan against each `FirmwareCatalog`. A scan replaces its machine's entries. An entry is removed only when the machine's next scan does not report the device, or when its `Machine` is deleted, so a machine that is off keeps its entries.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmachines--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusmachines--device"></span>`device` | string | yes | The device's name, as fwupd reports it, such as `System Firmware`. |
| <span id="statusmachines--deviceid"></span>`deviceId` | string | no | fwupd's ID of the device on the machine. `firmware-operator` matches the entry to its component's `Firmware` by this ID, so the entry stays there after the device installs the newest release. |
| <span id="statusmachines--version"></span>`version` | string | no | The version of the firmware the device runs, as fwupd reports it. |
| <span id="statusmachines--catalog"></span>`catalog` | string | yes | The `FirmwareCatalog` that the scan matched the device against. |
| <span id="statusmachines--file"></span>`file` | string | yes | The catalog file that the scan read, such as `firmware-09138-stable.xml.zst`. It can instead be a newer file that offers the machine the same releases, which the operator records without a new scan. |
| <span id="statusmachines--releases"></span>`releases` | [\[\]object](#statusmachinesreleases) | no | The releases that the catalog offers for the device, newest first, as fwupd reports them. Empty when the device runs the newest release that the catalog has for it. |
| <span id="statusmachines--time"></span>`time` | string | yes | When the scan that reported the entry finished. |

#### status.machines[].releases[]

The releases that the catalog offers for the device, newest first, as fwupd reports them. Empty when the device runs the newest release that the catalog has for it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmachinesreleases--version"></span>`version` | string | yes | The release's version. |
| <span id="statusmachinesreleases--id"></span>`id` | string | no | The AppStream ID of the catalog component that offers the release. |
| <span id="statusmachinesreleases--urgency"></span>`urgency` | string | no | The urgency that the vendor gives the release, such as `high`. |
| <span id="statusmachinesreleases--cabinet"></span>`cabinet` | [object](#statusmachinesreleasescabinet) | no | The cabinet file that contains the release. |

#### status.machines[].releases[].cabinet

The cabinet file that contains the release.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmachinesreleasescabinet--url"></span>`url` | string | yes | Where the cabinet downloads from. |
| <span id="statusmachinesreleasescabinet--sha256"></span>`sha256` | string | yes | The cabinet's SHA-256, in hex. |
| <span id="statusmachinesreleasescabinet--size"></span>`size` | integer | no | The size in bytes of the firmware payload that the release installs, as the catalog gives it. The cabinet file is larger. |

### status.rehearsals[]

One record for each machine that an approval waits for, from the latest rehearsal of the approved install on that machine. A rehearsal runs the install against an in-memory copy of the machine's firmware variables and records what the install would write. It runs once for each combination of the machine's boot, the device, the approved version and cabinet, and the catalog file. A record is removed when its machine is no longer pending or when the approval changes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrehearsals--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusrehearsals--boot"></span>`boot` | string | yes | The machine's boot time, from its `Machine`'s `status.boot.time`, when the rehearsal started. |
| <span id="statusrehearsals--deviceid"></span>`deviceId` | string | yes | fwupd's ID of the device the rehearsal installed on. |
| <span id="statusrehearsals--version"></span>`version` | string | yes | The approved version. |
| <span id="statusrehearsals--cabinetsha256"></span>`cabinetSHA256` | string | yes | The SHA-256 of the approved cabinet. |
| <span id="statusrehearsals--catalog"></span>`catalog` | string | yes | The `FirmwareCatalog` of the machine's latest scan. |
| <span id="statusrehearsals--file"></span>`file` | string | yes | The catalog file that the rehearsal loaded, which is the file in the machine's entry in `status.machines`. |
| <span id="statusrehearsals--result"></span>`result` | string | yes | `Running` while the rehearsal pod runs. `Passed` when a Secure Boot database install wrote only authenticated appends to its device's own variable and the variable store has room for them, or when a capsule install wrote only the variables and ESP files that a capsule needs and the ESP has room for them. `Refused` when the install would write anything else, when the store or the ESP has no room, or when fwupd updates the device through a plugin that a rehearsal does not cover. `Failed` when the rehearsal could not run, for example when the cabinet's SHA-256 is not the pinned one. `CatalogMoved` when the catalog's jcat lists a different file from the one the machine's scan read. That is not a failure. The operator reads the jcat again at once and rehearses the machine again against the new file. When the new file offers the machine other releases, a scan runs first. One of: `Running`, `Passed`, `Refused`, `Failed`, `CatalogMoved`. |
| <span id="statusrehearsals--message"></span>`message` | string | no | The reason for the result. |
| <span id="statusrehearsals--append"></span>`append` | [object](#statusrehearsalsappend) | no | The authenticated append that a passed rehearsal of a Secure Boot database update recorded. |
| <span id="statusrehearsals--capsule"></span>`capsule` | [object](#statusrehearsalscapsule) | no | What a passed rehearsal of a UEFI capsule recorded: each variable and file that fwupd wrote to the copies of the machine's variables and of the EFI system partition (ESP) of its next boot. |
| <span id="statusrehearsals--free"></span>`free` | integer | no | The free space in bytes in the machine's variable store when the rehearsal ran. |
| <span id="statusrehearsals--time"></span>`time` | string | yes | When the rehearsal got its result. |

#### status.rehearsals[].append

The authenticated append that a passed rehearsal of a Secure Boot database update recorded.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrehearsalsappend--variable"></span>`variable` | string | yes | The variable, named as its file in efivarfs, such as `dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f`. |
| <span id="statusrehearsalsappend--bytes"></span>`bytes` | integer | yes | The length of the append in bytes. For each authenticated write it is four bytes of attributes followed by the signed payload. |
| <span id="statusrehearsalsappend--sha256"></span>`sha256` | string | yes | The SHA-256 of the append's bytes, in hex. |

#### status.rehearsals[].capsule

What a passed rehearsal of a UEFI capsule recorded: each variable and file that fwupd wrote to the copies of the machine's variables and of the EFI system partition (ESP) of its next boot.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrehearsalscapsule--writes"></span>`writes` | [\[\]object](#statusrehearsalscapsulewrites) | yes |  |
| <span id="statusrehearsalscapsule--bytes"></span>`bytes` | integer | yes | The size in bytes of the files that the capsule stages on the ESP. |
| <span id="statusrehearsalscapsule--sha256"></span>`sha256` | string | yes | One SHA-256 over all the writes. The apply compares it with what fwupd writes. |
| <span id="statusrehearsalscapsule--espfree"></span>`espFree` | integer | yes | The free space in bytes on the ESP of the machine's next boot. |

#### status.rehearsals[].capsule.writes[]

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrehearsalscapsulewrites--kind"></span>`kind` | string | yes | `variable` for a firmware variable, or `file` for a file on the ESP. One of: `variable`, `file`. |
| <span id="statusrehearsalscapsulewrites--name"></span>`name` | string | yes | The variable, named as its file in efivarfs, or the file's path on the ESP. |
| <span id="statusrehearsalscapsulewrites--bytes"></span>`bytes` | integer | yes | The size in bytes of the variable or the file after the install. |
| <span id="statusrehearsalscapsulewrites--sha256"></span>`sha256` | string | yes | The SHA-256 of the variable or the file after the install, in hex. |

### status.applies[]

One record for each machine that an approval waits for and whose rehearsal passed on the boot it runs. The operator starts an apply pod on the machine. The pod rehearses the install again before the drain, while the cluster's DNS still works, and then waits. When that preparation is done, the pod's readiness probe makes it `Ready`, and the operator creates a `Disruption` with `spec.hold`, so the machine's reboot turn stops after the drain. When the turn is held, the operator sets the pod's annotation `firmware.liken.sh/write` to `go`. The pod then writes the recorded append to the real variable, or stages the capsule, with no network. The operator then removes `spec.hold`, so the machine reboots. The machine's next scan shows whether the write took effect. A record is removed when its machine is no longer pending or when the approval changes. The operator does not write again after a record with the result `Refused` or `Failed`. To try again, withdraw the approval and approve it again. That removes the record, and the machine is rehearsed again and requests its turn again. When an apply pod finds that the catalog's jcat lists a different file from the one the machine's scan read, it writes nothing and requests no turn. The operator removes its record, reads the jcat again at once, and starts the apply again with the new file.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusapplies--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusapplies--boot"></span>`boot` | string | yes | The machine's boot time, from its `Machine`'s `status.boot.time`, when its rehearsal passed. |
| <span id="statusapplies--version"></span>`version` | string | yes | The approved version. |
| <span id="statusapplies--cabinetsha256"></span>`cabinetSHA256` | string | yes | The SHA-256 of the approved cabinet. |
| <span id="statusapplies--sha256"></span>`sha256` | string | yes | The SHA-256 of the append that the rehearsal recorded. The apply writes only that append. |
| <span id="statusapplies--disruption"></span>`disruption` | string | no | The `Disruption` that requests the machine's reboot turn. It is not set while the apply pod prepares, because the operator creates the `Disruption` only when the pod is `Ready`. |
| <span id="statusapplies--capsule"></span>`capsule` | boolean | no | True for the apply of a UEFI capsule. Its `sha256` is then the digest of all the rehearsal's writes. After the pod stages the capsule, the turn reboots the machine with `BootNext` set to fwupd's updater. |
| <span id="statusapplies--result"></span>`result` | string | yes | `Waiting` while the apply pod prepares, and then while it waits for `machine-operator` to drain the machine and hold its turn. `Writing` after the operator sent the apply pod the signal to write. `Written` when the firmware took the append. The machine then reboots, and the record stays until its next scan. `Uncertain` when the apply pod ended after the signal without reporting whether it wrote, because the pod is gone, ran past its deadline, or crashed. The deadline counts from the signal. The machine reboots in its turn anyway, and its next scan reads what the firmware has. `Refused` when the firmware refused the append, which changes nothing, or when the machine must take a capsule and its `status.firmware.bootOrderKept` is not `true`. `Failed` when the apply could not run, the apply pod ended or was gone before the signal, the variables changed since the rehearsal, the machine left its turn before the hold, or the machine still reads the old version after its reboot. One of: `Waiting`, `Writing`, `Written`, `Uncertain`, `Refused`, `Failed`. |
| <span id="statusapplies--message"></span>`message` | string | no | The reason for the result. |
| <span id="statusapplies--time"></span>`time` | string | yes | When the record got its result. |

### status.counts

The counts for the rule's own kind. An upload has the number of entries in `devices` with each result. An update has the number of machines that have an entry in `status.machines`, the number that a catalog offers a release, and the number that an approval waits for. `firmware-operator` writes a rule's own counts even when they are 0, and never the counts of the other kind, so the printer columns of the other kind stay empty.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscounts--running"></span>`running` | integer | no | Entries that are `Running`. |
| <span id="statuscounts--loaded"></span>`loaded` | integer | no | Entries that are `Loaded`. |
| <span id="statuscounts--failed"></span>`failed` | integer | no | Entries that are `Failed`. |
| <span id="statuscounts--conflict"></span>`conflict` | integer | no | Entries that are `Conflict`. |
| <span id="statuscounts--machines"></span>`machines` | integer | no | The number of machines that have an entry in `status.machines`. |
| <span id="statuscounts--behind"></span>`behind` | integer | no | The number of machines that have at least one release offered in `status.machines`. |
| <span id="statuscounts--pending"></span>`pending` | integer | no | The number of machines that an approval covers and waits for. A machine counts when a catalog offers one of its devices the approved version in the pinned cabinet. |
