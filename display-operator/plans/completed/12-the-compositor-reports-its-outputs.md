# 12. The compositor reports its outputs

## The problem

Two parts of this operator guessed at facts the compositor owns.

The canvas heal of [plan 10](10-the-compositor-heals-the-canvas.md)
inferred "an output was re-created" by comparing which monitor each
connector carried against the pass before. That comparison is blind
to the flap a studio produces nightly: the same monitor sleeps and
wakes, the kernel mode on its connector changes, and weston destroys
and re-creates the output under a monitor whose identity never
moved. On 2026-08-27 an ultrawide woke from sleep on the lab
machine, weston re-created its output, a second panel's client kept
a 3840x1600 canvas on a 1920x1080 screen, and the heal never fired.

The mode readback after a switch polled the kernel's connector for
the requested mode. The kernel syncing a mode and weston serving
canvases at that mode are two different facts, a client draws at the
second one, and every canvas defect this operator has met lived in
the gap between them.

## The design

The operator holds one standing, listen-only Wayland connection to
the compositor it launched, and reads three facts from it.

The registry's `global` and `global_remove` events are the heal's
signal. A `wl_output` that leaves and a `wl_output` that arrives on
one live connection, in either order, is an output that was
re-created, and it owes the restart plan 10 pays. The outputs a
fresh connection finds are a baseline and owe nothing, because a
compositor that just started lays every canvas out right. The
connection dies with every compositor restart, the operator's own
included, so a restart cannot report itself as a re-creation; that
property is the whole of the self-echo guard, and the design adds no
other.

The `wl_output` `mode` events are the readback. A mode switch waits
for the compositor that started after its restart, told from the
one it ended by a session counter, to report the requested mode. A
failure names the mode the compositor serves instead. The old poll
of the kernel's connector is gone, and so is the separate probe of
the socket, because a compositor with a standing connection is a
compositor a consumer can connect to.

The same events fill `status.mode`, which replaces
`status.currentMode` with the mode's two values side by side:
`kernel` is what the card is synced to, `weston` is what the
compositor serves canvases at, and a gap between them is the canvas
defect, visible from `kubectl get displays` as the `MODE` and
`CANVAS` columns. `weston` is absent while no connection stands,
because an absent value is honest and a carried-over one is a guess.

The client writes the wire protocol itself, in `wayland.go`. It
reads four interfaces, and a Wayland library is a dependency the
image does not otherwise carry. Weston 14.0.2 advertises `wl_output`
version 4, whose `name` event carries the connector's own name; a
compositor that advertises less is tracked with no names, which
still heals and answers no mode readback.

## Considered and set aside

Widening the identity comparison to include the kernel mode would
have caught the sleep-wake flap, but every mode the operator applies
itself changes the kernel mode too, so that design needs a list of
self-caused changes to exempt, and the list is a second guess
stacked on the first. The compositor's registry needs no exemptions.

`wl_output.release` is not sent. Bound output proxies accumulate on
the compositor only within one connection, and every restart clears
them.

## What was measured and what was read

The failing flap was measured on the lab machine on 2026-08-27, on
an LG ultrawide waking from sleep. Weston's `wl_output` version, its
bind order for output events, and the wire format were read from the
weston 14.0.2 and wayland 1.26 sources, and the tests in
`wayland_test.go` drive a fake compositor that serves that wire
format, re-creations, restarts, and slow mode batches included.

The build was drilled on the lab machine the same night, in release
2026.08.27-008. A resting `1280x720@60` landed in 5 seconds with
both of `status.mode`'s values agreeing, so the switch's readback
ran through the compositor's events on the metal. A forced
disconnect and return of the ultrawide's connector, written through
the connector's own sysfs `status` file, ran the heal end to end:
the disconnect taints stood while the connector was off, the release
printed the deferral line while the outputs settled, and the heal
restarted the compositor 5 seconds later with every canvas laid out
correctly. The compositor survived this flap without a crash, which
is the exact case plan 10 was built for and could never fire on.

The drill also caught a defect the bench had not: a restore whose
compositor restart crossed the kubelet's backoff printed a readback
failure naming the dead compositor's mode, because the watch kept
its answers between connections. The maps now empty the moment a
connection ends, `status.mode.weston` goes absent while no
compositor answers, and the failure line reports `no mode`, which is
the truth.
