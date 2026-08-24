# The idle screen

Plan 09. A standing pod, one per `Player`, draws status while no `Play`
runs, and yields the screen the moment a `Play`'s `mpv` draws over it.
It builds on the display of
[plan 07](completed/07-the-player-draws-its-own-display.md). The
hardware seam it needs, a screen that more than one client draws, is
the display-operator's
[plan 07](https://github.com/liken-sh/display-operator/blob/main/plans/07-sharing-the-screen.md).

## The problem

The `liken` display lives inside the playback pod. It is an `mpv`
script, and it draws only while a `Play` runs. Between plays there is no
pod and no client, so the screen shows the compositor's bare background.
A `Player` with no `Play` is dark.

A person who walks up to an idle `Player` sees nothing. No clock, no
room name, no now-playing from another room, and no sign the `Player`
is powered and ready. The gap is between two states with nothing in the
middle: a `Play` runs and the screen is the film, or the `Play` ends and
the screen is nothing.

## The design

A standing pod, one per `Player`, runs whether or not a `Play` runs. It
draws the idle surface while no `Play` runs: the clock, the household
zone, the now-playing from another room, and the `Player`'s own name. It
reuses the display stack of plan 07, the `mpv` script and the `libass`
overlay, fed an idle layout with no video. The clock, the zone, and the
art bridge already exist there.

### The handoff is a stack, not a switch

The idle pod draws as a second Wayland client on the same standing
`weston` the display claim delivers. `weston` runs the kiosk shell,
which makes every client fullscreen on one output and routes it by
app-id. Two clients that set one output's app-id both go fullscreen, and
the shell stacks them. So the handoff is free:

* The idle client draws while no `Play` runs.
* A `Play`'s `mpv` starts with the same app-id and draws on top.
* The `Play` ends, and the shell reveals the idle client again.

There is no black flash and no restart. `weston` runs with
`idle-time=0` and never blanks the head on its own, and the idle client
never disconnects. The idle surface is also the fallback when a `Play`
pod crashes: the film's surface unmaps, and the idle surface below it
shows until the operator restarts the film.

### The idle pod draws, it does not own the mode

The idle pod holds a shared draw device, not the exclusive output
device. The display-operator publishes a draw device per connector that
delivers the compositor socket, the app-id, and an optional panel-power
request, but no mode, and marks it shareable so many clients hold it at
once. Each `Play` keeps its own output claim, which still owns the mode,
so a `Play` sets its own resolution as before. The
display-operator's
[plan 07](https://github.com/liken-sh/display-operator/blob/main/plans/07-sharing-the-screen.md)
builds this seam.

### The power policy lives on the `Player`

A `Player` drives exactly one screen. `PlayerSpec.Display` is a single
device, and the design states one pod drives one screen through one GPU
(`api.go:90-101`). So the idle and power behavior belongs on the
`Player`, not on a new resource. The `Player` gains a small idle power
policy: how long to hold the panel on after the last activity, and
whether to sleep after that. The actual panel power reports in `Player`
status.

The panel follows demand. It is on while any holder asks for it on. A
`Play` asks for power while it plays. The idle pod asks for power while
its active window is open, then drops the request so the panel can
sleep. A power change is a DDC write on the panel and does not restart
`weston`: the delivery sets the panel controls before it sets the mode,
and only the mode restarts the compositor (`dra.go:320` sets the
controls, `dra.go:328` sets the mode). So the request goes on and off
with no screen blink. The display-operator actuates the panel and counts
the holders.

How the idle pod raises and drops that request is its own design
question, and this plan defers it. The open problem
[How the idle screen asks for power](open-problems/how-the-idle-screen-asks-for-power.md)
holds it. Until it is answered, the idle screen draws with the panel on,
and the sleep behavior is a later slice.

### The wake signal rides the bus

A remote press crosses the message bus and resets the idle pod's sleep
timer with no round trip to the API server. The desired policy and the
actual power stay on the Kubernetes API. The bus carries only the fast
event. Hardware state stays on the API, and MQTT speeds the
notification between the components. It does not hold the state.

## What was considered and set aside

**Make the output device shareable.** Let the idle claim and each `Play`
claim hold the same output, each setting its own mode and power. Set
aside because the output's mode and power records are one slot per
connector (`modes.go:401`, `controls.go:586`), and a `Play` ending
reverts the mode and puts the panel to standby for the connector the
idle client still holds (`modes.go:502`, `controls.go:616`). The panel
would go dark on the exact event the idle screen exists to survive, a
`Play` ending. It would also change the public device contract for every
consumer of the display-operator.

**Give the `Player` one shared display claim.** Reserve one claim for
both the idle pod and each `Play` pod, and drop the display request from
the `Play`. Set aside because per-`Play` resolution becomes
unrepresentable. The only trigger that sets a mode is a claim prepare,
and a standing claim prepares once, so a `Play` would have no point at
which to set its own resolution, and the display-operator's plans 05 and
06 would stop working. The panel would also never return to standby,
because the standing claim never unprepares.

**A separate `Screen` resource.** Hold the idle and power state on its
own resource. Set aside because a `Player` drives exactly one screen
(`api.go:90-101`), so a `Screen` would stand one-to-one with the
`Player` and add a resource with no independent life. The line to watch
is `api.go:90`: the day one pod no longer drives one screen is the day a
`Screen` resource earns its place. Two cases would cross it, a screen
driven by more than one `Player`, or screen state that must be named
from outside the media domain.

The chosen design, the shared draw device beside the exclusive output
device, is the honest fit. Drawing is shared because a Wayland socket is
shared. The mode and the power stay single-owner because one panel has
one mode and one power state.

## How the work is proved

This plan is not built yet. The drill runs on `liken-1`. With a `Player`
and no `Play`, the screen shows the idle surface, the clock and the
zone. Start a `Play`, and the film covers the idle surface with no black
flash. End the `Play`, and the idle surface returns with no restart.
Leave the `Player` idle past its hold time, and the panel sleeps. Press
the remote, and the press crosses the bus, the panel wakes, and the idle
surface returns.

The build can run in slices:

* The standing idle pod and its shared draw device.
* The idle surface content: the clock, the zone, the `Player` name, and
  the now-playing from another room.
* The power policy on the `Player`, the sleep timer, and the wake on bus
  activity. This slice waits on the open problem
  [How the idle screen asks for power](open-problems/how-the-idle-screen-asks-for-power.md).
