# 87. Firmware facts a machine publishes

Milestone 87. Proposed 2026-10-19. The USB bus and device numbers
were built on 2026-10-19, for `firmware-operator`'s uploads. The
report of missing kernel firmware is not built, and its open
questions below are still open. The OS publishes two
kinds of fact that firmware work needs, and does nothing more about
device firmware. Each USB device in a machine's `ResourceSlice` gains
its bus number and device number, so `firmware-operator` can find the
device's node and tell one appearance of a device from the next. And
`Machine` status reports the firmware files that the kernel requested
and did not find, which was the open problem `missing-firmware-status`.

## The boundary

The word "firmware" names three different jobs. Root
[plan 81](../../plans/completed/81-firmware.md) records the owner of each:

| | The kernel loads it | The host uploads it | The device keeps it |
|---|---|---|---|
| Example | i915 and wifi blobs | a QHY guide camera on a Cypress FX2 chip | UEFI, a dock, an SSD |
| Owner | the OS image, [milestone 32](completed/32-hardware-support-in-the-image.md) | `firmware-operator`, its plan 01 | `firmware-operator`, its plan 02, with the OS parts in [milestone 33](completed/33-firmware-updates.md) |

`firmware-operator` is an add-on. It ships apart from the OS, like
display-operator, and a cluster that does not install it loses
nothing that the OS does. The OS keeps only what an add-on cannot do
for itself: it publishes the facts about each device, it reports the
kernel's own firmware, and, for milestones 33 and 88, it gives an
add-on a turn in which to write the firmware itself. Vendor firmware, vendor tools, and the `Firmware`
resource stay out of `liken`.

## USB bus and device numbers

machine-operator publishes each USB device with its `address`, the
port path, which stays the same while the device stays in its port.
Two more facts change each time the device enumerates:

- `busNumber`, the number of the USB bus, from `busnum` in sysfs.
- `deviceNumber`, the number the kernel gives the device on that bus,
  from `devnum` in sysfs.

`firmware-operator` needs both, for two reasons:

- The usbfs node of a device is `/dev/bus/usb/<bus>/<device>`. A
  loader such as `fxload` takes that path, and the operator writes it
  into the loader pod's spec.
- A device that resets and enumerates again gets a new device number
  on the same address. That is how the operator tells a new appearance
  from one it already handled, after its own restart, with no record
  in memory.

The device number of a device changes only when the device enumerates
again, which already changes the slice. So the new attributes add no
writes to the slice. They are published for every USB device, because
the facts belong to the device, not to the operator that reads them.

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

`status.hardware.missingFirmware` reports the requests that did not
resolve, not every file that loaded:

```yaml
status:
  hardware:
    missingFirmware:
      - file: intel/ibt-hw-37.8.10-fw-22.50.19.14.f.bseq
        driver: btusb
```

An entry names the file and, where the log gives it, the driver. The
list comes from the current boot, so a boot into an image that has the
file clears it with no action from a person. A request that a later
retry or a fallback name satisfies is not reported. The list is beside
`unclaimed`, and like `unclaimed` it sets no condition: `unclaimed`
lists hardware with no driver, and `missingFirmware` lists hardware
whose driver is missing a file.

The kernel log reader today is the `liken-logs` relay:
[`kmsg.go`](../logs/kmsg.go) reads `/dev/kmsg`, and
[`cursor.go`](../logs/cursor.go) stores its position. Its kernel
container writes records to stdout and has no API token, so a new log
pattern is not enough. The failures need a path to machine-operator,
or machine-operator reads `/dev/kmsg` itself. The open questions below
list what that choice must settle.

## The guides and skills

The guides are the documentation, and `make -C docs skills` writes
each guide as an Agent Skill. A guide and its skill change in the same
commit:

- `hardware-modules` covers `status.hardware.missingFirmware`: a file
  that the image does not ship is a report against the release, and no
  setting fixes it. It also says that a bare USB chip that no module
  drives can need firmware from the host, and links to
  `firmware-operator`'s `firmware:upload-to-a-device`.
- `devices` and the devices reference page list `busNumber` and
  `deviceNumber`, and say that both change each time the device
  enumerates, so a claim must not select on them.
- `troubleshoot` adds a device that misbehaves while
  `missingFirmware` lists its file.
- The `Machine` reference page gains `status.hardware.missingFirmware`
  from the CRD's descriptions.

## Tests

The tests run on the Go toolchain alone:

- A USB device in the sysfs fixtures publishes `busNumber` and
  `deviceNumber`, and a device that enumerates again publishes the new
  device number under the same name.
- Kernel log fixtures cover a missing file, a retry that succeeds, a
  fallback name, an error other than `-2`, and a log that starts after
  the request.

The drill reboots a machine into an image without one firmware file,
confirms that `missingFirmware` lists it, then reboots into a correct
image and confirms that the list clears. The drill needs a cold power
cycle, because a warm reboot did not show the controller's request.

## Open questions

- **Which kernel errors count as missing.** `-2` means the file is
  absent. Other errors need their own reading, and a driver can retry
  or use another file.
- **How early the kernel log reader starts.** Requests often happen
  while a module probes, before machine-operator runs. The ring buffer
  can wrap, so a late reader must not take an incomplete log for a log
  with no failures.
- **Which process reads the kernel log**: the `liken-logs` relay with a
  new path to machine-operator, or machine-operator itself.
- **Whether the install-time hardware report prints
  `missingFirmware`**, before the machine joins a cluster.

## A possible direction: supplying the file

This milestone only reports a missing file, and the fix is a release
whose image ships it. A later design could let `firmware-operator`
supply the file instead, as a second target of its `upload` action.
The operator would read `missingFirmware`, copy the file from a vendor
image into a directory that the kernel searches through
`firmware_class.path`, and make the driver probe again. The OS would
have to give each node a writable directory for that path, because the
image is read-only. A driver that requests its firmware only once, at
boot or when the device opens, may need a reboot before it reads the
file. Nothing here is decided, and this milestone does not depend on it.
