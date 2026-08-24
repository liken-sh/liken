# Sharing the screen

An output publishes as one device that a single `ResourceClaim` holds.
`weston` already draws many clients on one output, so a second client
that wants to draw between films cannot get in. This plan publishes a
draw device that many claims share, and it settles where panel power
lives for a screen a standing client holds: on the control device, in
the hands of the one pod that holds the wire.

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
edits the output device already delivers (`cdi.go:133-142`). It sets no
mode and carries no power.

So the right to draw is shared, because a Wayland socket is shared, and
the right to set the mode stays exclusive, because one panel runs one
mode. This mirrors the shape the operator already uses for the control
device. The control device is a separate exclusive device for the
one-writer i2c wire. The draw device is the separate shared device for
the many-writer Wayland socket.

### Panel power goes through the control device

The standing client that needs the panel dark and lit again holds the
connector's control device and writes DDC/CI itself. The control
device already exists for exactly this, a pod that drives the panel
while it runs, and a prepared control claim delivers the wire and the
operator performs no write for it. The media-operator's
[plan 17](https://github.com/liken-sh/media-operator/blob/main/plans/17-the-idle-screen-powers-the-panel.md)
builds that consumer and records why every shape that moved a power
request through the Kubernetes API was set aside: a claim is never
prepared without a consuming pod, a pod's claim list and a claim's
spec are immutable, the kubelet never redelivers a changed claim to a
driver, and `resourceclaims` has no node-scoped field selector to
watch.

This operator's part is the rule, stated in the device reference: on a
screen whose control device a pod holds, no claim states `power`. The
power record is one slot per connector, and `releasePower` puts a
connector to standby when any claim that named it ends
(`controls.go:586`, `controls.go:616`), so a `power` claim ending
would darken the panel under the standing holder. The rule keeps the
two writers off one wire, and the one-slot record stays correct for
the screens it was built for, a panel one exclusive claim runs.

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

**Panel power as a count of prepared claims.** The first draft of this
plan counted the claims that ask for power and put the panel to standby
when the last one ended. Set aside because the request could not move:
a standing pod's claim list is immutable, a claim's spec is immutable,
and the kubelet never redelivers a changed claim, so the count could
only change by scheduling and ending pods. The control device already
gave the standing holder the wire, so the count had nothing left to
carry.

**A `coordination.k8s.io` `Lease` per screen, powered down when no
`Lease` holds it.** Set aside because a `Lease` renews on a timer, which
is a polling shape. Power follows the events the holder already has,
the sleep window and the wake press, not a renewal clock.

## How the work is proved

The draw device is built and drilled. The media-operator's idle screen
runs on it on `liken-1`: a standing idle pod and a `Play`'s pod held
one screen at once, the film drew on top, and the idle surface returned
when the film ended, through the media releases of 2026-08-24. The
power half is proved by the media-operator's
[plan 17](https://github.com/liken-sh/media-operator/blob/main/plans/17-the-idle-screen-powers-the-panel.md)
drill, because the writes are the control-claim holder's: the backlight
reads 0 over DDC after the quiet window, and reads restored after a
press on the bus.

The related open problem "A control claim waits for the compositor"
stays open. The draw device needs the compositor to serve its socket, so
it is a different case from a control-only claim that reaches the panel
over DDC with no compositor.
