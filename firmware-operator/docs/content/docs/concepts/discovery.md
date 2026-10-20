---
title: How discovery works
weight: 20
---

# How discovery works

A machine's board firmware, its SSD, its dock, and the Secure Boot
revocation list keep their version across a power cycle, and their
vendors publish newer versions on the Linux Vendor Firmware Service
(LVFS). A `liken` machine runs no fwupd daemon, so nothing tells you
which machine runs which version, or that an update exists.
`firmware-operator` answers both questions with a scan of each machine
against a `Catalog`, and records the answers in the cluster.
[Find firmware updates](/docs/guides/find-updates/) shows how to read
the records.

## Finding, approving, and applying are separate

Applying a firmware update is risky, and the boot chain belongs to
`init`. Finding an update changes nothing. So discovery stands alone:
it reads the devices and the catalog and writes status, and it
installs nothing. A later release lets you approve a version, and a
later release applies an approved version one machine at a time.

## A scan runs fwupd's default plugins

The scan runs `fwupdtool` with every plugin that fwupd enables by
default. Turning plugins off looks safe, because a scan needs only a
few, but it can hide the update of a device that the plugin does not
own. The BIOS release of a Framework Laptop 13 needs the `uefi_pk`
plugin, which reports the Secure Boot Platform Key as a device.
Without that plugin fwupd offers no BIOS update, and it gives no
error. So the scan changes only fwupd's remotes, and leaves the
plugin list alone.

The scan also disables the remotes that fwupd's package ships, and
enables one remote for the `Catalog`. Every release then comes from a
catalog that you declared.

## The ESP is an empty directory

fwupd calls a UEFI device updatable only when it finds an EFI system
partition (ESP). It looks for one through `udisks` on the system D-Bus,
and a pod has neither. Without an ESP, fwupd marks every UEFI device
`updatable-hidden` and lists no update for it.

The scan sets `FWUPD_UEFI_ESP_PATH` to an empty directory in the pod.
fwupd then finds an ESP, calls the device updatable, and matches its
releases. The directory is empty and stays empty, because the scan
stages no capsule. Staging a capsule on the real ESP is part of
applying an update, and applying belongs to `init`.

## A Firmware is a device model

fwupd matches a release to a device by the device's GUIDs. So the
operator keeps one `Firmware` for each set of GUIDs, and each machine
with that model has an entry in its status. The name of the object
holds a hash of the GUIDs and no version, so the object stays the same
when a machine installs a new release. The machines that have one
model share the object, and `kubectl get firmware` shows how many of
them the catalog offers a release for.

## Each scan pod reads the versioned catalog file

LVFS names each catalog file with a build number, such as
`firmware-09138-stable.xml.zst`, and its jcat names the current file
with its SHA-256. The same catalog is also at an unversioned URL,
but its content can lag the jcat, because a CDN caches it, and then
its SHA-256 differs from the one the jcat names. The versioned file
does not change, so it always matches its jcat.

So the operator reads the jcat, downloads the versioned file it names,
and checks its SHA-256. Then it gives each scan pod the file name and
the SHA-256. Each scan pod downloads the jcat and the versioned file
itself, and checks both against what the operator gave it. So every
scan reads the exact file the operator chose, and the record of each
scan names it.

A scan pod that finds another file in the jcat does not use it. It
answers `CatalogMoved`, because the operator chose which machines to
scan from the file it read, and the scan must match that file. The
operator reads the jcat at once and starts the scan again against the
new file.

The operator does not check the signature of the jcat. It uses the
catalog only to choose which machines to scan again. It does not judge
whether a release applies to a device. fwupd checks the signature in
each scan pod when it refreshes, and refuses a catalog that fails.

## Scans follow events

A scan starts when something that can change the answer changes: the
machine booted, its devices changed, or the catalog offers other
releases for its devices. A timer that scans again to look for a change
would load the API server, and would read the devices of every machine
for nothing. The only timer is the hourly read of the jcat, because
LVFS publishes no feed of its changes. That read costs one small
answer when nothing changed.
