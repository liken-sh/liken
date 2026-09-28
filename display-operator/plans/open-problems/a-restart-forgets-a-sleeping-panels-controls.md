# A restart forgets a sleeping panel's controls

The operator holds what a panel carries in memory only: the probe's
answer in `panelControls.probed`, keyed by connector, with the
monitor it probed. A new
operator process starts with nothing and probes each panel again. A
panel in standby answers no DDC/CI, so the probe records a refusal,
and the pass publishes what the refusal holds:

- `status.capabilities` goes empty, because `statusOf` writes
  `facts.Capabilities` over the published list.
- The control device (`<connector>-control`) leaves the
  ResourceSlice, and the output device loses its `controlsBrightness`
  and `controlsPower` attributes. A claim that asks for either
  cannot be allocated until the panel wakes and a retry of the probe
  finds it.
- `Responsive` goes False with the reason `NoDDCReply`.
- `status.observed` keeps the values of the last process, because
  `statusOf` writes it only when the probe observed something. For a
  panel in standby, it reports the power on.

The last process had the list, and the panel still carries the
controls. Only the restart removed them from the resource.

## What a home cluster showed

One panel on a home cluster goes to standby when nobody uses it. It
slept through about 15 restarts of the operator in two days. The first
of them removed the control device, and it stayed absent for about 45
hours, until a person turned the panel on and the refusal's 60 s retry
probed it. Three hours later the panel went to standby again, and the
next rollout removed the device again. No claim on that cluster asks
for the panel's controls, so nothing failed. A claim that asks for
brightness or power would have waited for those 45 hours.

## Options

1. Keep the behavior. The resource reports what this process read,
   and `Responsive` explains why the list is empty.
2. Keep the last published list while the panel does not answer.
   `statusOf` leaves `status.capabilities` alone when the probe
   refused and the published `status.serial`, `status.manufacturer`,
   and `status.model` match the monitor on the connector. The slice
   keeps the control device from the same list. `Responsive` still
   reports False, so a reader sees that the list is the last one read.
3. Option 2 for the status only. The list stays readable, and the
   control device leaves the slice until the panel answers, so no
   claim is allocated against a panel that does not answer now.
4. Clear `status.observed` when the probe refuses, so the status does
   not report a power value that no read confirmed. This goes with
   any of the above. A poll that fails in a running process leaves the
   same stale value
   ([a-refused-probe-reads-a-dark-panel-every-minute.md](a-refused-probe-reads-a-dark-panel-every-minute.md)),
   and the two want one answer to what a failed read means for
   `status.observed`.

Option 2 makes a claim on the control device work during standby only
if a DDC write reaches a sleeping panel. The restore path already
writes to a panel that answers nothing and counts on the write waking
it (`wakesAfter` in the tests models that panel). Whether the home
cluster's panel takes a power-on in standby is not known. A panel
whose DDC/CI a person turned off in its menu since the last process
also keeps its list under option 2, and a claim against it fails at
the write, with the panel's silence in the error.

## What is not known

- Whether panels in standby take a DDC/CI power-on. Two lab panels and
  one home panel is too few to say.
- Whether a seed from the published status is safe when two monitors
  of one model share a `Display`
  ([two-monitors-of-one-model-share-a-display.md](two-monitors-of-one-model-share-a-display.md)).
