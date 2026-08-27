# The compositor heals the canvas

When a monitor's output flaps, a fullscreen client on another
screen can end up with a canvas sized for the wrong output, and
Weston never corrects it. This plan makes the operator restart the
compositor when the hazard fires and the screens are free, so the
canvas heals with nobody's hand on a pod.

## The problem

Seen on the metal on 2026-08-27: the lab's ultrawide switched
inputs, its output was destroyed and re-created, and the portable
panel then showed a canvas laid out for 3840x1600, cropped to
1920x1080. The kernel still drove the correct mode on every
connector; the wrong size lived in the client's surface.

The mechanism is upstream and unfixed. Weston's kiosk-shell handles
a destroyed output by orphaning the shell surface, core reflow
parks the orphan on a surviving output, and no path sends the
client a corrected configure when the output returns: the app-id
assignment is one-shot and is never re-run. The behavior is
unchanged from the 14.0.2 this operator ships through the current
16.0.0, no `weston.ini` key prevents it, and the newest upstream
hotplug commit only guards the crash in this window and leaves the
reassignment un-done. The open problem
[kiosk-shell loses a surface's output](open-problems/kiosk-shell-loses-a-surfaces-output.md)
carries the references.

The client is not the stuck party: `mpv` obeys every configure the
compositor sends. A fresh compositor places every surface at its
output's true size, and since the media layer's window watchdog
(its release 2026.08.27-003), every standing idle client rides a
compositor restart on its own: it exits when its window dies and
the kubelet brings it back. What was missing is the restart itself,
fired at the right moment.

## The design

The operator already tracks each connector's output across passes.
Two additions:

- **Detect the hazard.** A pass that finds a connector's output
  re-created, the connector present now and absent or changed on
  the pass before, marks the card's compositor as owing a restart.
- **Heal when the screens are free.** While any output claim is
  prepared on the card, the restart waits: a restart would kill a
  running film, and the film's own unprepare already leads to a
  mode prepare that rebuilds the compositor state for the next
  claim. When no output claim is prepared, the operator restarts
  the compositor once, after the output set has been stable for a
  settling window, because restarting inside the flap can hit the
  deferred-destroy crash upstream documents in Weston 14.

The restart reuses the path a mode change already takes: the
kubelet supervises the compositor container and brings it back, the
operator taints the outputs while the socket is down, and the draw
clients reconnect. The `Display` resources are untouched: panel
controls live on the DDC wire, which a compositor restart never
touches.

## What was considered and set aside

**The kernel connector force, `video=<connector>:e`.** It prevents
the output's destruction entirely by making the connector report
connected forever. Set aside because it is boot-level configuration
outside this operator, and because a connector that cannot report a
disconnect breaks the `disconnected` taint and the `Connected`
condition for that screen.

**A client-side nudge.** The client can detect its own wrong size
and cycle fullscreen, which re-runs kiosk-shell's output matching.
Held in reserve in the media layer rather than built, because the
operator can heal the canvas for every client at once, and a
client-side fix would repeat per client.

**Carrying a kiosk-shell patch.** The true fix is upstream, in
kiosk-shell re-matching surfaces when an output returns. This
project carries no patches to its dependencies; the open problem
records the contribution.

## How the work is proved

On `liken-1`, with only idle clients attached:

- Switching the ultrawide's input away and back re-creates its
  output; within the settling window the operator restarts the
  compositor, the idle clients exit and return through their
  watchdog, and the portable panel's canvas is correct with no
  hand on a pod.
- The same flap during a running `Play` defers the heal: the film
  keeps its screen, and the restart happens after the run ends.
