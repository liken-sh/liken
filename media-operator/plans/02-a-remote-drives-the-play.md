# A remote drives the play

Plan 02. Built, and drilled on `liken-1` on 2026-08-20, in release
2026.08.20-004. The second slice of the design: `Remote` and
`Keymap`, and one controller in a person's hands controlling a
running play. No bus, no standing remote pod. The proof was a paired
DualSense pausing a film, and `status.paused` following it.

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

## How it was proved

On `liken-1` on 2026-08-20, with the portable BOE monitor and a
paired DualSense, in release 2026.08.20-004. A `Keymap` named
`dualsense` mapped the face buttons and the hat to the action
vocabulary, and a `Remote` selected the controller by its
`bluetooth.liken.sh` address and bound it to the `lab-portable`
player. The same film from plan 01 played, with the sidecar beside
`mpv`.

The pod scheduled with two containers, `player` and
`remote-player-one-pad`, and both reached ready. A press of the X
button flipped `status.paused` from absent to `true` inside one
report, at position `0:17:40`, and a second press cleared it. The
sidecar's own log stayed empty, which is the quiet it reports on a
run with no error.

One fact the drill taught, absent from the design: a sleeping
controller carries two device taints, not one. The
`bluetooth.liken.sh/disconnected` taint is `NoExecute`, and the
operator tolerates it with no time limit so a mid-film sleep never
evicts the pod. A sleeping controller also carries
`bluetooth.liken.sh/no-input-node`, a `NoSchedule` taint the
operator does not tolerate, because a controller with no input node
has nothing to deliver into the container. So a `Play` bound to a
controller starts only once that controller is awake, and it keeps
playing through every sleep after. The film waited `Pending` until
the DualSense connected, then scheduled at once.

The hand-written driver manifests this replaces are retired.
