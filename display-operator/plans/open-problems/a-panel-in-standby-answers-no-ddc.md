# A panel in standby answers no DDC/CI

Many panels answer no DDC/CI while they are in standby. The operator
meets that silence in four places, and each one gives a costly or a
wrong answer:

- The probe of a panel that answered nothing runs again every 60 s,
  for as long as the panel stays dark.
- The poll reads a dark panel that has no power value every 10 to
  20 s.
- The poll reads a panel that went to standby after it answered, and
  `status.observed` keeps reporting its power on.
- A restart of the operator forgets the controls of a panel that
  sleeps through the restart.

All four ask one question: what a failed DDC read means for
`status.observed` and for the controls the operator publishes. This
document asks it once.

## The probe runs again every minute

The operator probes a panel when it first sees the monitor on a
connector. The probe asks for the capability string, then for the core
controls. A panel that answers nothing is recorded as not responsive,
and `askAgain` in `panels.go` probes it again once per
`probeRetryInterval`, which is the 60 s `backstopInterval`. The probe
runs again because a person can turn DDC/CI on at the panel's own
menu, and that raises no uevent and changes no EDID. Plan 08 added
this window after its rollout.

The window is a timer that reads state again to find a change. The
repository's `AGENTS.md`, in "Keep state current with events", calls
such a timer a defect.

The window has no power guard. The poll in `displaycontrol.go` never
reads a panel whose last power value reads standby or off, but a panel
that refused the probe has no power value, so nothing holds the probe
back. A panel reaches this state when it was dark at first sight: the
operator started while the panel slept, or the monitor arrived on its
connector while asleep. A panel that answered once is not probed again
while the same monitor stays on the connector. The poll still reads it,
as the next section describes.

## The poll reads a dark panel

The poll reads a panel only while its last power value reads on, and
`lit` counts a panel with no power value as lit. A panel that answers
DDC/CI and carries no power control has no power value, so the poll
reads its core controls every 10 to 20 s while it is dark. The
operator has no DDC way to tell that such a panel is dark.

A panel that answered the probe with its power on, and then went to
standby at its own button or its own timer, keeps the power value on.
A read that fails leaves the last value standing, so `lit` reads on,
and the poll reads every carried control every 10 to 20 s for as long
as the panel sleeps. The same stale value stays in `status.observed`
and on `display_panel_power`, which both report on for a panel in
standby.

## A restart forgets the panel's controls

The operator holds what a panel carries in memory only: the probe's
answer in `panelControls.probed`, keyed by connector, with the
monitor it probed. A new operator process starts with nothing and
probes each panel again. A panel in standby answers no DDC/CI, so the
probe records a refusal, and the pass publishes what the refusal
holds:

- `status.capabilities` goes empty, because `statusOf` writes
  `facts.Capabilities` over the published list.
- The control device (`<connector>-control`) leaves the
  `ResourceSlice`, and the output device loses its
  `controlsBrightness` and `controlsPower` attributes. A claim that
  asks for either cannot be allocated until the panel wakes and a
  retry of the probe finds it.
- `Responsive` goes False with the reason `NoDDCReply`.
- `status.observed` keeps the values of the last process, because
  `statusOf` writes it only when the probe observed something. For a
  panel in standby, it reports the power on.

The last process had the list, and the panel still carries the
controls. Only the restart removed them from the resource.

## What a home cluster showed

### The reads of a sleeping panel

One panel on a home cluster carries seven core controls, power among
them. In standby it answers each DDC/CI read with bytes that are not a
DDC/CI reply, and the first byte differs from read to read. Its log
over one week shows:

- The poll read it for five days in a row while it slept: seven reads
  every 10 to 20 s, about 5,600 polls and 39,000 failed reads a day.
  Each poll printed its seven failures, because the log kept one
  message per error text and no two texts matched. The log now prints
  one message until a poll reads the panel cleanly or a different set
  of controls fails.
- The reads did not wake it. The failures ran without a break until
  a person turned the panel on.
- Each time the panel came on, the card raised two `drm change`
  uevents about 300 ms apart, and the sound card reported an ELD
  change. The panel going to standby raised no uevent: the failed
  reads just started.
- A restart of the operator while the panel slept found it silent, so
  the new process recorded a refusal. That is the case the probe
  section describes. The panel came on at 13:11:53, and the uevents
  did not start a probe, because the refusal's 60 s window had not
  passed. The window came due on a timed pass, and the probe found the
  panel at 13:12:16, 23 s after the uevents.

### The controls that a restart removed

One panel on a home cluster goes to standby when nobody uses it. It
slept through about 15 restarts of the operator in two days. The first
of them removed the control device, and it stayed absent for about 45
hours, until a person turned the panel on and the refusal's 60 s retry
probed it. Three hours later the panel went to standby again, and the
next rollout removed the device again. No claim on that cluster asks
for the panel's controls, so nothing failed. A claim that asks for
brightness or power would have waited for those 45 hours.

## The cost

A probe of a silent panel sends the capability request three times
and the first core control's request three times, with the reply wait
doubling from 40 ms on each attempt. That is six requests and under a
second of bus time per panel, once a minute, for as long as the panel
stays dark. The poll of a panel with no power control is one request
per core control it carries, every 10 to 20 s, for as long as it
answers. Nothing measured the power or the bus cost on a real panel.

## The risk

A DDC read is a wake stimulus on some panels. That is why the poll
has its guard. A panel that sleeps and refuses DDC while it sleeps
gets six requests a minute, and a dark panel with no power control
gets the poll six times a minute. If one of those requests wakes it,
the panel lights with no workload asking for it, and a screen the
media layer darkened comes back. A panel that does not wake may still
leave its deepest standby to answer on the bus. Neither effect was
observed on the lab panels, and neither was tested for. The panel on
the home cluster did not wake from a week of reads, which is one
panel.

## Options for when to read a silent panel again

1. Keep the window, and accept the risk until a panel shows it.
2. Probe again only on an event: a uevent on the connector, a change
   of EDID, a new generation of the `Display`'s spec, or a claim's
   prepare. A person who turns DDC/CI on at the menu then also has to
   touch one of these, for example by editing the `Display`.
3. Probe again only when another source says the panel is lit. For an
   HDMI television, CEC reads the power status with no DDC traffic.
   equipment-operator has that source: its `Television` kind
   ([plan 09](../../../equipment-operator/plans/completed/09-cec.md))
   reports the TV's power in `status.power` and lists the `Display`
   objects whose pictures reach it in `status.displays`. display-operator
   would watch the `Television` objects and probe a listed `Display`'s
   panel when the power changes to on. This covers only a TV on a
   `CECBus` in `Control`. The kernel's connector state describes the
   source and not the panel, so it does not answer this.
4. Grow the window after each refusal, from 60 s toward an hour. The
   read stops being a steady stimulus, and a panel whose DDC/CI a
   person turned on is found later. The window is still a timer.
5. Put a field on the `Display` that asks for a probe, so the person
   who turned DDC/CI on asks for the read once.

For the poll of a panel with no power control, options 1 and 3 apply
as they stand. A further option is to stop the poll for a panel with
no power value while the kernel reports its connector's DPMS state as
off, which covers a panel the compositor put to sleep and not one a
person turned off at its button.

For a panel that stopped answering after it answered, the options are
the same two ideas. The poll can stop after a poll in which no carried
control answered, and start again on a uevent, on a claim's prepare, or
on a new generation of the `Display`'s spec. Or it can drop to the
refusal's 60 s window and grow from there. The home cluster's panel
raises a uevent when it comes on, which is what the first idea needs.
A panel that raises none would stay unread until one of the other
events.

Options 2 and 5 together remove every probe with no cause. Option 4 is
the smallest change.

## Options for what the resource reports for a silent panel

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
   same stale value, so the same rule would apply to it.

Option 2 of this list makes a claim on the control device work during
standby only if a DDC write reaches a sleeping panel. The restore path
already writes to a panel that answers nothing and counts on the write
waking it (`wakesAfter` in the tests models that panel). Whether the
home cluster's panel takes a power-on in standby is not known. A panel
whose DDC/CI a person turned off in its menu since the last process
also keeps its list under option 2, and a claim against it fails at
the write, with the panel's silence in the error.

## What is not known

- Whether panels in standby take a DDC/CI power-on. Two lab panels and
  one home panel is too few to say.
- Whether a seed from the published status is safe when two monitors
  of one model share a `Display`
  ([two-monitors-of-one-model-share-a-display.md](two-monitors-of-one-model-share-a-display.md)).
