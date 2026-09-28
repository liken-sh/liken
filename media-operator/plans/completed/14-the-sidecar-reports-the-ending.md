# 14, The sidecar reports the ending

Plan 12 made the end of a film the idle screen's cue: the operator
publishes the Player's `Idle` status and the re-present, and the mark
arrives at full swing and eases to rest. But the cue waits on the
slowest signal in the system. The operator learns that a `Play` ended
from the pod reaching a terminal state, and a pod takes seconds to
terminate. By the time the idle surface is revealed, the person who
pressed exit has been looking at a dead screen, and the arrival
animation plays to a viewer who already gave up on it.

This plan moves the ending onto the bus. The playback pod's command
sidecar is the first process that knows the film is over, and it
knows it in every ending there is. It marks the ending in the
retained play status it already publishes, the operator folds the
mark on the bus wake it already has, and the idle screen takes over
in bus time: milliseconds after the exit press, not seconds after
the pod dies.

## The three endings

The sidecar publishes the ending mark in each path:

* The exit press. The display's back action at the bare video used
  to quit mpv itself; now it broadcasts a `liken-exit` script message
  and the sidecar answers it: the mark goes out first, then the
  `quit`, so the ending is on the bus before mpv begins to die.
* mpv reaches the end of the last item on its own and closes its IPC
  socket, the observation the reporter already ends on.
* The kubelet sends SIGTERM, because someone deleted the `Play` or
  the operator replaced the pod. The quiesce path publishes the mark
  with the final position it already reports, just before it clears
  the retained status. A pod the operator replaces therefore shows
  the idle screen for the second the replacement takes to map, which
  used to be a black gap.

The mark is a field on the play status payload, so the single-writer
rule holds: the sidecar owns its `Play`'s report, and nothing else
writes that topic.

## The fold

The operator already subscribes to every play status and already
wakes a pass on each message. The pass treats a `Play` whose report
carries the ending mark as no longer active when it derives the
Player's state, so the derivation settles to `Idle`, and the
existing edge publishes the `Idle` status and then the re-present,
in that order, per plan 12.

The `Play`'s Kubernetes status keeps deriving from what exists, so
`kubectl get plays` shows the `Play` for the seconds the pod takes
to die. The API server holds what exists, and the bus carries the
presentable now.

## What weston does with it

The re-present recreates the idle surface while the film's surface
still stands, and weston's kiosk-shell reveals a newly mapped
surface along its seat-independent path, the same behavior the
plan-12 re-present stands on. So the idle screen draws on top of the
dying film, the ramp-down plays where a person is looking, and the
pod finishes its teardown behind a screen that already moved on. The
black gap between a film and the clock does not exist at all.

## Considered and set aside

* **The translator publishes the ending.** The translator knows the
  exit press first, but it does not know that mpv accepted the quit,
  and it is not the writer of the play status. The sidecar sits at
  the one point every ending passes through.
* **Advancing the `Play`'s phase from the report.** Folding the mark
  into the Kubernetes phase would make the API say a pod is gone
  while it exists. The phase keeps deriving from the pod, and only
  the Player's presentable state reads the mark.

## Proof

On `liken-1`, with the sailing demo:

* Exit a film from the controller. The idle screen takes over within
  a beat of the press, with the mark in motion, and no black gap.
* Let a film run out on its own. The same takeover plays at the end
  of the last item.
* Delete a running `Play` with `kubectl`. The same takeover plays.
