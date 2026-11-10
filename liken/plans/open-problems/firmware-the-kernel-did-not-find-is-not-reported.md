# Firmware the kernel did not find is not reported

A driver can keep running after a firmware file fails to load. The
kernel reports the failure in its log, and `Machine` status does not.
So a machine can report `Ready` while a device works badly because the
release does not ship a file that its driver asked for. The proposal
below, `status.hardware.missingFirmware`, has four open questions, and
nothing of it is built.

[Milestone 87](../completed/87-firmware-facts-a-machine-publishes.md)
held this report beside the USB bus and device numbers that
machine-operator now publishes for `firmware-operator`. The numbers are
built, and that milestone is closed. Its "The boundary" section gives
the owner of each kind of firmware: the OS image ships the firmware
that the kernel loads, so a missing file is a report against the
release.

## The evidence

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

## The proposal

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
[`kmsg.go`](../../logs/kmsg.go) reads `/dev/kmsg`, and
[`cursor.go`](../../logs/cursor.go) stores its position. Its kernel
container writes records to stdout and has no API token
(`automountServiceAccountToken: false` in `logs/manifests/logs.yaml`),
so a new log pattern is not enough. The failures need a path to
machine-operator, or machine-operator reads `/dev/kmsg` itself. The
open questions below list what that choice must settle.

## The guides and skills

The guides are the documentation, and `make -C docs skills` writes
each guide as an Agent Skill. A guide and its skill change in the same
commit:

- `hardware-modules` covers `status.hardware.missingFirmware`: a file
  that the image does not ship is a report against the release, and no
  setting fixes it.
- `troubleshoot` adds a device that misbehaves while
  `missingFirmware` lists its file.
- The `Machine` reference page gains `status.hardware.missingFirmware`
  from the CRD's descriptions.

## Tests

The tests run on the Go toolchain alone. Kernel log fixtures cover a
missing file, a retry that succeeds, a fallback name, an error other
than `-2`, and a log that starts after the request.

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

This proposal only reports a missing file, and the fix is a release
whose image ships it. A later design could let `firmware-operator`
supply the file instead, as a second target of its `upload` action.
The operator would read `missingFirmware`, copy the file from a vendor
image into a directory that the kernel searches through
`firmware_class.path`, and make the driver probe again. The OS would
have to give each node a writable directory for that path, because the
image is read-only. A driver that requests its firmware only once, at
boot or when the device opens, may need a reboot before it reads the
file. Nothing here is decided, and the report does not depend on it.
