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
`firmware-operator` scans each machine against a `FirmwareCatalog`
and records the versions and the available updates in `Firmware`
objects. [Find firmware updates](/docs/guides/find-updates/) shows how
to read them.

## Finding, approving, and applying are separate

A firmware update can leave a machine unable to boot, so the operator
writes nothing until you approve a release. A scan reads the devices
and the catalog, writes status, and installs nothing. An approval
records the version you want.
[Approve a firmware update](/docs/guides/approve-an-update/) shows how.
A machine is pending when a catalog offers one of its devices the
approved version. After an approval, the operator rehearses the install on each
pending machine. The rehearsal runs against a copy of the machine's
firmware variables, and for a capsule also against a copy of its ESP.
The operator records what the install would write. Then it applies the
update on each machine during that machine's reboot turn, which `liken` grants
under the machine's disruption budgets. It applies Secure Boot
database updates and UEFI capsules, and refuses an update that another
fwupd plugin installs. The next scan confirms the new version.

## A scan runs fwupd's default plugins

The scan runs `fwupdtool` with every plugin that fwupd enables by
default, because disabling a plugin can hide the update of a device
that the plugin does not own, with no error. For example, the BIOS
update of a Framework Laptop 13 needs the `uefi_pk` plugin, which
reports the Secure Boot Platform Key as a device.

The scan changes only fwupd's remotes. It disables each remote that
fwupd's package includes and adds one remote for the
`FirmwareCatalog`, so every release comes from a catalog that you
declared.

## The scan gives fwupd an empty ESP

fwupd treats a UEFI device as updatable only when it finds an EFI
system partition (ESP). It looks for one through `udisks` on the system
D-Bus, and a pod has no `udisks` and no D-Bus. Without an ESP, fwupd
marks every UEFI device `updatable-hidden` and lists no update for it.

So the scan sets `FWUPD_UEFI_ESP_PATH` to an empty directory in the
pod. fwupd then finds an ESP, treats the device as updatable, and
matches its releases. The directory stays empty, because a scan stages
no capsule. Only the apply pod of an approved capsule writes to the
real ESP.

## One Firmware for each catalog component

You approve a version of one catalog component, such as
`com.microsoft.dbx.x64.firmware`, the Secure Boot revocation list for
x64 machines. So the operator creates one `Firmware` for each component
that a catalog offers a release of, and each machine's device that the
catalog offers it to has an entry in that object's status.

The operator does not group devices by the GUIDs that fwupd reports,
because two devices of the same kind can report different GUIDs. fwupd
adds a GUID to the `dbx` device for each Key Exchange Key that the
machine has enrolled, so two machines with the same revocation list
can report different sets. The first GUID of a USB device contains its
revision, which an update changes.

After a device installs the newest release, the catalog offers it
nothing, and its entry stays in the component's object. The next scan
finds the entry by fwupd's ID of the device. A device that no catalog
offers a release to has no component, so the operator keeps
its entry in an object named for its set of GUIDs. When a catalog
offers that device a release, the operator moves the entry to the
component's object and deletes the object for the GUIDs.

The name of a component's object is the component ID with no top-level
domain and no `firmware` part, such as `microsoft-dbx-x64`. When
another `Firmware` already has that name, the operator uses the whole
component ID in lowercase. The name of an object for a set of GUIDs is
the device's name and eight hex digits of a hash of the GUIDs, such as
`uefi-device-firmware-042452fb`. fwupd names every entry of the
firmware's ESRT `UEFI Device Firmware`, so many devices have the same
name, and the hash keeps their objects apart. The same GUIDs give the
same name on every cluster.

## Each scan pod reads the versioned catalog file

LVFS names each catalog file with a build number, such as
`firmware-09138-stable.xml.zst`, and its jcat, a small signed file,
lists the current file and its SHA-256. The same catalog is also at an
unversioned URL, but a CDN caches that URL, so its content can be older
than the jcat and fail the jcat's SHA-256. A versioned file never
changes, so it always matches its jcat.

So the operator reads the jcat, downloads the versioned file that it
lists, and checks the file's SHA-256. Then it gives each scan pod the
file name and the SHA-256. Each scan pod downloads the jcat and the
file itself and checks both against what the operator gave it, so
every scan reads the file that the operator chose, and the record of
each scan has that file.

When a scan pod finds a different file in the jcat, it reports
`CatalogMoved` and does not scan. The operator chose which machines to
scan by reading its own copy of the file, so the scan must use that
same file. The operator then reads the jcat again in the same pass and
scans against the new file.

The operator does not verify the jcat's signature, because it uses the
catalog only to decide which machines to scan again. fwupd decides
whether a release applies to a device, and it verifies the signature
in each scan pod when it refreshes, and refuses a catalog that fails.

## Scans follow events

A scan starts when something that can change its result changes: the
machine booted, its devices changed, the catalog offers other releases
for its devices, or the operator runs a new scan image. Scanning on a
timer would load the API server and read the devices of every machine
for nothing. The only timer that reads state is the read of the jcat,
once each `--catalog-interval`, an hour by default, because LVFS
publishes no feed of its changes. Each read sends the
`ETag` of the previous one, so an unchanged jcat costs one `304`
response.
