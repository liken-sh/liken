# 12, The idle screen reads the bus

Plan 09 built the idle screen: a standing pod per `Player` that draws
the clock, the `liken` mark, and the identity block while no `Play`
runs. That screen is still and it is frozen. The mark never moves. The
identity block comes from environment variables set at pod creation,
so a renamed part waits for a pod restart. And the screen says nothing
while a `Play` starts, though those are exactly the seconds a person
stares at it.

This plan makes the idle screen a live client of the bus. The operator
publishes one retained status per `Player`, the standing remote pod
publishes its controller's presence, and the idle pod's sidecar
forwards both into the display script over the IPC socket it already
holds. Nothing restarts to show a change.

## What the screen shows

The mark rests still. When a `Play` starts, the mark comes to life:
each hexagon pulses in size, independently, and the motion ramps up in
swing and speed. That motion is the loading indicator, and it runs
during the seconds the playback pod pulls and starts. A line appears
at the top right, under the clock: `Playing "Adventure Time"…`. When
the film's surface takes over, the idle screen stops every animation
timer, because nothing behind a film deserves GPU time. When the film
ends and the sidecar recreates the idle surface, the mark returns in
motion and eases back down to still. The motion itself is brand: the
brand repository's `motion.md` describes it, and `display/logo.lua`
implements it.

The identity block reads presence. A part with no live state, a wired
screen or its built-in speakers, draws at full brightness always. A
controller draws dim while it is disconnected, and pulses back to full
when it reconnects.

## The signal path

One writer per fact, and every fact retained, so a late subscriber
reads the current state the instant it connects:

* The standing remote pod publishes
  `liken/media/remotes/<ns>/<name>/presence`, retained, with
  `{"connected": true}` or `false`. The pod senses the controller
  first-hand: its evdev nodes open on connect and vanish on
  disconnect, so the signal originates where it is read, with no new
  Kubernetes watch. The pod also gains the availability pattern the
  playback sidecar already uses: an MQTT Last Will that marks the
  topic `offline` when the pod itself dies, so a dead translator does
  not read as a connected controller.
* The operator publishes
  `liken/media/players/<ns>/<name>/status`, retained, from the same
  pass that writes the `Player`'s Kubernetes status. The payload
  carries the display name, the activity in the vocabulary the
  Kubernetes status already uses (`Idle`, `Starting`, `Playing`), the
  current `Play`'s name and title, and the component list with each
  part's kind and presence. The operator subscribes to the presence
  and availability topics and folds them in, so the idle pod reads
  one topic and no more. A part whose pod is offline folds to
  disconnected, because an unread controller cannot be claimed
  connected.
* The idle pod's sidecar subscribes to the status topic beside the
  commands topic it already reads, and forwards each status into the
  display script as a `player-status` script message with the JSON as
  its one argument. On a re-present it recreates the surface as it
  does today, and then sends a `revealed` script message, so the
  display knows the exact frame it came back into view.

The title comes from the `Play`'s `Presentation`: `Series` first,
then `Title`, then the `Play`'s own name, resolved by the operator so
the display formats one string.

The Kubernetes API stays the source of truth for what exists and what
is desired. The bus carries the presentable now: the same doctrine
that put the keymaps and the focus mark there.

## The motion

One scalar, the energy, drives the whole animation. At rest the
energy is zero and the mark is still, so the only timer on a settled
idle screen is the clock's one-second tick. `Starting` eases the
energy up. `Playing` stops the timers outright. `revealed` sets the
energy high and eases it down to zero, so the mark seems to arrive in
motion and come to rest.

Each hexagon scales about its own center by up to ten percent at full
energy. Each takes its phase and its rate from its own index, so the
fourteen move independently and the whole reads as one organic thing,
and the same frame renders the same way on every run. The energy
scales the swing and the speed together, so the ramp-down both
shrinks and slows the motion. The frame timer runs at thirty frames
per second only while the energy moves or stands above zero.

## Seeds and the preview

The environment variables from plan 09 remain as the first paint: the
display seeds the identity block from them before the broker answers,
and the first retained status replaces them. So the screen is never
blank, the local preview keeps working with no broker, and a live
edit to a `Player` shows without a restart.

`local-idle` sets `IDLE_PREVIEW=1`, and under that variable alone the
display registers keys that fake the bus: one key plays each activity
edge, and one toggles a component's presence. So the ramps, the dim,
and the pulse can all be seen on a workstation before a release.

## Considered and set aside

* **A `ResourceSlice` watch for controller presence.** The
  disconnected state exists as a device taint the bluetooth operator
  writes. Watching it would make the media operator re-derive, one
  watch and one RBAC grant later, a fact its own remote pod senses
  the moment it happens.
* **The idle sidecar subscribing to each remote's presence
  directly.** The sidecar would need the remote list in its
  environment, which is the frozen-env problem again. The operator
  already holds the `Player` and already folds relational state, so
  it folds presence too, and the sidecar keeps exactly two
  subscriptions.
* **A standing animation at rest.** A mark that breathes forever
  spends GPU on a screen nobody is watching, on hardware a film
  shares. The mark rests still, and the motion means something is
  happening.
* **Animating under a running film.** The idle surface is hidden
  behind the `Play`'s surface, so any frame drawn there is wasted.
  `Playing` stops the timers.

## Proof

On `liken-1`, with the sailing demo:

* Create the `Play`. The mark ramps into motion and the
  `Playing "…"…` line appears before the playback pod is running.
* Exit the film. The idle surface returns with the mark in motion,
  and it eases to still.
* Power the DualSense off and on. Its line dims, then pulses back.
* Edit the `Player`'s `displayName`. The screen shows the new name
  with no pod restart.
