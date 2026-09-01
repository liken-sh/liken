# A missing firmware file reaches only the kernel log

Open problem. When a driver requests a firmware file the image does
not carry, the kernel prints one line to its log and the driver
carries on degraded. Nothing folds that line into the `Machine`
status, so `kubectl get machine` shows a healthy machine while a
device runs without the blob it asked for. The console parity
principle says what the machine prints must also reach the status,
and this class of print does not.

## The evidence

On 2026-09-01 a hard reboot forced a USB power reset on a test
machine's Intel 7265, which dropped the controller to its 2015 ROM.
The kernel asked for `intel/ibt-hw-37.8.10-fw-22.50.19.14.f.bseq`,
logged `Direct firmware load ... failed with error -2`, and brought
the radio up unpatched. Every Bluetooth link then flapped. The
`Machine` reported `Ready` with every condition true, and the gap
was found only by reading `/dev/kmsg` from a privileged pod. The
release that shipped no `ibt` files had been in production for
weeks, because a patched controller keeps its patch across warm
reboots and the image was never asked for the file until that day.

## The shape of an answer

The gap doctrine fits exactly: status reports the files requested
and not found, never the census of files loaded. A machine whose
every request succeeded reports nothing. The list is re-derived from
the current boot's kernel log, so it empties on the first boot of a
release that carries the file, and it needs no reset.

The machine-operator already reads `/dev/kmsg` with a cursor for the
wifi work, so the failed-load lines are one pattern away from a
status field, `status.hardware.missingFirmware` or a list beside
`unclaimed`. One entry would carry the file name and the requesting
device or driver.

## To design before the build

- Which failures count. `-2` is the missing file; other errors mean
  other things, and a request the driver retries and satisfies must
  not linger.
- The boot window. Most requests fire at module probe, before the
  operator runs, so the reader must cover the log from the boot's
  start and not only from its own start.
- Whether the fix belongs beside `status.hardware.unclaimed`, which
  answers the sibling question: hardware nothing drives. This field
  answers: hardware driven without the blob it wanted.
- Whether the hardware report at install time should also print the
  list, so a new machine's gaps show before it joins a cluster.
