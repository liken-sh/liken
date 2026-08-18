# The restore set is proven for one BR/EDR device

Open problem. Everything the Secret holds was chosen by reading
BlueZ's source and proven with one DualSense over BR/EDR. Two edges of
that work are unmeasured.

[Plan 03](../completed/03-a-secret-for-each-adapter.md) decides which files
travel. It prices `settings` and `attributes` and sets both aside. It
does not price the two below.

## The adapter's own identity file does not travel

The operator copies each paired device's `info` file and the matching
`cache` entry. It copies nothing that belongs to the adapter itself,
and one adapter file holds a key: `<adapter>/identity`, which holds
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
generates a new IRK after every restore, because the Secret holds no
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
* It includes the `cache` entry, which for an LE device holds the GATT
  database under `[Attributes]`.

A DualSense pairs BR/EDR, so neither claim has been tested on
hardware. The drill
that would settle both is small: pair one LE device, delete the
operator pod, and reconnect it.
