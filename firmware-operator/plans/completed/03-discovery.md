# 03, Discovery

Proposed on 2026-10-20. Built on 2026-10-20 with one object for each
set of GUIDs, and drilled on liken-1 the same day. The drill showed
that a set of GUIDs does not name one kind of device, so
[Grouping devices by catalog component](#grouping-devices-by-catalog-component)
revises how the operator groups devices into objects. The revision
was built and drilled on liken-1 the same day, under
[The second drill](#the-second-drill). A drill of a device that
installs the newest release and keeps its entry needs an approved
update, so it waits for plan 05. This plan finds the firmware that
each machine's devices keep, and the newer releases that a catalog
offers for it, and records both in the cluster. It installs nothing.
[Plan 04](04-approval.md) lets a person approve a version, and
[plan 05](../05-applying-updates.md) applies an approved version, one
machine at a time. The three plans replace the stub
[plan 02](../rejected/02-updates.md). Root
[plan 81](../../../plans/81-firmware.md) records which component owns
each kind of firmware.

## The problem

A machine's board firmware, its SSD, its dock, and the Secure Boot
revocation list (`dbx`) all keep a version across a power cycle, and
their vendors publish newer versions on the Linux Vendor Firmware
Service (LVFS). On a desktop, the fwupd daemon reads the devices,
downloads the LVFS catalog, and tells the user which updates apply. A
`liken` machine runs no fwupd daemon, so nobody can see which machine
runs which firmware, or that an update exists.

Applying an update is risky, and the boot chain belongs to `init`.
Finding updates changes nothing. So this plan builds the finding part
alone, and keeps four questions apart:

1. Which firmware devices does each machine have, at which version?
2. Which newer releases does a catalog offer for each of them?
3. Which version has a person approved? This is plan 04.
4. When does each machine take its turn to apply it? This is plan 05.

This plan answers the first two.

## What the lab measured

A spike on 2026-10-20 ran fwupd 2.1.1 from Ubuntu 26.04 in a
privileged pod with `fwupdtool`, which needs no daemon. It ran on
liken-1 and stick-1, and on two machines of another cluster: a
Framework Laptop 13 and a GMKtec mini PC. Every run only read.

- **The UEFI plugin needs `efivarfs`.** A container's `/sys` has no
  `efivarfs`, and `uefi_capsule` disables itself without it. Mounted
  read-only, it reads each ESRT entry: liken-1 has 4, the GMKtec mini
  PC has 2, the Framework has 1, and stick-1 has none.
- **The ESP gates the answer.** fwupd finds an EFI system partition
  through udisks on the system D-Bus, and udisks reads its block
  devices from udev. A pod has neither, so fwupd marked every UEFI
  device `updatable-hidden` and listed no update for it. A fake ESP on
  a loop device and a udisks daemon in the pod did not help, because
  udisks saw no udev events. The environment variable
  `FWUPD_UEFI_ESP_PATH`, which `libfwupdplugin/fu-path-store.c` reads,
  skips the search: with it pointing at an empty directory, the
  Framework's BIOS became `updatable`.
- **fwupd needs its default plugins.** With `--plugins`, even a list
  of every plugin, `get-updates` offered nothing. With
  `DisabledPlugins` in `fwupd.conf`, disabling plugins one at a time
  showed that the Framework's BIOS release needs `uefi_pk`, which
  reports the Secure Boot Platform Key as a device. Disabling a plugin
  can hide the update of a device that the plugin does not own, with
  no error.
- **LVFS serves each catalog under its own name.** The jcat at
  `https://cdn.fwupd.org/downloads/firmware.xml.zst.jcat` names one
  versioned file, such as `firmware-09138-stable.xml.zst`, with its
  SHA-256. The file at the unversioned URL had a different SHA-256
  than the jcat named, three times in a row: the CDN caches each for
  up to 4 hours. The versioned file matched the jcat.
- **Matching runs offline.** `fwupdtool refresh FILE JCAT REMOTE`
  loaded the versioned catalog, and `get-updates` matched it, in a
  network namespace with no interfaces. A catalog with one byte
  appended failed the refresh with a checksum error.
- **The answers.** On the Framework, fwupd offered BIOS 0.0.4.2 over
  0.0.3.5, and two `dbx` releases. On the GMKtec machines it offered
  nothing, because GMKtec publishes nothing on LVFS. One of liken-1's
  GUIDs, the generic Intel ME GUID, is in the catalog under a Lenovo
  component, and fwupd refused it because the hardware IDs differ.
- **What a release reports.** `get-updates --json` gives each
  release's `AppstreamId`, `ReleaseId`, `Version`, `Checksum` (the
  SHA-1 and SHA-256 of the cabinet), `Locations`, `Size`, and
  `Flags`.
- **What a scan opens.** With only `uefi_capsule`, fwupd opened
  `/dev/dri/card0`, `/dev/urandom`, and the directory `/dev/bus/usb`.
  A scan with every plugin was not traced. fwupd opens a USB device
  only when a plugin claims its IDs, and no plugin claims the rig's
  cameras or the CEC adapter, so the project assumes a scan does not
  disturb them.

## The `FirmwareCatalog` resource

A `FirmwareCatalog` is one source of firmware releases. It is cluster-scoped,
in `firmware.liken.sh/v1alpha1`:

```yaml
apiVersion: firmware.liken.sh/v1alpha1
kind: FirmwareCatalog
metadata:
  name: lvfs
spec:
  url: https://cdn.fwupd.org/downloads/firmware.xml.zst
status:
  file: firmware-09138-stable.xml.zst
  sha256: 5ad15d603bdb60befea76491b19db88b9323f5542e6ea21a770508bb7470f5dc
  checked: "2026-10-20T15:10:00Z"
  conditions:
    - type: Ready
      status: "True"
      reason: Fetched
  machines: [...]   # below
```

- `spec.url` is the catalog's unversioned URL. The jcat is at the same
  URL with `.jcat` added, and the versioned file is in the same
  directory, under the name the jcat gives.
- The operator reads the jcat when the `FirmwareCatalog` changes and once an
  hour after that, with the jcat's `ETag`, so an unchanged jcat costs
  one small response. LVFS has no feed of changes to subscribe to, so
  this timer is the only way to see a new catalog. A comment at the
  timer gives that reason.
- When the jcat names a new file, the operator downloads that file
  once, checks its SHA-256 against the jcat, and writes `file` and
  `sha256`. It reads the file only to decide which machines to scan
  again. It never decides that a release applies, so it does not check
  the jcat's signature: fwupd checks it in each scan pod, and refuses
  a catalog that fails.
- `Ready` is `False` when the jcat or the file cannot be read or the
  SHA-256 differs. The status keeps the last good `file`, and no scan
  starts against a catalog that is not `Ready`.

`deploy/` ships the `lvfs` object. A cluster can add others, such as a
mirror for a cluster with no route to the internet. `lvfs-testing` is
one more object.

## The scan pod

One scan answers both questions for one machine and one `FirmwareCatalog`,
because `get-updates` needs the live devices. The operator creates a
bare pod in `liken-system` on the machine's node. The pod is
privileged, because fwupd reads SMBIOS tables, `efivarfs`, sysfs, and
device nodes, and it holds no claim, because fwupd reads many devices.
Its image, `firmware-operator-scan`, holds fwupd from a dated Ubuntu
26.04 snapshot and one small program, `scan`, which:

1. Mounts `efivarfs` read-only at `/sys/firmware/efi/efivars`.
2. Downloads the jcat, and refuses with the result `CatalogMoved` when
   it names another file than the one in the pod's spec.
3. Downloads the versioned file, and refuses when its SHA-256 differs
   from the one in the pod's spec.
4. Writes one fwupd remote for the `FirmwareCatalog` and disables the remotes
   that the package ships, so every release comes from that catalog.
5. Runs `fwupdtool refresh` with the file and the jcat, which checks
   the signature, then `fwupdtool get-devices --json` and
   `fwupdtool get-updates --json`, with `FWUPD_UEFI_ESP_PATH` set to
   an empty directory, so no ESP is read or written.
6. Writes one line of JSON to its log: the catalog file, each device
   with the `updatable` flag, and the releases fwupd offers for each.

The pod has a deadline of 10 minutes, and a `NetworkPolicy` allows its
egress only to DNS and to port 443. The operator reads the result from
the pod's log, writes it to status, and then deletes the pod.

## When a machine is scanned

The operator scans a machine when one of these changes since its last
scan against a `FirmwareCatalog`:

- **The machine booted.** `status.boot.time` of the `Machine` changed.
  A version changes only across a reboot or a flash.
- **The machine has a new device.** The set of device names in the
  machine's `liken.sh` `ResourceSlice` changed. A device number that
  changes on a replug does not count.
- **The catalog changed the releases for one of its devices.** When the
  `FirmwareCatalog` names a new file, the operator computes, for each machine,
  a hash of the catalog's releases for the GUIDs that the machine
  reported last time. When the hash changed, the machine is scanned.
  When it did not, the operator records the new file for that machine
  and scans nothing.

The record of each machine's last scan is in the `FirmwareCatalog`'s status,
so a restarted operator holds no state in memory:

```yaml
status:
  machines:
    - machine: liken-1
      boot: "2026-10-20T01:47:55Z"
      devices: 3f9a…      # hash of the slice's device names
      file: firmware-09138-stable.xml.zst
      releases: 77c1…     # hash of the catalog's releases for guids
      guids: [69585d92-…, 4e88068b-…, 23192307-…, 2fe2cbfc-…]
      result: Scanned     # Running, Scanned, Failed, or CatalogMoved
      message: 4 devices, no updates
      time: "2026-10-20T15:12:04Z"
```

A machine that is not `Ready` is not scanned. A failed scan does not
run again until the boot, the devices, or the catalog file change, the
way a failed upload waits for a new appearance. A `CatalogMoved` result
makes the operator read the jcat at once instead of at the next hour,
and the new file starts the scan again.

## Discovered `Firmware` objects

The operator keeps one `Firmware` for each firmware device model that
any scan reports with the `updatable` flag. A model is the sorted set
of the device's GUIDs. The object's name is the fwupd device name and
a short hash of the GUIDs, such as `system-firmware-3c1f0a2b`, and it
carries the label `firmware.liken.sh/discovered`.

```yaml
apiVersion: firmware.liken.sh/v1alpha1
kind: Firmware
metadata:
  name: system-firmware-3c1f0a2b
  labels:
    firmware.liken.sh/discovered: "true"
spec:
  update:
    guids: [9c13b7f1-d618-5d68-be61-6b17881014a7]
status:
  machines:
    - machine: node-2
      device: System Firmware
      version: 0.0.3.5
      catalog: lvfs
      file: firmware-09138-stable.xml.zst
      releases:
        - version: 0.0.4.2
          id: work.frame.Laptop.RyzenAI300.BIOS.firmware
          cabinet:
            url: https://fwupd.org/downloads/9c085d03…-Framework_Laptop_13_RyzenAI300_fwupd_4.02.cab
            sha256: 9c085d03d6ea515101a8519f7855bf951e7693ff4f392585bbb692275a8b27f5
            size: 36134500
      time: "2026-10-20T15:12:04Z"
  counts:
    machines: 1
    behind: 1
```

- `spec.update` marks the object as a rule for firmware that a device
  keeps. Its `guids` say which devices the object covers. Plan 04 adds
  `spec.update.version`, the approval. A `Firmware` has exactly one of
  `upload` and `update`, and `selector` belongs to `upload` alone.
- Each entry in `status.machines` is one machine's device, with the
  `FirmwareCatalog`, the file it was matched against, and each release fwupd
  offered with its cabinet's URL and SHA-256. Plan 04 pins an approval
  to that SHA-256.
- `counts.behind` counts the machines with at least one release.
  `kubectl get firmware` shows `MACHINES` and `BEHIND` beside the
  upload columns.
- A scan replaces its machine's entries in every discovered object.
  An entry leaves only when its machine's next scan does not report
  the device, or when its `Machine` is deleted. A machine that is off
  keeps its entries.
- The operator deletes a discovered object when no entry is left and
  `spec.update` holds nothing but `guids`. It never deletes an object
  that a person changed.

## What the drill found

On 2026-10-20, liken-1 ran a development build, and the operator
scanned liken-1 and stick-1 within 40 seconds of its start. liken-1
reported 9 updatable devices and stick-1 5, and the operator created
13 discovered objects. The catalog offered three updates, all to the
Secure Boot databases: stick-1's `dbx` from 2016 to `20260402`, its
UEFI CA from the 2011 certificate to the 2023 one, and liken-1's `dbx`
from `20260402` to `20260707`. liken-1's 4 ESRT devices have no
release on LVFS.

The drill also showed that one kind of device became two objects. The
`dbx` of liken-1 has the GUIDs `f8ba2887…` and `d07ff664…`, and the
`dbx` of stick-1 has only `f8ba2887…`, so each machine's `dbx` got an
object of its own. The UEFI CA and the KEK CA split the same way.

## Grouping devices by catalog component

Proposed and built on 2026-10-20. A set of GUIDs is not a stable name
for a kind of device, for two reasons:

- **A device's GUIDs change with what is installed.** fwupd's `dbx`
  device adds one GUID for each Key Exchange Key certificate that the
  machine has enrolled (`plugins/uefi-dbx/fu-uefi-dbx-device.c` in
  fwupd 2.1.1). liken-1 has Microsoft's 2023 KEK and stick-1 does
  not, so their sets differ, and stick-1's set changes when it enrolls
  that key. The hash of the `dbx` contents is an instance ID for
  quirks only, and fwupd does not publish it as a GUID.
- **No single GUID is the stable one.** For a USB device, fwupd lists
  the GUIDs from the most specific to the least: vendor, product, and
  revision first, then vendor and product, then vendor alone, as the
  [LVFS metainfo guide](https://lvfs.readthedocs.io/en/latest/metainfo.html)
  shows. The revision is often the firmware version, so the first GUID
  changes with an update. A less specific GUID can be too broad: a
  [ModemManager issue](https://gitlab.freedesktop.org/mobile-broadband/ModemManager/-/issues/502)
  reports that one vendor reuses its USB IDs across different modems.

An LVFS release names the GUIDs it flashes, and fwupd offers it when a
device has any one of them. What a person approves in plan 04 is a
version of one catalog component, such as
`org.linuxfoundation.dbx.x64.firmware`. So the operator groups by
component:

- **A device that a catalog offers a release for** is an entry in the
  object of the release's component, the `id` of the release. Its spec
  is `spec.update.component`. liken-1's and stick-1's `dbx` are then
  two entries in one object.
- **A device with no release** has nothing to approve. It stays in an
  object for its set of GUIDs, with `spec.update.guids`. Two of
  these objects for one kind of device cost one extra row and nothing
  else.
- **An entry moves only toward a component.** Each entry records the
  fwupd `DeviceId` of the device on its machine. A scan puts a device
  in the object that already holds its entry, unless the catalog now
  offers it a release of another component. A device that installs the
  newest release gets no offer, and it stays in its component's object
  with no release, so the machines of a component stay together after
  an update. The record is in status, so the rule holds across an
  operator restart.
- A `Firmware` has exactly one of `spec.update.component` and
  `spec.update.guids`. The deletion rule stays: an object with no entry
  and an unchanged spec is deleted, so an object for a set of GUIDs
  goes away when its last device moves to a component.

The names are short enough to read in `kubectl get firmware`. A
component's object is named by the component ID with no top-level
domain and no last part `firmware`, as one DNS label:
`com.microsoft.dbx.x64.firmware` becomes `microsoft-dbx-x64`. A GUID
set's object is named by the device's name, such as `uefi-dbx`. When
another `Firmware` holds the name, such as a person's upload rule or a
GUID set of another laptop's `System Firmware`, the operator uses a
longer name: the whole component ID in lowercase, with each part made
a DNS label, or the device's name and eight hex digits of a hash of
the GUIDs. On 2026-10-20 the LVFS stable catalog had 2,748 component
IDs. The short rule gave 6 pairs of IDs one name, and each pair
differed in case alone, such as `com.hp.laptop.system.S70.firmware`
and `com.hp.laptop.System.S70.firmware`, which the whole ID in
lowercase also gives one name. Dropping the vendor's domain too gave
11 pairs, such as `com.dell.xg6-nvme-ssd.firmware` and
`com.lenovo.xg6-nvme-ssd.firmware`, so the vendor stays in the name.

When the releases a device is offered come from more than one
component, the operator takes the component of the newest release, and
posts a `ManyComponents` `Warning` that names the others. LVFS usually serves one
component for one device, and this has not been observed.

The guides change with it: `firmware:find-updates` explains the two
kinds of object and the move toward a component, and the reference
gains `spec.update.component`. The tests add a device whose GUID set
differs between two machines and lands in one component object, a
device that installs the newest release and keeps its entry, and a
device that gains its first release and leaves its GUID-set object,
which is then deleted, a release of two components, and a short name
that a person's upload rule holds.

## The second drill

On 2026-10-20, liken-1 and stick-1 ran `liken` 2026.10.20-001 and
firmware-operator `2026.10.20-001-dev-002-0d16bf0a`. Both machines
were scanned again after a reboot into the release, on the build
before the revision, so the boot time started each scan as
[When a machine is scanned](#when-a-machine-is-scanned) says.

The revision then grouped the three updates by component:
`microsoft-dbx-x64` holds stick-1's `dbx`, `microsoft-dbx-x64-kek2023`
holds liken-1's, and `microsoft-db-uefi-3p` holds stick-1's UEFI CA.
The two `dbx` objects are two components on LVFS, because the
`dbx` update for a machine that enrolled Microsoft's 2023 KEK is
signed with that key. So the two machines need two approvals, and the
split is correct. The devices with no release got objects named by
the device, such as `uefi-ca` and `windows-production-pca`, which
counts both machines. Two names were already held, so the operator
used the longer names: liken-1's 4 ESRT devices are all named
`UEFI Device Firmware` with other GUIDs, and the two machines' `KEK CA`
devices have other GUIDs.

After the drill, the GUID sets' names changed. Of liken-1's 4 ESRT
devices, which fwupd all names `UEFI Device Firmware`, the first one
the operator met got the plain name and the other three a hash, so
the plain name depended on the order of creation. A GUID set's object
now always takes the device's name and the hash, and only a
component's object takes the short name.

The objects that the first build had created kept their names, because
the operator finds an object by its group and not by its name. The
drill deleted them, and the next scans created them under the short
names. A status write from outside the operator wakes no pass, as
[`watch.go`](../../watch.go) intends, so the drill restarted the operator
to start the scans.

## Choices made in the build

The build decided these, where the plan said nothing:

- The scan image's name comes from the tag of the operator's own pod
  image, and `--scan-image` overrides it.
- A person changed a discovered object when its `metadata.generation`
  is greater than 1.
- The CRD refuses `selector` and `machines` with `update`.
- Deleting a `FirmwareCatalog` leaves its entries in the discovered
  objects. They leave when the `Machine` is deleted, or when a
  `FirmwareCatalog` of the same name scans the machine again.
- The newest release is the first in fwupd's list.
- The operator's memory limit is 256Mi. A scan pod requests 10m of CPU
  and 64Mi of memory, with no limit.
- The first `kubectl apply -k` of the whole base refuses the `lvfs`
  `FirmwareCatalog` until its CRD is established. The install guide
  applies `lvfs.yaml` last, and a Flux `Kustomization` waits for the
  CRDs.

## Conditions and `Event`s

A `FirmwareCatalog` has a `Ready` condition, and each transition posts an
`Event`. A scan posts `ScanStarted` and `Scanned` or `ScanFailed` on
the `FirmwareCatalog`. A discovered `Firmware` posts `UpdateAvailable` when a
machine's newest offered release changes, and `Discovered` when the
operator creates it. `ManyComponents` is a `Warning` on a discovered
`Firmware`, when a catalog offers one device releases of more than one
component.

## The guides and skills

The guides are the documentation, and each one is a skill. They change
in the commit that builds this plan:

- `firmware:find-updates`, a new guide: what a scan reads, the `lvfs`
  `FirmwareCatalog`, reading `kubectl get firmware` and a machine's entry, and
  where each release came from.
- `firmware:install` adds the scan image, the `FirmwareCatalog` object, and the
  egress the scan pods need.
- `firmware:troubleshoot` adds a `FirmwareCatalog` that is not `Ready`, a scan
  that failed, `CatalogMoved`, and a machine that is never scanned.
- The reference gains a `FirmwareCatalog` page and the `update` fields of a
  `Firmware`, from the CRDs' descriptions.
- The concepts gain a page on discovery: why the scan runs fwupd's
  default plugins, why it never stages anything, and why a `Firmware`
  is a device model.

The README of `plans/` and the front page say that the operator also
runs a privileged scan pod with no claim.

## Tests

The tests run on the Go toolchain alone, against
`kubernetes/apiservertest`, with a fake LVFS behind an in-memory
`http.RoundTripper`:

- A new jcat file updates the `FirmwareCatalog`'s status, a SHA-256 that
  differs sets `Ready` to `False`, and an unchanged `ETag` changes
  nothing.
- A `Machine` with no record gets a scan pod with the catalog file and
  SHA-256 in its spec, and a result in the pod's log becomes the
  record and the discovered objects.
- A new boot time and a new device name start a scan, and a new device
  number does not.
- A new catalog that changes no release for a machine's GUIDs records
  the file and starts no scan. One that changes a release scans only
  the machines with that GUID.
- `CatalogMoved` reads the jcat at once.
- A failed scan does not run again for the same boot, devices, and
  file.
- An operator restart during a scan creates no second pod.
- A discovered object loses its last entry and is deleted, and an
  object with a changed spec is not.

The `scan` program has tests of its own with recorded `fwupdtool`
output from the spike.

The drill runs on liken-1: install the release, and see each machine
scanned once, liken-1's 4 UEFI devices as discovered `Firmware`
objects with no release, and stick-1 recorded with no devices. A drill
on a machine with an LVFS update needs a cluster with such a machine.

## Open questions

- **Devices beyond UEFI.** The spike traced only UEFI devices. NVMe
  drives, docks, and Thunderbolt controllers appear with the default
  plugins, and their GUID sets can change with a firmware version,
  which would change the model.
- **The `dbx` releases.** They change the Secure Boot revocation list,
  which is the boot chain. Discovery reports them like any release,
  and plan 05 has to apply them through `init`.
- **Machines with no internet route.** A scan pod downloads the
  catalog itself. A cluster with no route needs a mirror `FirmwareCatalog`.
