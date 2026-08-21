# The player owns its remotes

Plan 04. Built, and drilled on `liken-1` on 2026-08-21, in release
2026.08.21-005. It moves the binding between a controller and a unit from the
`Remote` onto the `Player`, and it makes a `Player` edit reshape a
running `Play` without losing the film's place. When this plan lands,
a `Player` is the whole description of a unit of equipment, controllers
included, and editing one recreates its playback pod at the position it
had.

## The problem

The binding lives on the wrong resource. Today a `Remote` holds
`spec.bindings`, a list of the players it drives, so the controller
declares the unit. The design reads the other way. It calls a `Player`
"one named unit of equipment" and says a gaming unit "groups a
console's TV and controllers so that the equipment has a name". The
prose puts the controllers in the unit. The code drifted the other
way, and this plan corrects the drift.

The ownership matters for one concrete reason beyond tidiness. A
`Player` already declares the shape of a `Play`'s pod: its display, its
sinks, and its render node become the pod's claims. The controllers
that drive the unit belong to that same shape, because each one is a
sidecar in the pod. So the resource that owns the pod's shape should
own the controller list, and the operator should build the pod from
one read of the `Player` the `Play` names, not from a scan of every
`Remote`.

That ownership also answers a question the immutable pod raised. A
pod's container set is fixed once it runs, so adding a controller to a
unit cannot add a sidecar to a running pod. The pod must be recreated.
Recreation loses the film's place unless the operator carries the
place across, and carrying it across is work this plan does once, for
every shape change and not only for controllers.

## The reference moves to the Player

`Player.spec.remotes` is a list. Each entry names one `Remote` in the
same namespace and reserves a `keymap` slot that plan 06 fills with a
per-unit override. `Remote.spec.bindings` is removed. A `Remote` keeps
its device selector and the `Keymap` it uses, and it no longer names
any player.

The list holds one entry for now. This is the same trade
`spec.players` and the old `spec.bindings` made: a list of one costs
nothing to grow, and a grown list is no migration. Plan 06 lifts the
count. Until then a unit names one controller, and a controller may be
named by one unit.

The reference is a name, not a selector. A `Player` names its
`Remote`s the way it will name anything it owns, and a name is exact
where a selector invites the question of what a second match means.

## The namespace is the room

Every reference between a `Player`, a `Play`, and a `Remote` resolves
in the object's own namespace. Nothing points across a namespace
boundary. A person who wants to model rooms or floors as namespaces
gets a clean result: a room's `Player`s, `Remote`s, and the `Play`s
run against them all live together, and deleting the room's namespace
deletes the room.

Same-namespace is the Kubernetes rule for a reason this project keeps.
An owner reference cannot cross a namespace, and the design tears a
`Play`'s pod and a `Remote`'s pod down through owner references, so a
cross-namespace reference would break that teardown. A namespace is
also the unit of access control, and a reference that crosses it
reaches past the boundary the namespace exists to draw. The one
resource that is shared across rooms is the `Keymap`, and plan 05
lifts it to cluster scope rather than let any reference cross a
namespace.

## The operator builds the pod from the Player

A `Play` names one `Player` in `spec.players`. The operator reads that
`Player` and builds the pod from it: the claims from the device roles,
and the controller handling from `spec.remotes`. The controller list
is a field on the resource the `Play` already names, so the build is
one read. The scan of every `Remote` that the old binding forced is
gone.

Nothing else about the pod changes in this plan. The keymap still
reaches the sidecar in an environment variable, the playback pod is
still `mpv` and one bridge sidecar, and a unit still names one
controller. Plan 05 rebuilds the sidecar and the keymap path.

## A Player edit recreates the pod at its place

A `Player` owns the pod's shape, so an edit that changes the shape must
reach a running `Play`. The container set is immutable, so the operator
recreates the pod. It recreates gracefully, so the film keeps its
place.

The operator compares the running pod against the pod the current
`Player` would produce. When the two differ in shape, a changed
display selector, a changed sink, a changed device parameter, or a
changed controller, the operator reads the `Play`'s current position
from `status.position`, deletes the pod, and creates the replacement
with `mpv` set to start at that position. The image is already on the
machine, so `mpv` resumes within about a second. A rename of a field
that does not shape the pod, such as `spec.zone`, changes nothing and
recreates nothing.

The position source is the status the sidecar already reports. A pod
that has not yet reported a position recreates from the `Play`'s own
`spec.start`, so a shape change during startup loses nothing either. A
`Play` that is `Finished` or `Failed` is terminal and recreates
nothing.

This mechanism is the reason the immutable container set costs so
little later. Plan 05 makes each controller its own sidecar, and plan
06 lets a unit name several. Adding or removing a controller then
reshapes the pod, and this graceful recreate turns that reshape into a
sub-second resume instead of an interruption.

## Set aside for this plan

* **The keymap path.** The compiled keymap is still carried in an
  environment variable, and the playback pod is still `mpv` and one
  bridge sidecar. Plan 05 moves the keymap onto the bus and splits the
  sidecar.
* **The `Keymap` scope.** A `Keymap` stays namespaced in this plan.
  Plan 05 lifts it to cluster scope when it moves onto the bus.
* **Several controllers per unit, and focus.** `spec.remotes` holds one
  entry, so no controller drives two plays and nothing arbitrates
  focus. Plan 06 lifts the count.
* **The per-unit keymap override.** The `keymap` slot on a remote entry
  is reserved and unread. Plan 06 fills it.

## How it will be proved

On `liken-1`, with one monitor and a paired DualSense, the way plan 03
was proved. The `Remote` from plan 03 loses its `spec.bindings`, and
the `Player` gains `spec.remotes` naming it.

The drill checks each claim:

* A `Play` starts and its pod builds from the `Player`'s `spec.remotes`.
  The X button pauses the film through the same environment-variable
  keymap path plan 03 used, so the ownership move changed the source of
  the controller list and nothing else.
* The `Player` is edited mid-film. A changed device parameter reshapes
  the pod, and the operator recreates it at the position the status
  reported, which `mpv` resumes within about a second. The position
  keeps advancing across the recreate.
* A `Player` edit that does not shape the pod, a `spec.zone` rename,
  recreates nothing and the film plays on undisturbed.
* The `Play` is deleted, and its pod tears down through the owner
  reference.
