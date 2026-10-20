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
| <span id="spec--machines"></span>`machines` | [object](#specmachines) | no | A label selector on `Machine` objects. An upload runs only on the machines whose labels it selects. Optional. With no value, the upload runs on every machine. Refused with `update`. |
| <span id="spec--upload"></span>`upload` | [object](#specupload) | no | The pod that writes the firmware into a device that holds none of its own. `firmware-operator` runs it once for each appearance of a matched device, on the device's machine, with a claim on that one device. |
| <span id="spec--update"></span>`update` | [object](#specupdate) | no | Marks the rule for the firmware of a device that keeps its own firmware, such as a board's UEFI firmware or an SSD. `firmware-operator` creates one such `Firmware` for each catalog component that a scan finds a release of, and one for each set of GUIDs of an updatable device with no release. Each carries the label `firmware.liken.sh/discovered`. `status.machines` records the version each machine runs and the releases each `FirmwareCatalog` offers. It installs nothing. |

### spec.machines

A label selector on `Machine` objects. An upload runs only on the machines whose labels it selects. Optional. With no value, the upload runs on every machine. Refused with `update`.

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

Marks the rule for the firmware of a device that keeps its own firmware, such as a board's UEFI firmware or an SSD. `firmware-operator` creates one such `Firmware` for each catalog component that a scan finds a release of, and one for each set of GUIDs of an updatable device with no release. Each carries the label `firmware.liken.sh/discovered`. `status.machines` records the version each machine runs and the releases each `FirmwareCatalog` offers. It installs nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specupdate--component"></span>`component` | string | no | The ID of the catalog component, such as `org.linuxfoundation.dbx.x64.firmware`. A device belongs to the rule when a catalog offers it a release of this component. A device keeps its entry after it installs the newest release, by its fwupd device ID. A discovered `Firmware` is named by this ID with no top-level domain and no `firmware` part, such as `microsoft-dbx-x64`, or by the whole ID in lowercase when another `Firmware` has that name. |
| <span id="specupdate--guids"></span>`guids` | []string | no | The GUIDs that fwupd reports for a device that no catalog offers a release for, sorted. A device whose set of GUIDs is exactly this set belongs to the rule, until a catalog offers it a release. A discovered `Firmware` is named by the device's name and eight hex digits of a hash of this set, such as `uefi-ca-7420e8dc`. |

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The generation of the spec that the status answers. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Valid` is `True` with the reason `Compiled` when the selector and `spec.machines` compile. It is `False` with the reason `SelectorInvalid` when either one does not, and the message gives the error. A rule that is not valid runs nowhere. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | One entry for each port, a machine and a USB address, where the rule matched a device. The entry describes the latest appearance of a device in the port, so the list grows only with the number of ports. A `Loaded` entry stays after the bare device leaves. |
| <span id="status--machines"></span>`machines` | [\[\]object](#statusmachines) | no | One entry for each machine's device in an update rule's group, from that machine's last scan against each `FirmwareCatalog`. A scan replaces its machine's entries. An entry leaves only when the machine's next scan does not report the device, or when its `Machine` is deleted, so a machine that is off keeps its entries. |
| <span id="status--counts"></span>`counts` | [object](#statuscounts) | no | The number of entries in `devices` with each result, and the machines that `machines` names. |

### status.conditions[]

`Valid` is `True` with the reason `Compiled` when the selector and `spec.machines` compile. It is `False` with the reason `SelectorInvalid` when either one does not, and the message gives the error. A rule that is not valid runs nowhere.

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
| <span id="statusmachines--file"></span>`file` | string | yes | The catalog file the scan read, such as `firmware-09138-stable.xml.zst`. |
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
| <span id="statusmachinesreleasescabinet--size"></span>`size` | integer | no | The cabinet's size, in bytes. |

### status.counts

The number of entries in `devices` with each result, and the machines that `machines` names.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscounts--running"></span>`running` | integer | no | Entries that are `Running`. |
| <span id="statuscounts--loaded"></span>`loaded` | integer | no | Entries that are `Loaded`. |
| <span id="statuscounts--failed"></span>`failed` | integer | no | Entries that are `Failed`. |
| <span id="statuscounts--conflict"></span>`conflict` | integer | no | Entries that are `Conflict`. |
| <span id="statuscounts--machines"></span>`machines` | integer | no | The machines that `machines` names. |
| <span id="statuscounts--behind"></span>`behind` | integer | no | The machines with at least one release offered in `machines`. |
