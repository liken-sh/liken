# Sharing the screen

An output publishes as one device that a single `ResourceClaim` holds.
`weston` already draws many clients on one output, so a second client
that wants to draw between films cannot get in. This plan publishes a
draw device that many claims share, and it counts the claims that ask
for panel power, so the panel sleeps only when the last one lets go.

The client that needs this is the media-operator's idle screen,
[plan 09](https://github.com/liken-sh/media-operator/blob/main/plans/09-the-idle-screen.md).

## The problem

The output `SliceDevice` sets no `AllowMultipleAllocations` (`slices.go`),
so the scheduler hands one output to one claim at a time. But `weston`
serves many clients on one output already. The kiosk shell makes every
client fullscreen and routes it by app-id (`weston.go`), and the
compositor keeps the screen while claims come and go: "the compositor
keeps the screen, and the next claim receives the same socket"
(`dra.go:388-389`).

The socket arrives only through a claim, and the output takes one claim.
So a second Wayland client that wants to draw while no film holds the
output has no way to reach the compositor. The media-operator's idle
screen is that client. It draws the clock and the room between films,
and it must draw at the same time a film's claim may hold the output.

## The design

### A draw device that many claims share

Publish a second device for each connector, the draw device, and mark
it `AllowMultipleAllocations` so many claims hold it at once. The draw
device delivers the compositor socket and the app-id, the same container
edits the output device already delivers (`cdi.go:133-142`), and it
carries an optional panel-power request. It sets no mode. The output
device stays single-allocation and keeps the mode.

So the right to draw is shared, because a Wayland socket is shared, and
the right to set the mode stays exclusive, because one panel runs one
mode. Panel power is shared too, but counted: a request on the draw
device or on the output device adds to the count on the connector. This
mirrors the shape the operator already uses for the control device. The
control device is a separate exclusive device for the one-writer i2c
wire. The draw device is the separate shared device for the many-writer
Wayland socket.

### Panel power by a count of holders

Make the panel's power a reference count across holders. Today power is
actuated per claim on prepare and reverted on unprepare, and the record
is one slot per connector: `releasePower` puts a connector to standby
when any claim that named it ends (`controls.go:586`, `controls.go:616`).

Change this so the panel is on while any prepared claim asks for power on
the connector, and returns to standby only when the last such claim
ends. This is the count the idle screen needs. It needs no
`coordination.k8s.io` `Lease`: the set of prepared claims the operator
already holds is the count, and it changes on prepare and unprepare,
which are events, not a timer.

A power change is a DDC write that does not restart `weston`. The
delivery sets the panel controls before it sets the mode, and only the
mode restarts the compositor (`dra.go:320` sets the controls,
`dra.go:328` sets the mode). So the count changes with no screen blink.

The mode is untouched. Each `Play` still selects its own resolution
through its own output claim, as plans 05 and 06 describe.

## What was considered and set aside

**Make the output device itself `AllowMultipleAllocations`, with no
separate draw device.** Set aside because the mode and power records are
one slot per connector (`modes.go:401`, `controls.go:586`). Two claims
on one output would each set a mode and contend on the one record, and
the first to unprepare would revert the mode (`modes.go:502`) and put the
panel to standby (`controls.go:616`) for the connector the other still
holds. The draw device carries no mode, and its power is a count, not a
setting, so many claims share it with nothing to contend over.

**A `coordination.k8s.io` `Lease` per screen, powered down when no
`Lease` holds it.** Set aside because a `Lease` renews on a timer, which
is a polling shape, and the operator already holds the exact count it
needs in its prepared claims. Count the allocations on an event, not a
`Lease` on a timer.

## How the work is proved

This plan is not built yet. The drill runs on `liken-1`. Allocate the
draw device to a standing pod and the output device to a `Play` at the
same time. Both draw on the one output, the `Play` on top. End the
`Play`, and the standing pod's surface stays, and the panel stays on
while the standing pod's power request stands. Drop the standing pod's
power request, and the panel returns to standby.

Measured on the metal: the panel power state after each step, read back
from the panel over DDC. Read in the code: the branch that delivers the
draw device with no mode, and the count that holds the connector on.

The related open problem "A control claim waits for the compositor"
stays open. The draw device needs the compositor to serve its socket, so
it is a different case from a control-only claim that reaches the panel
over DDC with no compositor.
