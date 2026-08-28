## The resting layer

A declared field is a standing instruction. On every pass the
operator compares the declaration with the value it last saw, and it
writes the panel only where the two diverge, so a settled panel
costs nothing on the wire. A declared value is validated against
`status.capabilities`: a value the panel does not carry fails the
pass and is never written. An empty `spec` writes nothing at all.
The operator invents no value, ever: a panel with no declarations
keeps whatever its own menu holds.

## The override

`spec.override` holds a temporary state above the resting layer, the
way `kubectl cordon` holds `spec.unschedulable` above a `Node`'s
definition. A writer adds the block, and the operator obeys it. The
writer deletes the block, and the operator restores the panel: to
the resting declaration where `spec` states one, otherwise to the
value it captured.

The capture is the load-bearing step. Before the operator obeys
`backlight: off`, it reads the panel's brightness and writes the
value to `status.captured`, and only a committed capture is followed
by the write that darkens the panel. A capture in `etcd` survives an
operator restart, a pod move, and a reboot, so the restore does too.
The restore retries until the panel reads back the value, because a
panel that is waking answers late.

An override has no timeout. If the writer that set one crashes, the
panel stays dark until the writer returns or a person deletes the
block. That failure is visible: `kubectl get display` shows the
standing override, and the block's field manager names the writer
that owes the lift.

## The resting mode

`spec.mode` follows the resting pattern with one difference in
weight: a mode lands through the compositor, and applying it
restarts the compositor once, which ends every Wayland client on
the card. So the operator applies a resting mode only while no
claim holds the screen. A claim's own `mode` parameter wins for the
claim's lifetime, a `spec.mode` edit during a claim waits for the
claim to end, and the unprepare that frees the screen restores the
declaration promptly.

## The two values of the mode

`status.mode` reports the mode twice, because two parties each
report one and they can disagree. `kernel` is the mode the graphics
card is synced to on the connector. `weston` is the mode the
compositor lays canvases out at, read from the compositor's own
`wl_output` events over a standing connection the operator holds.
A client draws at the second one. When the two values differ, the
clients on that screen are drawn at the wrong size, and the
operator restarts the compositor to correct it once the screens are
free. `weston` is absent while the operator holds no connection to
a compositor, and `kernel` is absent while the connector drives
nothing. `kubectl get displays` shows the two as the `MODE` and
`CANVAS` columns.

## Shared screens

A monitor with several inputs dims all of them at once, because
brightness and power are panel-global, and the operator writes what
an override states whenever the panel answers. Panels do not say
reliably which input they show: the query is optional, and a panel
can answer it with the name of the port the question arrived on. So
whether a screen should ever go dark is its owner's declaration,
not the operator's guess. State it in the layer that writes the
override; the media operator's `Player` carries an idle policy
whose `offAfterSeconds: 0` keeps a shared screen's panel untouched.

## Observed values

`status.observed` is what the operator last read or wrote. The
operator touches the wire when it probes, when it captures before
an override, when it actuates, and about every ten seconds for a
panel that is lit and under no override. That last read is what
finds a change a person made at the panel's own menu, and it is
what makes
a resting declaration hold: the pass that finds the divergence
writes the declaration back. A panel in standby or off, a panel an
override holds, and a panel that answers nothing are never read on
a timer, because a DDC/CI read is itself a wake stimulus on some
panels, and a polling loop would relight the screens the override
layer darkened. For those panels, `observed` stays what the
operator last saw.

## One writer per wire

The operator is the one process that writes a panel's i2c wire for
the `Display`: resting declarations, overrides, and restores all
land through the same reconciler. The
[one-writer rule](/docs/reference/devices/#the-control-device) in
the devices reference still governs the other paths: a pod that
holds a connector's control device owns that wire while it runs, so
do not declare resting values or write overrides for a screen whose
control device a pod holds.
