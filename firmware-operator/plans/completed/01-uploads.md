# 01, Uploads

Proposed on 2026-10-19. Built on 2026-10-19 and released in
2026.10.19-004. The first drill passed on liken-1 on 2026-10-20, under
[Tests](#tests). The replug during a reservation and the operator
restart during an upload have not run on hardware. It needs `liken`'s
[milestone 87](../../../liken/plans/87-firmware-facts-a-machine-publishes.md),
which publishes the USB bus and device numbers of each device, and
whose USB part was built on 2026-10-19. This
plan adds the cluster-scoped `Firmware` resource and its `upload`
action: when a device that a rule matches appears on any machine,
`firmware-operator` writes the device's firmware into it from the
vendor's image. [Plan 02](../02-updates.md) adds the `update` action to
the same resource.

## The problem

Some USB devices hold no firmware of their own. They power up as a
bare microcontroller, and the host must write the device's program
into it before any driver can open it. The chip then resets and
enumerates again as the real device, with a new product ID. No kernel
driver does this for these devices, so nothing in the OS image can. On
a Linux distribution, the vendor's package installs a udev rule and
the firmware files, and the rule runs `fxload` when the bare chip
appears. A `liken` machine runs no udev, so the bare chip stays bare.

This is common in astronomy and in lab equipment. INDI's
`85-qhyccd.rules` holds 117 rules for QHY cameras: 18 for FX2 chips,
32 for FX2LP, and 67 for FX3. SBIG and Meade's Deep Sky Imager cameras
load the same way, and so do Xilinx's platform cables and sigrok's
`fx2lafw` logic analyzers.

## What the lab measured

On 2026-10-19 a `Job` written by hand loaded a QHY5L-II-C on the
liken-1 test cluster. observatory-operator's
[plan 23](../../../observatory-operator/plans/completed/23-firmware-before-the-driver.md)
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

`Firmware` is cluster-scoped, in `firmware.liken.sh/v1alpha1`. It is
cluster-scoped because it describes a device model, the same way a
`DeviceClass` does, and a device can move from one machine to another.
A rule on one `Machine` would not follow a portable camera to the next
machine.

```yaml
apiVersion: firmware.liken.sh/v1alpha1
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

- `spec.selector` is a CEL expression over the devices in the
  machines' `ResourceSlice`s, with the same variables as a
  `DeviceClass` selector. The operator compiles it with the CEL
  environment of Kubernetes' `dynamic-resource-allocation` module, so a
  selector means what it means in a `DeviceClass`.
- `spec.machines` is an optional label selector on `Machine`s. With no
  value, the rule applies on every machine.
- `spec.upload` is required in this plan's version of the CRD. Plan 02
  adds `spec.update`, and with it a CEL rule that refuses an object
  with both or neither. Making `upload` optional then is a change that
  every existing object still passes.
- `spec.upload` names an image and the command to run in it. The
  operator implements no loader. The loaders differ by chip and by
  vendor: `fxload -t fx2`, `-t fx3`, a second-stage loader for older
  QHY cameras (`-s`), and vendor tools. The vendor's image already
  holds the right one.

The firmware bytes stay in the vendor's image and never enter the OS
image. `liken`'s milestone 32 ships the firmware that the kernel's own
modules name in `MODULE_FIRMWARE`, so the kernel build decides that
set. Bytes for a device with no kernel driver do not belong in it, and
vendor firmware often has its own license. The image that holds a
vendor's library already has that license question, and the notices
for that image answer it.

## How the operator uploads

The operator watches the `ResourceSlice`s of every machine and the
`Firmware` objects through client-go's reflector. A bare chip that
appears is a change to its machine's slice, so the operator reads it
from the watch, with no timer.

The operator watches only the slices whose driver is `liken.sh`,
the ones machine-operator publishes. When a device matches a
`Firmware` with `upload`, and no upload for that appearance has run,
the operator creates a `Pod` and a `ResourceClaim` in its own
namespace, `liken-system`:

- A node selector on `kubernetes.io/hostname` puts the pod on the
  slice's node.
- The pod names the claim by name, and the claim's owner reference
  names the pod, so the claim is deleted with its pod. The operator
  creates the pod first, because the claim needs the pod's UID. A pod
  whose claim does not exist yet stays unscheduled, and the next pass
  creates a claim that is missing.
- The claim asks for one device of the `DeviceClass`
  `firmware-upload`, which the manifests ship and which selects the
  USB devices of `liken.sh`. Its selector names the device's `address`
  and `deviceNumber`. The address is the port path, so it is unique on
  the machine even when two bare chips of one model are connected. The
  device number limits the claim to this appearance, so a claim that
  was not allocated before a replug cannot take the next appearance.
- `restartPolicy: Never` and an `activeDeadlineSeconds` of five
  minutes. The pod runs once. `activeDeadlineSeconds` counts from the
  pod's start, so the operator also measures five minutes from the
  pod's creation, for a pod that never schedules.
- The owner reference of the pod names the `Firmware`, so a deleted
  rule deletes its pods.
- Labels and annotations record the rule, its generation, the
  machine, the address, and the device number. The operator reads
  them after its own restart, and the pod's name is a hash of them, so
  a second create for one appearance answers `409`.

The command needs the path of its device node. The slice gives the
device's USB bus number and device number, and the usbfs node is
`/dev/bus/usb/<bus>/<device>`, each number in three digits. The
operator sets the environment variable `LIKEN_DEVICE` in the pod's
spec to that path, and the kubelet expands `$(LIKEN_DEVICE)` in the
command. machine-operator's CDI spec delivers each node at its host
path, so the path is also correct in the container.

An upload succeeds when the pod exits 0 and the bare device leaves the
machine's slice. The exit code alone is not enough, because a loader
can exit 0 after it writes firmware that the chip does not run. The
device that comes back is not part of the check, because the rule does
not know its IDs. In the lab the chip left 14 seconds after `fxload`
reset it, so the operator waits one minute after the container ends.
A device still in the slice after that minute is a failed upload. A
pod that exits with another code is a failed upload, and the message
gives the exit code and the last line of the pod's log. The container
sets `terminationMessagePolicy: FallbackToLogsOnError`, so the kubelet
copies the end of the log into the container's terminated state, and
the operator reads the line from the pod with no permission on
`pods/log`.

A port runs one upload at a time. While a rule's pod for a port
exists, a new generation of the rule or a second rule waits for that
pod's result, and the new rule takes effect on the next appearance.

The operator writes the result to status first and then deletes the
pod, so an operator that stops between the two reads the same pod
again and writes the same result.

Each appearance of a device gets one attempt. A failure goes to status
and to an `Event`, and nothing retries it until the device appears
again or the rule changes. A loop that writes to a device again and
again can disturb it, so no timer retries an upload. A power cycle, a
replug, or a new generation of the rule starts a new attempt.

The operator holds no record of attempts in memory. The device number
changes each time a device enumerates, so the machine, the address,
and the device number together identify one appearance. The status
entry records them, and the operator reads it after a restart to
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

The pod writes nothing back. The operator decides the result from the
slice and the pod's exit code, and it is the only writer of the
result.

### Two rules on one device

Two `Firmware` objects can match one device. CEL validates one object
at a time, so the API server cannot refuse the second rule. The
operator runs neither rule on that device and reports the conflict in
the status of both objects. An `upload` and an `update` on one device
are a conflict too: `upload` matches a chip that holds no firmware,
and `update` matches a device that does.

## Status

The operator writes all of a `Firmware` object's status, so each
object has one writer:

```yaml
status:
  conditions:
    - type: Valid           # the selector compiles
      status: "True"
  devices:
    - machine: liken-1
      device: usb-1-4-1-2
      address: 1-4.1.2
      deviceNumber: 12
      generation: 1
      result: Loaded        # Running, Loaded, Failed, Conflict
      message: ""
      time: "2026-10-19T22:01:09Z"
  counts: { running: 0, loaded: 1, failed: 0, conflict: 0 }
```

`status.devices` has one entry for each port, a machine and an
address, where the rule matched a device. The entry describes the
latest appearance, so a camera that is power-cycled every night keeps
one entry, and the list grows only with the number of ports. Each
entry also records the rule's `generation` that ran. An entry that is
`Loaded` stays after the bare device leaves, so the entry shows which
rule loaded the device that replaced it. `status.counts` gives the
number of entries in each result, and the printer columns show those
counts, so `kubectl get firmware` shows the fleet at a glance.

The operator posts an `Event` on the `Firmware` for each upload it
starts and for each result, as root plan 78 requires for an action
that changes no condition. A device that did not load sets no
condition on its `Machine`. The machine's own work does not depend on
the device, and the `Machine`'s status belongs to the OS.

## Plan 83 and a device that changes its identity

`liken`'s
[milestone 83](../../../liken/plans/83-a-usb-device-is-named-for-its-unit.md)
names a USB device by its vendor, product, and serial in place of its
port. An upload changes the product, and on some chips the vendor too,
so the loaded device gets a different name from the bare chip. That is
correct: the loaded device is a different device, and the device's
own claim selects it by its own IDs.

The two plans work together after a power cycle during a session. The
camera falls back to the bare chip, and its name leaves the slice. The
operator uploads again, and the camera comes back with the same
vendor, product, and serial, so it takes the same name. The claim that
the camera's pod holds is then valid again, and the CDI rewrite that
milestone 83 relies on for a replug delivers the new node. This is a
prediction, not a measurement.

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

The guides are the manual, and the docs build writes each guide as an
Agent Skill under the prefix `firmware`. A guide and its skill change
in the same commit:

- `firmware:install`: install the operator, confirm that it holds its
  `Lease`, and remove it.
- `firmware:upload-to-a-device`: how to tell that a device needs
  firmware from the host, how to write a `Firmware` with `upload`, how
  to read `status.devices` and the `Event`s, and what a `Conflict`
  means.
- `firmware:troubleshoot`: a `Failed` upload, a `Conflict`, an upload
  that never starts because the selector matches nothing, and a
  claimed device that stays `Pending` because its upload has not run.
- The `Firmware` reference page, generated from the CRD's
  descriptions.

`liken`'s `hardware-modules` and `devices` guides link to
`firmware:upload-to-a-device`, as milestone 87 says.

## Decisions

- **Not in `init`, and not in the OS.** An upload is one short
  operation, and its bytes come from a container image. `init` cannot
  run an image, and vendor bytes cannot go in the OS image. With the
  orchestration here, the OS only publishes devices, and this operator
  ships apart from it.
- **Not in each operator.** Firmware state belongs to the device on
  its machine, not to a session. A camera that gets power at noon is
  ready at night with no reservation involved. Each operator that
  loaded its own devices would write the same code again, and each one
  would have to read `ResourceSlice`s to tell a loaded device from an
  absent one.
- **One `Deployment` for the cluster.** The work on a device is a pod
  on the device's node, so the operator itself needs no node. One
  process watches every slice and writes every status.
- **A command, not a loader in the operator.** The operator judges the
  result from the slice, so it does not need to understand the
  protocol.
- **A `Pod`, not a `Job`.** Each appearance gets one attempt, so a
  `Job`'s retry policy would be set to zero, and its controller would
  add a second object with nothing to do.

## Tests

The tests run on the Go toolchain alone, in a `synctest` bubble,
against `kubernetes/apiservertest`:

- A slice device that matches a rule creates one pod, with the node
  selector, the claim template, `LIKEN_DEVICE`, the security context,
  and the owner reference above.
- The bare device leaves the slice after the pod succeeds, and the
  entry turns `Loaded`. A pod that exits 0 while the device stays turns
  it `Failed`.
- A failed upload does not run again for the same appearance, and runs
  again when the device number changes.
- A restart of the operator during an upload creates no second pod.
- Two rules on one device create no pod and report `Conflict` on both.
- A selector that does not compile sets `Valid` to `False`.
- A CRD validation test refuses an object with both actions or
  neither.

The drills run on liken-1 with the QHY5L-II-C:

- Upload after a cold power cycle, with `fxload` as user 1000.
- Replug the camera, and power-cycle it during a reservation.
- Restart the operator during an upload.

The first drill passed on 2026-10-20, on `liken` 2026.10.19-005 and
`firmware-operator` 2026.10.19-004. The camera was unplugged and
plugged into another port of the same hub, which cut its power. It
appeared as the bare chip, `1618:0920`. The operator posted
`UploadStarted` at 13:18:25 UTC and `Loaded` at 13:18:29, 4 seconds
later. `fxload` ran as user 1000 under the `restricted` profile and
exited 0. The camera then published as `1618:0921`, "QHY-CCD QHY5-II",
with a new device number. The new port did not matter, because the
rule selects on the vendor and product. The other two drills have not run on
hardware. A partial run on `dev-cluster/` covered the restart with a
fake device: the restarted operator created no second pod.

## Open questions

- **Image volumes.** Kubernetes can mount an image's files as a
  volume, so a rule could name a small loader image and a separate
  image of firmware bytes. The first version runs the vendor's image
  directly. Whether k3s's containerd on `liken` supports image volumes
  was not checked.
