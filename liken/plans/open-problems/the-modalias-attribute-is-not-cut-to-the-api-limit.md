# The modalias attribute is not cut to the API limit

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19 with a modalias written for the test. Nobody has found a
modalias that long on a real machine.

machine-operator cuts a device's `name` and `serial` to 64
characters, the API's limit for a string attribute of a
`ResourceSlice` device, but it writes `modalias` uncut. When a
modalias is longer than 64 characters, the API server can refuse the
whole `ResourceSlice`, and no device of the machine can be allocated.

## The evidence

`inventoryDevices` (`machine-operator/dra.go:149`) passes `d.Name` and
`d.Serial` through `attributeString` (`machine-operator/dra.go:284`), which cuts a value to 64
characters, and passes `d.Modalias` as it is. The comment on
`attributeString` says that names from the hardware and from
`pci.ids` can be longer than the limit. A modalias comes from sysfs.
The modaliases of PCI and USB devices are shorter than 64 characters,
but an ACPI or platform modalias can list several IDs. Nobody checked
whether a modalias on a real machine is longer than 64 characters.

## The reproduction

A test gave `inventoryDevices` one platform device with an ACPI
modalias of 67 characters that lists several IDs, and a device node to
deliver. It asserted that every string attribute of the slice device
is at most 64 characters long, and it failed with:

```
attribute modalias has 67 characters, want at most the API's limit of 64
```

The test is not in the tree, because a failing test cannot land on
`main`. A survey of `/sys/bus/*/devices/*/modalias` on the lab guests
and the test clusters for the longest value would show whether a real
machine has one this long.

## What a fix has to decide

This needs the maintainer's decision before a fix is built.

**What the operator publishes for a modalias longer than 64
characters.** It can cut the value to 64 characters, like `name`, so
the device keeps a `modalias` attribute, but a `DeviceClass` selector
that compares the full modalias no longer matches it. Or it can leave
the attribute out of that device, so a selector that tests
`has(device.attributes["liken.sh"].modalias)` does not match it.
