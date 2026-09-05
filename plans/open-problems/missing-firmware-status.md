# Report missing firmware in machine status

Open problem. A driver can continue running after a firmware file fails
to load. The kernel reports the failure, but `Machine` status does not,
so a device can malfunction while the machine reports `Ready`.

The project aims to make operational failures visible through the API
as well as the console. Missing firmware is a gap in that reporting.

## Evidence

On 2026-09-01, a hard reboot reset the USB power on a test machine's
Intel 7265 controller. It returned to its 2015 ROM firmware. The kernel
requested `intel/ibt-hw-37.8.10-fw-22.50.19.14.f.bseq`, logged
`Direct firmware load ... failed with error -2`, and started the radio
without the patch. Every Bluetooth link repeatedly disconnected.

The `Machine` reported `Ready` with every condition true. Reading
`/dev/kmsg` from a privileged pod exposed the missing file. The release
had omitted the `ibt` files for weeks without showing this failure:
the controller retained its patch across warm reboots and did not request
the file again until the power reset.

## Proposed reporting

Report unresolved firmware requests rather than listing every firmware
file loaded. An entry would name the requested file and, where the log
provides it, the requesting device or driver. Possible locations are
`status.hardware.missingFirmware` or a list beside `unclaimed`.

Derive the report from the current boot. A successful boot with the
needed firmware should remove the previous boot's report without a
manual reset. A request that later succeeds, or succeeds through a
supported fallback filename, should not remain an unresolved failure.

The existing kernel-log reader is the `liken-logs` relay, not the machine
operator. [kmsg.go](../../logs/kmsg.go) reads `/dev/kmsg`, and
[cursor.go](../../logs/cursor.go) stores its resume point. The kernel
container in [logs.yaml](../../logs/manifests/logs.yaml) emits facility-0
records to stdout and has no mounted API token. Collecting these failures
into status therefore needs a path to the status publisher or another
reader, not only a new log pattern.

## Remedy scope

**A small API and health-policy design, followed by implementation work.**
The collection mechanism is ordinary code. These reporting choices need
agreement before it is built:

- Which errors count. `-2` means a missing file; other errors need their
  own interpretation. Drivers can retry or use alternative firmware.
- How early collection starts. Requests often occur during module probe,
  before the operator runs. The kernel ring buffer can also wrap, so a
  late reader must not equate an incomplete log with no failures.
- Where the list belongs, which process collects it, and how status gets
  updated. `unclaimed` describes undriven hardware; this list describes
  firmware failures on hardware with a driver.
- Whether unresolved failures affect `Ready` or produce a separate
  diagnostic. Optional firmware and working fallback paths need different
  treatment from an unusable device.
- Whether the installation hardware report also prints the list, before
  the machine joins a cluster.

## Verification needed

Use kernel-log fixtures for an unresolved request, a successful retry,
a fallback filename, an error other than `-2`, and missing early records.
Verify status across reader restarts and a reboot into a corrected image.
A cold-power hardware test is necessary for the controller behavior that
warm reboots did not expose.
