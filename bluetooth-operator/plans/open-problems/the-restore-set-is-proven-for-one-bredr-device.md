# The restore set is proven for one BR/EDR device

Open problem. Everything the Secret carries was chosen by reading
BlueZ's source and proven with one DualSense over BR/EDR. Three edges
of that work are unmeasured, and one of them is a defect in the reader
rather than a gap in the set.

[Plan 03](../03-a-secret-for-each-adapter.md) decides which files
travel. It prices `settings` and `attributes` and sets both aside. It
does not price the two below.

## The adapter's own identity file does not travel

The operator copies each paired device's `info` file and the matching
`cache` entry. It copies nothing that belongs to the adapter itself,
and one adapter file holds a key: `<adapter>/identity`, which carries
`[General] IdentityResolvingKey`. That is the adapter's own IRK, the
key a peer uses to resolve this radio's rotating address back to one
identity. `load_irk` in `src/adapter.c` reads it, and writes a fresh
one when the file has no key.

This costs nothing today. `load_irk` has one caller, `set_privacy`,
and `set_privacy` asks for the local IRK only when privacy is on. The
pod's `main.conf` sets no `Privacy` key, so `parse_privacy` in
`src/main.c` leaves `btd_opts.privacy` at `0x00` and bluetoothd never
reads or writes the file. The lab machine has no `identity` file at
all.

The gap opens the day somebody turns privacy on. The adapter then
generates a new IRK after every restore, because the Secret carries no
old one to restore, so it presents a new identity to each peer that
had resolved the previous one.

A *peer* device's IRK is not at risk. `store_irk` writes it into that
device's own `info` file, which already travels.

**What has to be decided.** Either add `identity` to the set, or state
that the operator supports privacy off only and write `Privacy = off`
into `main.conf`, so that the current behaviour is a decision and not
the absence of one.

## No LE device has been through a restore

Plan 03 reasons about BLE in two places, and both were read rather
than measured.

* It waits a settle window before it snapshots, because
  `[General] AddressType` is written on a deferred `g_idle_add` path
  while the key material is written synchronously. On restore,
  `load_devices` reads `AddressType` first and interprets the rest of
  the file with it, so a snapshot taken too early loses the key and an
  LE device with a static random address loads as BR/EDR.
* It carries the `cache` entry, which for an LE device holds the GATT
  database under `[Attributes]`.

A DualSense pairs BR/EDR, so neither claim has met hardware. The drill
that would settle both is small: pair one LE device, delete the
operator pod, and reconnect it.

## ReadTree fails on the less important file

`ReadTree` in `bonds/disk.go` treats the two files by opposite rules.
An `info` file that fails to read is skipped, and the device drops out
of the tree. A `cache` file that fails to read for any reason other
than "not found" fails the whole call.

Each rule has a reason, and each reason is sound alone. An unreadable
or empty `info` is a pairing that did not finish, and writing it back
would give bluetoothd a device directory with no key in it. A missed
`cache` file is a cache file the next write drops, and a BR/EDR HID
device does not reconnect without its SDP records.

Together they rank the two files backwards. The file that *is* the
bond is the one a read failure discards silently, and the file
that only matters once the bond exists is the one that stops the read.

The case is narrow. Both files are 0600 under a 0700 directory, owned
by the user that reads them, so a failure that is not `ErrNotExist`
means something already went wrong. But if it happens, the operator
writes a Secret with that bond missing, and the bond is gone. That is
the exact loss the Secret exists to prevent.

**What has to be decided.** Whether to fail on any `info` read error
that is not `ErrNotExist`, and keep the skip for the missing and empty
cases that the comment actually describes.
