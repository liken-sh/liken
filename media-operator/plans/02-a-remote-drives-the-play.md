# A remote drives the play

Plan 02. Proposed. The second slice of the design: `Remote` and
`Keymap`, and one controller in a person's hands controlling a
running play. No bus, no standing remote pod. The proof is a paired
gamepad pausing a film, and `status.paused` following it.

## The problem

Plan 01 plays media and nothing controls it. The status carries a
`paused` field that nothing has ever set to true, and stopping a film
five minutes early means deleting the `Play`. The founding design
answers input with a standing pod per `Remote` and a message bus, and
that architecture earns its cost only when remotes and players spread
across machines. The smallest proof of input needs none of it: one
controller, one running play, and a keymap between them.

Before this operator existed, the same proof ran from hand-written
manifests: a claim for the controller's input nodes, a driver pod
that mapped buttons to `mpv` commands, and a shared volume that put
the IPC socket in both pods. This plan makes the operator write that
wiring itself, the same promise plan 01 made for the playback pod.

## The resources

`Keymap` is one controller model's table from buttons and axes to
named actions. It is written once per model and shared by every
`Remote` of that model. The left side speaks evdev's names, `BTN_SOUTH`
and the hat axes, because those names are stable across every Linux
controller driver. The right side is a small action vocabulary this
plan defines: `pause`, `mute`, `seek` with a signed number of seconds,
`volume` with a signed step, `chapter` with a signed step, `subtitles`,
`audio`, and `info`. Raw `mpv` commands stay out of the API, as the
design requires, so a different player program can implement the same
actions later.

`Remote` is one physical controller. The spec selects its device the
way a `Player` selects a display: a device class and a CEL selector,
on an attribute like the controller's address. It names the `Keymap`
for its model, and it binds to players in `spec.bindings`, a list of
player names validated to length one for now, the same validation
`spec.players` carries. Per-binding keymap overrides wait for a later
plan.

## The shape, taken knowingly

The design set sidecars aside for the end state, and this plan runs
the remote as a sidecar anyway. The reader of the controller is a
second container in the playback pod: a native sidecar, an init
container with `restartPolicy: Always`, so a controller that sleeps
restarts the reader alone and the pod still ends when the player
ends.

Both costs the design named are real, and both are acceptable here.
The controller's claim joins the playback pod, so the pod lands on
the machine that owns the radio; today the one-machine rule already
puts a `Player`'s devices on one machine, and a radio elsewhere is
exactly the case the bus plan exists for. And a pod's container set
is immutable, so the remotes are fixed when the pod starts; a
`Remote` bound in the middle of a film joins at the next `Play`. The
bus plan retires the sidecar and keeps the resources: `Remote` and
`Keymap` survive unchanged, and only where the reader runs changes.

## The wiring the operator writes

When a `Play`'s player has bound remotes, the operator adds to the
pod it already builds:

* One claim request per remote, selecting the controller through the
  remote's class and selector. The disconnected taint is tolerated
  with no time limit, because a sleeping controller must never end
  the film. The sidecar alone holds the request, so the input nodes
  stay out of the player container.
* A shared `emptyDir` for `mpv`'s IPC socket. The supervisor moves
  the socket from the pod-private path onto that volume, at one
  constant path built into both modes of the binary.
* The sidecar itself: the same binary in a remote mode, in the same
  player image. The operator resolves each remote's `Keymap`,
  compiles the table, and passes it in an environment variable, so
  the pod stays free of API credentials and the map is as immutable
  as the container set.

The sidecar reads the claim's event nodes, translates each press
through the compiled table, and writes the matching command to the
socket. `mpv` echoes property changes back over the same socket, so
a pause pressed on the controller reaches `status.paused` through
the supervisor's existing reports, and no new report field is
needed.

## Set aside for this plan

* The message bus, the standing remote pod, and the receiver
  container. They arrive when remotes must outlive plays or live on
  other machines.
* Focus arbitration. A binding list of length one means no remote
  can drive two players.
* Per-binding keymap overrides, and any `Remote` status.
* Input that starts playback. A sidecar exists only while a `Play`
  does, so this plan cannot even express it.

## How it will be proved

On a lab machine, with a paired gamepad and a film already `Running`.
A `Keymap` for the gamepad's model, a `Remote` selecting it by
address and bound to the player, and then hands on the controller:
pause flips `status.paused` within one report, seek moves the
position, volume and chapter answer on the d-pad. The controller
then sleeps and wakes, and the film never stops: the sidecar
restarts, the pod stays, and the next press lands. The hand-written
driver manifests this replaces are retired the same day.
