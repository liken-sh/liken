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
| <span id="spec--selector"></span>`selector` | string | yes | A CEL expression that selects the devices this rule loads, with the same variables as a `DeviceClass` selector, for example `device.attributes["liken.sh"].vendor == "1618" && device.attributes["liken.sh"].product == "0920"`. `firmware-operator` compiles it with the CEL environment of Kubernetes' dynamic resource allocation, so it means here what it means in a `DeviceClass`. A selector that does not compile sets `Valid` to `False`, and the rule runs nowhere. |
| <span id="spec--machines"></span>`machines` | [object](#specmachines) | no | A label selector on `Machine` objects. The rule runs only on the machines whose labels it selects. Optional. With no value, the rule runs on every machine. |
| <span id="spec--upload"></span>`upload` | [object](#specupload) | yes | The pod that writes the firmware into a device that holds none of its own. `firmware-operator` runs it once for each appearance of a matched device, on the device's machine, with a claim on that one device. |

### spec.machines

A label selector on `Machine` objects. The rule runs only on the machines whose labels it selects. Optional. With no value, the rule runs on every machine.

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

## status

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The generation of the spec that the status answers. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Valid` is `True` with the reason `Compiled` when the selector and `spec.machines` compile. It is `False` with the reason `SelectorInvalid` when either one does not, and the message gives the error. A rule that is not valid runs nowhere. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | One entry for each port, a machine and a USB address, where the rule matched a device. The entry describes the latest appearance of a device in the port, so the list grows only with the number of ports. A `Loaded` entry stays after the bare device leaves. |
| <span id="status--counts"></span>`counts` | [object](#statuscounts) | no | The number of entries in `devices` with each result. |

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

### status.counts

The number of entries in `devices` with each result.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscounts--running"></span>`running` | integer | no | Entries that are `Running`. |
| <span id="statuscounts--loaded"></span>`loaded` | integer | no | Entries that are `Loaded`. |
| <span id="statuscounts--failed"></span>`failed` | integer | no | Entries that are `Failed`. |
| <span id="statuscounts--conflict"></span>`conflict` | integer | no | Entries that are `Conflict`. |
