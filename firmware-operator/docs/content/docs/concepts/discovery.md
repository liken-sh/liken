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
against a `FirmwareCatalog`, and records the answers in the cluster.
[Find firmware updates](/docs/guides/find-updates/) shows how to read
the records.

## Finding, approving, and applying are separate

Applying a firmware update is risky, and the boot chain belongs to
`init`. Finding an update changes nothing. So discovery stands alone:
it reads the devices and the catalog and writes status, and it
installs nothing. An approval records which version you want, also
with no install, and
[Approve a firmware update](/docs/guides/approve-an-update/) shows
how. The operator then rehearses the install against a copy of each
pending machine's firmware variables, and records what it would
write. For a Secure Boot database update, it then writes that record
in each machine's reboot turn, which `liken` grants under the
machine's disruption budgets, and the next scan proves it. A capsule
update is rehearsed and refused, and is not applied yet.

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
enables one remote for the `FirmwareCatalog`. Every release then comes from a
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

## A Firmware is a catalog component

What you approve is a version of one catalog component, such as
`com.microsoft.dbx.x64.firmware`, the Secure Boot revocation list for
x64 machines. So the operator keeps one `Firmware` for each component
that a catalog offers a release of, and each machine's device that
gets an offer has an entry in its status. `kubectl get firmware` shows
how many of those machines the catalog offers a release for.

The operator does not group by the GUIDs that fwupd reports, because a
device's set of GUIDs does not name one kind of device. fwupd adds a
GUID to the `dbx` device for each Key Exchange Key that the machine
has enrolled, so two machines with one revocation list can report two
sets. A USB device's first GUID holds its revision, which an update
changes.

A device keeps its entry in the component's object after it installs
the newest release and gets no offer. The entry records fwupd's ID of
the device, and the next scan finds the entry by that ID. A device that
no catalog has offered a release has nothing to approve, and the
operator keeps it in an object for its set of GUIDs. When a catalog
offers that device a release, its entry moves to the component's
object, and the operator deletes the emptied object.

The name of a component's object is the component ID with no
top-level domain and no `firmware` part, such as `microsoft-dbx-x64`.
When another `Firmware` already has that name, the operator uses the
whole component ID in lowercase. The name of a GUID set's object is
the device's name and eight hex digits of a hash of the GUIDs, such as
`uefi-device-firmware-042452fb`. Many devices share one name, because
fwupd names each entry of the firmware's ESRT `UEFI Device Firmware`,
so the hash keeps each device's name the same on every cluster.

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
