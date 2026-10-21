# 02, Updates

Proposed on 2026-10-19, as a stub. Replaced on 2026-10-20, before it
was built, by three plans that separate its parts:
[plan 03](../completed/03-discovery.md) finds the hardware and the updates for
it, [plan 04](../completed/04-approval.md) approves a version, and
[plan 05](../completed/05-rehearsal.md) applies an approved version in
turns. A spike on 2026-10-20 changed parts of the design below, and
plan 03 records what it measured. This plan adds the
`update` action to the `Firmware` resource of [plan 01](../completed/01-uploads.md):
a declared version of firmware that a device keeps across a power
cycle, such as a board's UEFI, a dock, or an SSD. fwupd does the
vendor-specific work. The operator also proposes updates from the LVFS
catalog, and a person adopts each proposal. The parts that change the
machine's boot chain stay in the OS, in `liken`'s
[milestone 33](../../../liken/plans/33-firmware-updates.md), which also
lists what the OS must build first.

## The declaration

A `Firmware` object with `spec.update` approves one version for one
device model across the fleet:

```yaml
apiVersion: firmware.liken.sh/v1alpha1
kind: Firmware
metadata:
  name: framework-13-bios
spec:
  selector: <the device's fwupd GUID>
  machines:
    matchLabels: { hardware: framework-13 }   # labels a person set
  update:
    version: "3.09"
    cabinet:
      url: https://fwupd.org/downloads/<hash>-bios.cab
      sha256: <digest>
```

- `spec.update.version` is the approval. An `update` with no `version`
  approves nothing. No other field approves an update, and `Cluster`
  and `Machine` gain no spec.
- `spec.update.cabinet` pins the file, the way an image is pinned by
  its digest. fwupd installs a local cabinet and checks the vendor's
  signature inside it, so the apply path needs no LVFS catalog. The
  operator checks the digest before fwupd reads the file.
- No machine fetches the LVFS catalog. fwupd's own refresh is a timer
  on each machine that reads remote state again to find a change. This
  operator is the one process that reads the catalog.
- An object stays after its update is done. Its status shows when
  every matched device reports the version, and the next release is a
  bump of the same object. A deleted object would leave a replaced
  board or a new machine on the old version, and deleting it does not
  undo a flash.

## The operator proposes, and a person adopts

The operator reads the LVFS catalog on a schedule, because LVFS has no
feed of changes to subscribe to, and it checks the catalog's signature
before it uses it. The comment at that timer gives this reason. It
never writes to git.

For each device model that a machine reports and no `Firmware` covers,
the operator creates a `Firmware` with the label
`firmware.liken.sh/discovered`, the device's selector, and an `update`
with no `version`. The object does nothing until a person sets the
version. The operator writes the newer releases it found into
`status.available`, each with its version, its cabinet URL, and the
cabinet's digest. This is the pattern of a `Display`, which
display-operator creates with an empty spec for a person to fill, and
of a `PairingRequest`, which reports what the radio sees and pairs
only the device that a person writes into its spec.

A person adopts the proposal by setting `spec.update.version` and the
cabinet from `status.available`. The object is then an approval, and
the steps below run. An option on the operator sets the version itself
for the devices a person chose, which is how a cluster updates
automatically.

The operator deletes only its own objects: an object with its label,
with no `version`, that no machine matches any more. It never deletes
an object with a version, because a person or the option approved
that version.

## The steps of an update

1. **Report.** The operator runs fwupd on each machine in a pod that
   only reads, when the machine boots and when a `Firmware` with
   `update` changes. The pod writes each device's ID and version as
   JSON to its log, and the operator writes them to the status of the
   `Firmware` that matches the device, or proposes one.
2. **Stage.** When a device's version differs from the approved
   version, the operator asks the OS for a rollout turn for that
   machine, through the interface that milestone 33 adds.
3. **Apply.** The rollout conductor grants the turn and drains the
   machine. The operator runs the fwupd pod that writes. A UEFI capsule
   goes through `init`'s efivars shim, and the reboot applies it. A
   device that fwupd flashes with no reboot, such as a dock or a USB
   receiver, is flashed only when no other claim holds the device, so
   a flash never runs while a workload uses the device.
4. **Prove.** The ESRT or the next report shows the approved version,
   and the machine joins its cluster again. A failed flash has no
   earlier state to return to, so it is recorded in status with an
   `Event`, and nothing runs it again on its own.

fwupd needs wide access to devices, even to read versions: NVMe admin
commands, hidraw, and sysfs. So both pods are privileged except for
the variable store, which the shim replaces. The pod that writes runs
only inside a rollout turn. The report pod changes nothing.

## The guides and skills

- `firmware:update-a-device`: read `status.available`, adopt a
  proposal, turn on automatic approval for chosen devices, follow the
  rollout turn, and read the version that the device reports after
  the update.
- `firmware:troubleshoot` adds an update that waits for its turn, a
  flash that waits because another claim holds the device, and a
  failed flash.
- `firmware:install` covers the option that approves updates on its
  own.
- The `Firmware` reference page gains the `update` fields from the
  CRD's descriptions.

`liken`'s `upgrade` guide says that a firmware update takes a rollout
turn the same way a release does, and links to
`firmware:update-a-device`.

## Open questions

- **The turn interface.** Milestone 33 has to give a component a way to
  request a rollout turn for a machine and to see it granted. Its
  shape is not designed.
- **How the report pod reaches fwupd's plugins** with the least
  privilege that still reads every device's version.
