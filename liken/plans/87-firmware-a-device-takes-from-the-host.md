# 87. Firmware a device takes from the host

Milestone 87. Proposed 2026-10-19. Not built. A cluster-scoped
`Firmware` resource declares what a device model needs from the host,
and machine-operator carries it out on every machine. This milestone
builds the resource and its `upload` action, which writes firmware into
a device each time the device appears. The `update` action, which
flashes a new version into a device that keeps it, is designed in
[milestone 33](33-firmware-updates.md) and uses the same resource.
This milestone also reports firmware that the kernel requested and did
not find, which was the open problem `missing-firmware-status`.

## Three kinds of firmware

The word "firmware" names three different jobs. Each one has its own
trigger and its own owner:

| | The kernel loads it | The host uploads it | The device keeps it |
|---|---|---|---|
| Example | i915 and wifi blobs | a QHY guide camera on a Cypress FX2 chip | UEFI, a dock, an SSD |
| Trigger | a driver probes the device | the device appears on the bus | a declared version differs from the device's version |
| Bytes from | the OS image, [milestone 32](completed/32-hardware-support-in-the-image.md) | the vendor's container image | an LVFS cabinet |
| Undone by | nothing is needed | a power cycle | nothing, so the checks before it are stricter |
| Owner | the kernel | `Firmware` with `upload`, this milestone | `Firmware` with `update`, milestone 33 |

The middle column has no owner in `liken` today. Some USB devices hold
no firmware of their own. They power up as a bare microcontroller, and
the host must write the device's program into it before any driver can
open it. The chip then resets and enumerates again as the real device,
with a new product ID. No kernel driver does this for these devices,
so nothing in the image can. On a Linux distribution, the vendor's
package installs a udev rule and the firmware files, and the rule runs
`fxload` when the bare chip appears. A `liken` machine runs no udev,
so the bare chip stays bare.

This is common in astronomy and in lab equipment. INDI's
`85-qhyccd.rules` holds 117 rules for QHY cameras: 18 for FX2 chips,
32 for FX2LP, and 67 for FX3. SBIG and Meade's Deep Sky Imager cameras
load the same way, and so do Xilinx's platform cables and sigrok's
`fx2lafw` logic analyzers.

## What the lab measured

On 2026-10-19 a `Job` written by hand loaded a QHY5L-II-C on the
liken-1 test cluster. observatory-operator's
[plan 23](../../observatory-operator/plans/23-firmware-before-the-driver.md)
records the details. The facts that shape this design:

- machine-operator publishes the bare chip, `1618:0920`, as a device,
  and a claim on it delivers its usbfs node to a pod.
- `fxload -t fx2` wrote 5545 bytes in 61 segments and reset the chip.
  The chip enumerated again 16 seconds later as `1618:0921`, on the
  same port.
- The camera after the load is a different USB device: a new product
  ID and a new device number. A claim on the bare chip does not follow
  it.
- The firmware stays until the camera loses power.

## The `Firmware` resource

`Firmware` is cluster-scoped, in `liken.sh/v1alpha1`, beside `Machine`
and `Cluster`. It is cluster-scoped because it describes a device
model, the same way a `DeviceClass` does, and a device can move from
one machine to another. A rule on one `Machine` would not follow a
portable camera to the next machine.

```yaml
apiVersion: liken.sh/v1alpha1
kind: Firmware
metadata:
  name: qhy5l-ii
spec:
  selector: |
    device.attributes["liken.sh"].vendor == "1618" &&
    device.attributes["liken.sh"].product == "0920"
  upload:
    image: ghcr.io/liken-sh/indi-qhy:<tag>
    command: [fxload, -t, fx2, -I, /lib/firmware/qhy/QHY5II.HEX, -D, $(LIKEN_DEVICE)]
```

- `spec.selector` is a CEL expression over the devices that
  machine-operator publishes, with the same variables as a
  `DeviceClass` selector. A person who wrote a `DeviceClass` can write
  this one.
- `spec.machines` is an optional label selector on `Machine`s. With no
  value, the rule applies on every machine.
- Exactly one of `spec.upload` and `spec.update` is set. A CEL rule in
  the CRD refuses an object with both or neither. An `update` with no
  `version` approves nothing. `firmware-operator` creates objects
  of that form as proposals, and a person adopts one by setting the
  version.
- `spec.upload` names an image and the command to run in it. `liken`
  does not implement a loader. The loaders differ by chip and by
  vendor: `fxload -t fx2`, `-t fx3`, a second-stage loader for older
  QHY cameras (`-s`), and vendor tools. The vendor's image already
  holds the right one.

The firmware bytes stay in the vendor's image and never enter the OS
image. Milestone 32 ships the firmware that the kernel's own modules
name in `MODULE_FIRMWARE`, so the kernel build decides that set. Bytes
for a device with no kernel driver do not belong in it, and vendor
firmware often has its own license. The image that holds a vendor's
library already has that license question, and the notices for that
image answer it.

## How machine-operator uploads

machine-operator already publishes each machine's devices from a walk
of sysfs, and a uevent starts that walk. So it learns that a bare chip
appeared from work it already does, with no new watch on the devices.
It watches `Firmware` objects through client-go's reflector, as every
operator does.

When a published device matches a `Firmware` with `upload`, and no
upload for that appearance has run, machine-operator creates a `Pod`
in `liken-system`:

- The pod runs on this machine only.
- Its `resourceClaimTemplate` selects the one device by its vendor, its
  product, and its `address` attribute. The address is the port path,
  so it is unique on the machine even when two bare chips of one model
  are connected. A claim from a template is deleted with its pod.
- `restartPolicy: Never` and an `activeDeadlineSeconds` of five
  minutes. The pod runs once.
- The owner reference names the `Firmware`, so a deleted rule deletes
  its pods.

The command needs the path of its device node. machine-operator reads
that path in the same sysfs walk that found the device, so it sets the
environment variable `LIKEN_DEVICE` in the pod's spec to the usbfs
node's path. The kubelet expands `$(LIKEN_DEVICE)` in the command from
the variables in the pod's spec. A variable in a CDI edit would not
work, because the runtime applies CDI edits after the kubelet expands
the command. The CDI spec delivers each node at its host path, so the
path from the walk is also the path in the container.

An upload succeeds when the pod exits 0 and the bare device leaves the
machine's slice. The exit code alone is not enough, because a loader
can exit 0 after it writes firmware that the chip does not run. The
device that comes back is not part of the check, because the rule does
not know its IDs.

Each appearance of a device gets one attempt. A failure goes to status
and to an `Event`, and nothing retries it until the device appears
again or the rule changes. A loop that writes to a device again and
again can disturb it, so no timer retries an upload. A power cycle, a
replug, or a new generation of the rule starts a new attempt.

machine-operator holds no record of attempts in memory. The usbfs
device number changes each time a device enumerates, so the pair of
address and device number identifies one appearance. The status entry
records that pair, and machine-operator reads it after a restart to
decide whether an upload for this appearance already ran. A pod that
already exists for the device means the attempt is in progress.

### Permissions

The upload needs no capabilities. An FX2 or FX3 load is a series of
USB control transfers on the device's usbfs node, and opening that
node is the only permission it needs. `liken` gives a claimed node to
the pod's user. So the pod runs with:

- `runAsNonRoot` as user 1000, `capabilities.drop: [ALL]`, and
  `allowPrivilegeEscalation: false`
- `readOnlyRootFilesystem` and seccomp `RuntimeDefault`
- `automountServiceAccountToken: false`, so the pod has no access to
  the API

That meets the Pod Security `restricted` profile. The firmware is in
the image, so the pod needs no network. A `NetworkPolicy` that denies
its egress is part of the manifests.

The pod writes nothing back. machine-operator decides the result from
the slice and the pod's exit code, and it is the only writer of the
result.

### Two rules on one device

Two `Firmware` objects can match one device. CEL validates one object
at a time, so the API server cannot refuse the second rule.
machine-operator runs neither rule on that device and reports the
conflict in the device's status entry. An `upload` and an `update` on
one device are a conflict too: `upload` matches a chip that holds no
firmware, and `update` matches a device that does.

## Status

Each machine reports its own firmware in `status.hardware.firmware` on
its `Machine`, because machine-operator on that machine is the only
process that has the facts. The field has one list for each kind of
firmware:

```yaml
status:
  hardware:
    firmware:
      missing:
        - file: intel/ibt-hw-37.8.10-fw-22.50.19.14.f.bseq
          driver: btusb
      uploads:
        - firmware: qhy5l-ii
          device: usb-1-4-1-2
          address: 1-4.1.2
          deviceNumber: 12
          result: Loaded        # Running, Loaded, Failed, Conflict
          message: ""
          time: "2026-10-19T22:01:09Z"
```

- `uploads` has one entry for each device that a rule matched in this
  boot. An entry that is `Loaded` stays after the bare device leaves,
  so the entry shows which rule loaded the device that replaced it.
- `missing` lists the firmware files that a kernel driver requested in
  this boot and did not find. [The section below](#firmware-the-kernel-did-not-find)
  covers it.
- Milestone 33 adds `devices`, the version that each updatable device
  reports.

machine-operator posts an `Event` on the `Machine` for each upload it
starts and for each result, as root plan 78 requires for an action
that changes no condition. An upload sets no condition. A device that
did not load is reported in the entry, and the machine stays `Ready`,
because the machine's own work does not depend on the device.

The `Firmware` object's status holds the rule's own facts: whether its
selector compiles, and a count of devices loaded and failed across the
fleet. `firmware-operator` writes it from the `Machine`s, so the
object has one writer. `firmware-operator` is optional: it is one
`Deployment` for the cluster, like cluster-operator, and its own
plans describe it. A cluster without it runs every upload, and its
`Firmware` objects have no status. The entries on each `Machine` are
the record.

## Firmware the kernel did not find

A driver can keep running after a firmware file fails to load. The
kernel reports the failure in its log, and `Machine` status does not.
On 2026-09-01 a hard reboot reset the USB power on a test machine's
Intel 7265 Bluetooth controller. The controller went back to its ROM
firmware from 2015. The kernel requested
`intel/ibt-hw-37.8.10-fw-22.50.19.14.f.bseq`, logged `Direct firmware
load ... failed with error -2`, and started the radio without the
patch. Every Bluetooth link disconnected again and again, and the
`Machine` reported `Ready` with every condition true. The release had
left out the `ibt` files for weeks. The failure stayed hidden because
the controller kept its patch across warm reboots and asked for the
file again only after the power reset.

`status.hardware.firmware.missing` reports the requests that did not
resolve, not every file that loaded. An entry names the file and,
where the log gives it, the driver. The list comes from the current
boot, so a boot into an image that has the file clears it with no
action from a person. A request that a later retry or a fallback name
satisfies is not reported.

The kernel log reader today is the `liken-logs` relay:
[`kmsg.go`](../logs/kmsg.go) reads `/dev/kmsg`, and
[`cursor.go`](../logs/cursor.go) stores its position. Its kernel
container writes records to stdout and has no API token, so a new log
pattern is not enough. The failures need a path to machine-operator,
or machine-operator reads `/dev/kmsg` itself. The open questions below
list what that choice must settle.

## Plan 83 and a device that changes its identity

[Milestone 83](83-a-usb-device-is-named-for-its-unit.md) names a USB
device by its vendor, product, and serial in place of its port. An
upload changes the product, and on some chips the vendor too, so the
loaded device gets a different name from the bare chip. That is
correct: the loaded device is a different device, and the device's
own claim selects it by its own IDs.

The two milestones work together after a power cycle during a
session. The camera falls back to the bare chip, and its name leaves
the slice. machine-operator uploads again, and the camera comes back
with the same vendor, product, and serial, so it takes the same name.
The claim that the camera's pod holds is then valid again, and the
CDI rewrite that milestone 83 relies on for a replug delivers the new
node. This is a prediction, not a measurement.

## What other components do

A component that runs a device of this kind ships `Firmware` objects
for it, next to the image that holds the bytes. observatory-operator
ships one object for each rule in INDI's `85-qhyccd.rules`, and the
`indi-qhy` image adds `fxload` and the firmware files. Plan 23 in that
component covers the generator and the image. No operator adds a step
to load firmware. A pod that claims the loaded device stays `Pending`
until the device appears, and that wait is already part of each
operator's start.

## The guides and skills

The guides are the documentation, and `make -C docs skills` writes
each guide as an Agent Skill. A change to what a person declares or
reads changes the guide in the same commit, and the skill with it:

- A new guide, `device-firmware`, and its skill
  `liken:device-firmware`: how to tell that a device needs firmware
  from the host, how to write a `Firmware` with `upload`, how to read
  `status.hardware.firmware.uploads` and the `Event`s, and what a
  `Conflict` means.
- `hardware-modules` says that a bare chip that no module drives can
  need an upload in place of a module, and links to `device-firmware`.
  It also covers `status.hardware.firmware.missing`: a file the image
  does not ship is a report against the release, and no setting fixes
  it.
- `devices` says that a claim on a device that appears only after an
  upload stays `Pending` until the upload finishes.
- `troubleshoot` adds a `Failed` or `Conflict` upload, and a missing
  firmware file.
- A new reference page for `Firmware`. `crdref` generates the
  reference pages from the CRDs of `Machine` and `Cluster`, so it reads
  a third schema. The `Machine` page gains `status.hardware.firmware`
  from the same CRD descriptions.

## Decisions

- **Not in `init`.** Plan 70 put `inputattach` in `init`, because a
  serio port exists only while a process holds the line, and that
  process must outlive every pod. An upload is one short operation,
  and its bytes come from a container image. `init` cannot run an
  image, and the bytes cannot go in the OS image. `init` stays as
  small as possible.
- **Not in each operator.** Firmware state belongs to the device on
  its machine, not to a session. A camera that gets power at noon is
  ready at night with no reservation involved. Each operator that
  loaded its own devices would write the same code again, and each one
  would have to read `ResourceSlice`s to tell a loaded device from an
  absent one.
- **A command, not a loader in `liken`.** `liken` judges the result
  from the slice, so it does not need to understand the protocol.
- **A `Pod`, not a `Job`.** Each appearance gets one attempt, so a
  `Job`'s retry policy would be set to zero, and its controller would
  add a second object with nothing to do.
- **One resource for two actions.** `upload` and `update` both apply
  to a device model across the fleet, selected by device identity.
  `Cluster` and `Machine` gain no spec.

## Tests

The tests run on the Go toolchain alone, in a `synctest` bubble, with
machine-operator against `kubernetes/apiservertest`:

- A published device that matches a rule creates one pod, with the
  claim template, the security context, and the owner reference above.
- The bare device leaves the slice after the pod succeeds, and the
  entry turns `Loaded`. A pod that exits 0 while the device stays turns
  it `Failed`.
- A failed upload does not run again for the same appearance, and runs
  again when the device number changes.
- A restart of machine-operator during an upload creates no second
  pod.
- Two rules on one device create no pod and report `Conflict`.
- A CRD validation test refuses an object with both actions or
  neither.
- Kernel log fixtures cover a missing file, a retry that succeeds, a
  fallback name, an error other than `-2`, and a log that starts after
  the request.

The drills run on liken-1 with the QHY5L-II-C:

- Upload after a cold power cycle, with `fxload` as user 1000. The
  proof `Job` ran as root, so this is not proven yet.
- Replug the camera, and power-cycle it during a reservation.
- Restart machine-operator during an upload.
- Reboot into an image without one firmware file, and confirm that
  `missing` lists it, then reboot into a correct image and confirm that
  the list clears.

## Open questions

- **Which kernel errors count as missing.** `-2` means the file is
  absent. Other errors need their own reading, and a driver can retry
  or use another file.
- **How early the kernel log reader starts.** Requests often happen
  while a module probes, before machine-operator runs. The ring buffer
  can wrap, so a late reader must not take an incomplete log for a log
  with no failures.
- **Which process reads the kernel log** for `missing`: the
  `liken-logs` relay with a new path to machine-operator, or
  machine-operator itself.
- **Whether the install-time hardware report prints `missing`**,
  before the machine joins a cluster.
- **Image volumes.** Kubernetes can mount an image's files as a
  volume, so a rule could name a small loader image and a separate
  image of firmware bytes. The first version runs the vendor's image
  directly. Whether k3s's containerd on `liken` supports image volumes
  was not checked.
