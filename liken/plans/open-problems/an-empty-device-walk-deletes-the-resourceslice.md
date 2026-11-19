# An empty device walk deletes the `ResourceSlice`

Open bug, with an open design question. A unit test reproduced it on
2026-11-19. When machine-operator cannot
read a machine's sysfs, it publishes the machine as one with no
devices and deletes its `ResourceSlice`. The scheduler then sees no
devices on the machine, and no failure shows why.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17. The fix rests on milestone 94's rule: when
the code cannot confirm a state, it must refuse or report, and never
act. So a failed read does not count as an empty result.

## The evidence

`SliceWriter.Write` (`kubernetes/resourceslices.go:245`), called from
`machine-operator/dra.go:88`, deletes the slice when the device list
is empty (`kubernetes/resourceslices.go:270`). `usbDevices` and
`boardDevices` (`hardware/inventory.go:52` and `:121`) turn an
unreadable directory into an empty list, and `DiscoverDevices`
(`hardware/sysfs.go:95`) does the same for a bus directory that does
not read. A machine with no devices and a machine whose sysfs could not
be read are the same input.

A unit test published a machine's devices from a fake sysfs, so the fake
API server held its `ResourceSlice`. Then it removed every permission
from the fake sysfs's `bus/usb/devices` directory and published again.
It asserted that the second pass either returns an error or keeps the
slice, and failed with:

```
with /tmp/TestAnUnreadableSysfsDoesNotDeleteTheResourceSlice287170180/001/bus/usb/devices unreadable, the pass returned no error and the ResourceSlice is gone (not found), want the read reported
```

The test is not in the tree, because a failing test cannot land on
`main`.

The comment in `DiscoverDevices` gives a reason to skip a bus
directory that does not exist: the kernel was built without that bus.
So the walk's new error leaves out a directory that does not exist,
and reports every other failed read.

## The fix

The fix is a type change: the walk returns an error, and the slice
writer refuses to delete on an error. Whether a failed walk keeps the
last offer or withdraws it needs a decision: the rule above withholds
an offer it cannot show is safe.

## The design question

This needs the maintainer's decision before the fix is built.

**Whether a failed sysfs walk keeps or withdraws the last offer.**
When the operator cannot read the machine's devices, do pods keep the
devices that the slice offered last, or does the operator withdraw
the offer and show the read error in status? The rule above withholds
an offer it cannot show is safe. The walk's new error must not delete
the slice the way an empty list does now, and nothing decides yet
whether the slice then keeps its last devices or drops them.

## What the lab can measure

The drill runs under QEMU, and needs no metal. A guest whose
`/sys/bus/pci/devices` is unreadable from the operator's mount
namespace: the `ResourceSlice` must follow the decision above, and the
read error must show in status.
