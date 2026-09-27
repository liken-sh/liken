# A refused probe reads a dark panel every minute

The operator probes a panel when it first sees the monitor on a
connector. The probe asks for the capability string, then for the core
controls. A panel that answers nothing is recorded as not responsive,
and `askAgain` in `panels.go` probes it again once per
`probeRetryInterval`, which is the 60 s `backstopInterval`. The probe
runs again because a person can turn DDC/CI on at the panel's own
menu, and that raises no uevent and changes no EDID. Plan 08 added
this window after its rollout.

The window has no power guard. The poll in `displaycontrol.go` never
reads a panel whose last power value reads standby or off, but a panel
that refused the probe has no power value, so nothing holds the probe
back. A panel reaches this state when it was dark at first sight: the
operator started while the panel slept, or the monitor arrived on its
connector while asleep. A panel that answered once is not probed again
while the same monitor stays on the connector, so a panel that answered
and then went to sleep is not affected.

A second read has the same gap. The poll reads a panel only while
its last power value reads on, and `lit` counts a panel with no power
value as lit. A panel that answers DDC/CI and carries no power
control has no power value, so the poll reads its core controls every
10 s while it is dark. The operator has no DDC way to tell that such a
panel is dark.

## The cost

A probe of a silent panel sends the capability request three times
and the first core control's request three times, with the reply wait
doubling from 40 ms on each attempt. That is six requests and under a
second of bus time per panel, once a minute, for as long as the panel
stays dark. The poll of a panel with no power control is one request
per core control it carries, every 10 s, for as long as it answers.
Nothing measured the power or the bus cost on a real panel.

## The risk

A DDC read is a wake stimulus on some panels. That is why the poll
has its guard. A panel that sleeps and refuses DDC while it sleeps
gets six requests a minute, and a dark panel with no power control
gets the poll six times a minute. If one of those requests wakes it, the
panel lights with no workload asking for it, and a screen the media
layer darkened comes back. A panel that does not wake may still leave
its deepest standby to answer on the bus. Neither effect was observed
on the lab panels, and neither was tested for.

## Options

1. Keep the window, and accept the risk until a panel shows it.
2. Probe again only on an event: a uevent on the connector, a change
   of EDID, a new generation of the `Display`'s spec, or a claim's
   prepare. A person who turns DDC/CI on at the menu then also has to
   touch one of these, for example by editing the `Display`.
3. Probe again only when another source says the panel is lit. For an
   HDMI television, equipment-operator's CEC adapter reads the power
   status with no DDC traffic. The kernel's connector state describes
   the source and not the panel, so it does not answer this.
4. Grow the window after each refusal, from 60 s toward an hour. The
   read stops being a steady stimulus, and a panel whose DDC/CI a
   person turned on is found later.
5. Put a field on the `Display` that asks for a probe, so the person
   who turned DDC/CI on asks for the read once.

For the poll of a panel with no power control, options 1 and 3 apply
as they stand. A further option is to stop the poll for a panel with
no power value while the kernel reports its connector's DPMS state as
off, which covers a panel the compositor put to sleep and not one a
person turned off at its button.

Options 2 and 5 together remove every probe with no cause. Option 4 is
the smallest change.
