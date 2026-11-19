# The modalias attribute is not cut to the API limit

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

machine-operator cuts a device's `name` and `serial` to 64
characters, the API's limit for a string attribute of a
`ResourceSlice` device, but it writes `modalias` uncut. When a
modalias is longer than 64 characters, the API server can refuse the
whole `ResourceSlice`, and no device of the machine can be allocated.

## The evidence

`inventoryDevices` in `machine-operator/dra.go` passes `d.Name` and
`d.Serial` through `attributeString`, which cuts a value to 64
characters, and passes `d.Modalias` as it is. The comment on
`attributeString` says that names from the hardware and from
`pci.ids` can be longer than the limit. A modalias comes from sysfs.
The modaliases of PCI and USB devices are shorter than 64 characters,
but an ACPI or platform modalias can list several IDs. Nobody checked
whether a modalias on a real machine is longer than 64 characters.

## What would confirm it

A test of `inventoryDevices` with a device whose modalias is longer
than 64 characters, which reads the attribute that it writes. A survey
of `/sys/bus/*/devices/*/modalias` on the lab guests and the test
clusters for the longest value.

## What a fix has to decide

Whether to cut `modalias` like `name`, which makes a long value
unusable for an exact match in a selector, or to leave out an
attribute that is too long.
