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
| <span id="spec--selector"></span>`selector` | string | no | A CEL expression that selects the devices an upload loads, with the same variables as a `DeviceClass` selector, for example `device.attributes["liken.sh"].vendor == "1618" && device.attributes["liken.sh"].product == "0920"`. `firmware-operator` compiles it with the CEL environment of Kubernetes' dynamic resource allocation, so it means here what it means in a `DeviceClass`. A selector that does not compile sets `Valid` to `False`, and the rule runs nowhere. Required with `upload`, and refused with `update`. |
| <span id="spec--machines"></span>`machines` | [object](#specmachines) | no | A label selector on `Machine` objects. An upload runs only on the machines whose labels it selects, and an approval in `update.version` covers only those machines. Optional. With no value, the upload runs on every machine, and the approval covers every machine. Refused with an `update` that has no `version`. |
| <span id="spec--upload"></span>`upload` | [object](#specupload) | no | The pod that writes the firmware into a device that holds none of its own. `firmware-operator` runs it once for each appearance of a matched device, on the device's machine, with a claim on that one device. |
| <span id="spec--update"></span>`update` | [object](#specupdate) | no | Marks the rule for the firmware of a device that keeps its own firmware, such as a board's UEFI firmware or an SSD. `firmware-operator` creates one such `Firmware` for each catalog component that a scan finds a release of, and one for each set of GUIDs of an updatable device with no release. Each carries the label `firmware.liken.sh/discovered`. `status.machines` records the version each machine runs and the releases each `FirmwareCatalog` offers. A person approves one release with `version` and `cabinet`. The approval installs nothing. |

### spec.machines

A label selector on `Machine` objects. An upload runs only on the machines whose labels it selects, and an approval in `update.version` covers only those machines. Optional. With no value, the upload runs on every machine, and the approval covers every machine. Refused with an `update` that has no `version`.

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

The pod that writes the firmware into a device that holds none of its own. `firmware-operator` runs it once for each appearance of a matched device, on the device's machine, with a claim on that one device.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specupload--image"></span>`image` | string | yes | The image that holds the loader and the firmware files, usually the vendor's own image, such as `ghcr.io/liken-sh/indi-qhy`. The pod runs it as user 1000 with no capabilities and a read-only root file system. |
| <span id="specupload--command"></span>`command` | []string | yes | The loader's command, such as `[fxload, -t, fx2, -I, /lib/firmware/qhy/QHY5II.HEX, -D, $(LIKEN_DEVICE)]`. The pod sets `LIKEN_DEVICE` to the device's usbfs node, `/dev/bus/usb/<bus>/<device>`, and Kubernetes expands `$(LIKEN_DEVICE)` in the command. The upload succeeds when the command exits 0 and the bare device leaves the machine's `ResourceSlice` within one minute. |
| <span id="specupload--args"></span>`args` | []string | no | Arguments after the command. Kubernetes expands `$(LIKEN_DEVICE)` in them too. Optional. |

### spec.update

Marks the rule for the firmware of a device that keeps its own firmware, such as a board's UEFI firmware or an SSD. `firmware-operator` creates one such `Firmware` for each catalog component that a scan finds a release of, and one for each set of GUIDs of an updatable device with no release. Each carries the label `firmware.liken.sh/discovered`. `status.machines` records the version each machine runs and the releases each `FirmwareCatalog` offers. A person approves one release with `version` and `cabinet`. The approval installs nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specupdate--component"></span>`component` | string | no | The ID of the catalog component, such as `org.linuxfoundation.dbx.x64.firmware`. A device belongs to the rule when a catalog offers it a release of this component. A device keeps its entry after it installs the newest release, by its fwupd device ID. A discovered `Firmware` is named by this ID with no top-level domain and no `firmware` part, such as `microsoft-dbx-x64`, or by the whole ID in lowercase when another `Firmware` has that name. |
| <span id="specupdate--guids"></span>`guids` | []string | no | The GUIDs that fwupd reports for a device that no catalog offers a release for, sorted. A device whose set of GUIDs is exactly this set belongs to the rule, until a catalog offers it a release. A discovered `Firmware` is named by the device's name and eight hex digits of a hash of this set, such as `uefi-ca-7420e8dc`. |
| <span id="specupdate--version"></span>`version` | string | no | The version of the component that a person approves, as `status.machines` lists it in a release, such as `20260707`. Optional. It needs `component` and `cabinet`. `firmware-operator` answers it with the `Approved` condition and `status.counts.pending`. With no value, the `Firmware` approves nothing. |
| <span id="specupdate--cabinet"></span>`cabinet` | [object](#specupdatecabinet) | no | The cabinet file of the approved release, copied from the release in `status.machines`. Required with `version`. |

#### spec.update.cabinet

The cabinet file of the approved release, copied from the release in `status.machines`. Required with `version`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specupdatecabinet--url"></span>`url` | string | yes | Where the cabinet downloads from. The approval does not compare it with the catalog's URL, so a mirror works. Pattern: `^https://`. |
| <span id="specupdatecabinet--sha256"></span>`sha256` | string | yes | The cabinet's SHA-256, in lowercase hex. It pins the file the way a digest pins an image: a catalog that offers the version in another cabinet does not match the approval. Pattern: `^[0-9a-f]{64}$`. |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The generation of the spec that the status answers. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | An upload has `Valid`. It is `True` with the reason `Compiled` when the selector and `spec.machines` compile. It is `False` with the reason `SelectorInvalid` when either one does not, and the message gives the error. A rule that is not valid runs nowhere. An update with `version` has `Approved`, over the machines that `spec.machines` covers. It is `True` with the reason `Offered` when a catalog offers the version in the pinned cabinet to at least one machine, and with `Installed` when none is offered it and at least one runs it. It is `False` with `CabinetDiffers` when a catalog offers the version only in another cabinet, with `NotOffered` when no catalog offers it, and with `MachinesInvalid` when `spec.machines` does not compile. The message names the machines. An approval that a catalog offers to at least one machine also has `Rehearsed`, over the rehearsals in `rehearsals`. It is `True` with the reason `Passed` when the install passed its rehearsal on every machine the approval waits for. It is `False` with `Refused` when a rehearsal shows that the install writes more than one authenticated append, or that the variable store has no room for it, and with `Failed` when a rehearsal could not run. It is `Unknown` with `Rehearsing` while a machine waits for its rehearsal, which includes a machine whose rehearsal found the catalog moved. The message names the machines. An approval with a machine that asked for its reboot turn also has `Applied`, over the records in `applies`. It is `True` with the reason `Written` when the firmware took the update on every machine the approval waits for. It is `False` with `Refused` when the firmware refused the write on a machine, and with `Failed` when a write could not run, an apply pod ended before the signal to write, a machine left its turn before the hold, or a machine still reads the old version after its reboot. It is `Unknown` with `Uncertain` when the operator cannot tell whether a write landed, until the scan after the machine's reboot answers, and with `Applying` while a machine waits for its rehearsal, its turn, or its write. The message names the machines. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | One entry for each port, a machine and a USB address, where the rule matched a device. The entry describes the latest appearance of a device in the port, so the list grows only with the number of ports. A `Loaded` entry stays after the bare device leaves. |
| <span id="status--machines"></span>`machines` | [\[\]object](#statusmachines) | no | One entry for each machine's device in an update rule's group, from that machine's last scan against each `FirmwareCatalog`. A scan replaces its machine's entries. An entry leaves only when the machine's next scan does not report the device, or when its `Machine` is deleted, so a machine that is off keeps its entries. |
| <span id="status--rehearsals"></span>`rehearsals` | [\[\]object](#statusrehearsals) | no | One record for each machine that an approval waits for, from the latest rehearsal of the approved install on that machine. A rehearsal runs the install against a copy of the machine's firmware variables in memory, and records what it would write. It runs once for each boot of the machine, device, approved version and cabinet, and catalog file. A record leaves when its machine is no longer pending, or when the approval changes. |
| <span id="status--applies"></span>`applies` | [\[\]object](#statusapplies) | no | One record for each machine that an approval waits for and whose rehearsal passed on the boot it runs. The operator starts an apply pod on the machine, which rehearses the install again while the cluster is whole and then waits. When that prepare is done, the pod's readiness probe turns it `Ready`, and the operator files a `Disruption` with `spec.hold`, so the turn stops after the drain. When the turn holds, the operator sets the pod's annotation `firmware.liken.sh/write` to `go`, and the pod writes the recorded append to the real variable with no network. The operator then removes `spec.hold`, so the machine reboots. The machine's next scan proves the write. A record leaves when its machine is no longer pending, or when the approval changes. The operator does not write again after a refused or failed record. A person who withdraws the approval and approves it again removes the record, and the machine rehearses and asks for its turn again. An apply pod that finds the catalog's jcat naming another file than the machine's scan read writes nothing and asks for no turn. Its record leaves, the operator reads the jcat again at once, and the apply starts again with the new file. |
| <span id="status--counts"></span>`counts` | [object](#statuscounts) | no | The counts of the rule's own kind. An upload has the number of entries in `devices` with each result. An update has the machines that `machines` names, the ones a catalog offers a release, and the ones an approval waits for. A rule writes its own counts even when they are 0, and never the other kind's, so the other kind's printer columns stay empty. |

### status.conditions[]

An upload has `Valid`. It is `True` with the reason `Compiled` when the selector and `spec.machines` compile. It is `False` with the reason `SelectorInvalid` when either one does not, and the message gives the error. A rule that is not valid runs nowhere. An update with `version` has `Approved`, over the machines that `spec.machines` covers. It is `True` with the reason `Offered` when a catalog offers the version in the pinned cabinet to at least one machine, and with `Installed` when none is offered it and at least one runs it. It is `False` with `CabinetDiffers` when a catalog offers the version only in another cabinet, with `NotOffered` when no catalog offers it, and with `MachinesInvalid` when `spec.machines` does not compile. The message names the machines. An approval that a catalog offers to at least one machine also has `Rehearsed`, over the rehearsals in `rehearsals`. It is `True` with the reason `Passed` when the install passed its rehearsal on every machine the approval waits for. It is `False` with `Refused` when a rehearsal shows that the install writes more than one authenticated append, or that the variable store has no room for it, and with `Failed` when a rehearsal could not run. It is `Unknown` with `Rehearsing` while a machine waits for its rehearsal, which includes a machine whose rehearsal found the catalog moved. The message names the machines. An approval with a machine that asked for its reboot turn also has `Applied`, over the records in `applies`. It is `True` with the reason `Written` when the firmware took the update on every machine the approval waits for. It is `False` with `Refused` when the firmware refused the write on a machine, and with `Failed` when a write could not run, an apply pod ended before the signal to write, a machine left its turn before the hold, or a machine still reads the old version after its reboot. It is `Unknown` with `Uncertain` when the operator cannot tell whether a write landed, until the scan after the machine's reboot answers, and with `Applying` while a machine waits for its rehearsal, its turn, or its write. The message names the machines.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition holds. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word for the cause. |
| <span id="statusconditions--message"></span>`message` | string | no | The cause, for a person to read. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The generation of the spec that the condition answers. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the status last changed. |

### status.devices[]

One entry for each port, a machine and a USB address, where the rule matched a device. The entry describes the latest appearance of a device in the port, so the list grows only with the number of ports. A `Loaded` entry stays after the bare device leaves.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdevices--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusdevices--device"></span>`device` | string | yes | The device's name in the machine's `ResourceSlice`. |
| <span id="statusdevices--address"></span>`address` | string | yes | The USB port path, such as `1-4.1.2`. |
| <span id="statusdevices--devicenumber"></span>`deviceNumber` | integer | yes | The device number that the kernel gave this appearance. A new number is a new appearance, after a replug or a power cycle, and gets a new upload. |
| <span id="statusdevices--generation"></span>`generation` | integer | yes | The generation of the rule that this entry ran. A new generation gets a new upload. |
| <span id="statusdevices--result"></span>`result` | string | yes | `Running` while the upload pod runs. `Loaded` when the loader exited 0 and the bare device left the slice. `Failed` when the loader failed, the device stayed for a minute after it exited 0, the pod did not finish within five minutes, or the pod is gone. `Conflict` when another rule matches the same device, so neither runs. A `Failed` upload does not run again until the device appears again or the rule changes. One of: `Running`, `Loaded`, `Failed`, `Conflict`. |
| <span id="statusdevices--message"></span>`message` | string | no | Why the entry has its result, such as the loader's exit code and the last line of its log. |
| <span id="statusdevices--time"></span>`time` | string | yes | When the entry got its result. |

### status.machines[]

One entry for each machine's device in an update rule's group, from that machine's last scan against each `FirmwareCatalog`. A scan replaces its machine's entries. An entry leaves only when the machine's next scan does not report the device, or when its `Machine` is deleted, so a machine that is off keeps its entries.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmachines--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusmachines--device"></span>`device` | string | yes | The device's name, as fwupd reports it, such as `System Firmware`. |
| <span id="statusmachines--deviceid"></span>`deviceId` | string | no | fwupd's ID of the device on the machine. It keeps the entry in its component's object after the device installs the newest release. |
| <span id="statusmachines--version"></span>`version` | string | no | The version of the firmware the device runs, as fwupd reports it. |
| <span id="statusmachines--catalog"></span>`catalog` | string | yes | The `FirmwareCatalog` the scan matched the device against. |
| <span id="statusmachines--file"></span>`file` | string | yes | The catalog file the scan read, such as `firmware-09138-stable.xml.zst`, or a newer file that offers the machine the same releases, which the operator records with no scan. |
| <span id="statusmachines--releases"></span>`releases` | [\[\]object](#statusmachinesreleases) | no | Each release the catalog offers for the device, newest first, as fwupd offered it. Empty when the device runs the newest release the catalog has for it. |
| <span id="statusmachines--time"></span>`time` | string | yes | When the scan that reported the entry ended. |

#### status.machines[].releases[]

Each release the catalog offers for the device, newest first, as fwupd offered it. Empty when the device runs the newest release the catalog has for it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmachinesreleases--version"></span>`version` | string | yes | The release's version. |
| <span id="statusmachinesreleases--id"></span>`id` | string | no | The AppStream ID of the catalog's component that offers the release. |
| <span id="statusmachinesreleases--urgency"></span>`urgency` | string | no | How urgent the vendor says the release is, such as `high`. |
| <span id="statusmachinesreleases--cabinet"></span>`cabinet` | [object](#statusmachinesreleasescabinet) | no | The cabinet file that carries the release. |

#### status.machines[].releases[].cabinet

The cabinet file that carries the release.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmachinesreleasescabinet--url"></span>`url` | string | yes | Where the cabinet downloads from. |
| <span id="statusmachinesreleasescabinet--sha256"></span>`sha256` | string | yes | The cabinet's SHA-256, in hex. |
| <span id="statusmachinesreleasescabinet--size"></span>`size` | integer | no | The size of the firmware payload the release installs, in bytes, as the catalog states it. The cabinet file is larger. |

### status.rehearsals[]

One record for each machine that an approval waits for, from the latest rehearsal of the approved install on that machine. A rehearsal runs the install against a copy of the machine's firmware variables in memory, and records what it would write. It runs once for each boot of the machine, device, approved version and cabinet, and catalog file. A record leaves when its machine is no longer pending, or when the approval changes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrehearsals--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusrehearsals--boot"></span>`boot` | string | yes | The time the machine booted, from its `Machine`'s status, when the rehearsal started. |
| <span id="statusrehearsals--deviceid"></span>`deviceId` | string | yes | fwupd's ID of the device the rehearsal installed on. |
| <span id="statusrehearsals--version"></span>`version` | string | yes | The approved version. |
| <span id="statusrehearsals--cabinetsha256"></span>`cabinetSHA256` | string | yes | The SHA-256 of the approved cabinet. |
| <span id="statusrehearsals--catalog"></span>`catalog` | string | yes | The `FirmwareCatalog` of the machine's last scan. |
| <span id="statusrehearsals--file"></span>`file` | string | yes | The catalog file the rehearsal loaded, which is the one the machine's entry names. |
| <span id="statusrehearsals--result"></span>`result` | string | yes | `Running` while the rehearsal pod runs. `Passed` when the install wrote one authenticated append to its device's own variable, nothing else, and the variable store has room for it. `Refused` when the install would write anything else, or the store has no room. `Failed` when the rehearsal could not run, such as a cabinet whose SHA-256 is not the pinned one. `CatalogMoved` when the catalog's jcat names another file than the machine's scan read. That is no failure: the operator reads the jcat again at once, and the machine rehearses again against the new file, after a scan of it when it offers the machine other releases. One of: `Running`, `Passed`, `Refused`, `Failed`, `CatalogMoved`. |
| <span id="statusrehearsals--message"></span>`message` | string | no | Why the rehearsal has its result. |
| <span id="statusrehearsals--append"></span>`append` | [object](#statusrehearsalsappend) | no | The one write that a passed rehearsal recorded. |
| <span id="statusrehearsals--free"></span>`free` | integer | no | The bytes free in the machine's variable store when the rehearsal ran. |
| <span id="statusrehearsals--time"></span>`time` | string | yes | When the rehearsal got its result. |

#### status.rehearsals[].append

The one write that a passed rehearsal recorded.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrehearsalsappend--variable"></span>`variable` | string | yes | The variable, as efivarfs names its file, such as `dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f`. |
| <span id="statusrehearsalsappend--bytes"></span>`bytes` | integer | yes | The length of the write: four bytes of attributes, then the signed payload. |
| <span id="statusrehearsalsappend--sha256"></span>`sha256` | string | yes | The SHA-256 of the write's bytes, in hex. |

### status.applies[]

One record for each machine that an approval waits for and whose rehearsal passed on the boot it runs. The operator starts an apply pod on the machine, which rehearses the install again while the cluster is whole and then waits. When that prepare is done, the pod's readiness probe turns it `Ready`, and the operator files a `Disruption` with `spec.hold`, so the turn stops after the drain. When the turn holds, the operator sets the pod's annotation `firmware.liken.sh/write` to `go`, and the pod writes the recorded append to the real variable with no network. The operator then removes `spec.hold`, so the machine reboots. The machine's next scan proves the write. A record leaves when its machine is no longer pending, or when the approval changes. The operator does not write again after a refused or failed record. A person who withdraws the approval and approves it again removes the record, and the machine rehearses and asks for its turn again. An apply pod that finds the catalog's jcat naming another file than the machine's scan read writes nothing and asks for no turn. Its record leaves, the operator reads the jcat again at once, and the apply starts again with the new file.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusapplies--machine"></span>`machine` | string | yes | The `Machine`, which has the name of its node. |
| <span id="statusapplies--boot"></span>`boot` | string | yes | The time the machine booted, from its `Machine`'s status, when its rehearsal passed. |
| <span id="statusapplies--version"></span>`version` | string | yes | The approved version. |
| <span id="statusapplies--cabinetsha256"></span>`cabinetSHA256` | string | yes | The SHA-256 of the approved cabinet. |
| <span id="statusapplies--sha256"></span>`sha256` | string | yes | The SHA-256 of the append that the rehearsal recorded, which is the only append the apply writes. |
| <span id="statusapplies--disruption"></span>`disruption` | string | no | The `Disruption` that asks for the machine's reboot turn. It is absent while the apply pod prepares, because the operator files the request only when the pod is `Ready`. |
| <span id="statusapplies--result"></span>`result` | string | yes | `Waiting` while the apply pod prepares, and then while it waits for `machine-operator` to drain the machine and hold its turn. `Writing` after the operator sent the apply pod the signal to write. `Written` when the firmware took the append; the machine then reboots, and the record stays until its next scan. `Uncertain` when the apply pod ended after the signal without saying whether it wrote: it is gone, it ran past its deadline, which counts from the signal, or it crashed. The machine reboots in its turn all the same, and its next scan reads what the firmware holds. `Refused` when the firmware refused the append, which changes nothing. `Failed` when the apply could not run, the apply pod ended or was gone before the signal, the variables changed since the rehearsal, the machine left its turn before the hold, or the machine still reads the old version after its reboot. One of: `Waiting`, `Writing`, `Written`, `Uncertain`, `Refused`, `Failed`. |
| <span id="statusapplies--message"></span>`message` | string | no | Why the record has its result. |
| <span id="statusapplies--time"></span>`time` | string | yes | When the record got its result. |

### status.counts

The counts of the rule's own kind. An upload has the number of entries in `devices` with each result. An update has the machines that `machines` names, the ones a catalog offers a release, and the ones an approval waits for. A rule writes its own counts even when they are 0, and never the other kind's, so the other kind's printer columns stay empty.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscounts--running"></span>`running` | integer | no | Entries that are `Running`. |
| <span id="statuscounts--loaded"></span>`loaded` | integer | no | Entries that are `Loaded`. |
| <span id="statuscounts--failed"></span>`failed` | integer | no | Entries that are `Failed`. |
| <span id="statuscounts--conflict"></span>`conflict` | integer | no | Entries that are `Conflict`. |
| <span id="statuscounts--machines"></span>`machines` | integer | no | The machines that `machines` names. |
| <span id="statuscounts--behind"></span>`behind` | integer | no | The machines with at least one release offered in `machines`. |
| <span id="statuscounts--pending"></span>`pending` | integer | no | The machines that an approval covers and waits for: a catalog offers one of the machine's devices the approved version in the pinned cabinet. |
