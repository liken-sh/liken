# Focus and many remotes

Plan 06. Built, and drilled on `liken-1` on 2026-08-21, in release
2026.08.21-005. It lets a `Player` name several controllers and a `Remote`
drive several units, gives each unit a per-controller keymap override,
and arbitrates the one press that would otherwise reach two plays.
When this plan lands, one controller can drive the theater and the
gaming unit, a source button switches which one it holds, and a press
lands on exactly one film.

## The problem

A unit names one controller and a controller drives one unit. Lifting
the first is easy and needs no arbitration: several controllers on one
unit each translate into that unit's one command topic, and both
driving one film is the couch working as it should. Plan 05's
translator sidecar per controller already covers it.

The second is the hard one. Let a `Remote` be named by two `Player`s,
each with an active `Play`, and one physical controller feeds two
command topics. A single cross press pauses both films, because the
controller has a translator sidecar in each pod and both read its one
events topic. This is focus: which of a controller's units holds it
right now.

## The count lifts

`Player.spec.remotes` may name several controllers, and a `Remote` may
be named by several `Player`s. The relation is many to many, the shape
the design named from the start. Each named controller still gets one
translator sidecar in the `Play`'s pod, so a `Remote` that two active
`Play`s name has a translator sidecar in each of their pods.

Adding or removing a controller reshapes the pod, and the operator
recreates it at the film's place through plan 04's graceful recreate.
So a controller bound to a running unit joins within about a second and
does not interrupt the film.

## The per-unit keymap override

The `keymap` slot plan 04 reserved on a remote entry fills here. A
`Player`'s remote entry may name a `Keymap` that overrides the
`Remote`'s base keymap for that unit, so one controller's cross means
`play-pause` on the theater and something else on the gaming unit.

The override needs no new topic. A `Keymap` is a cluster-scoped shared
vocabulary with its own retained topic, so an override is one more
named `Keymap`, and the operator tells the translator sidecar which
`Keymap` topic to read. A `Remote` with no override reads its base
keymap's topic; a `Remote` with an override reads the override's. Both
are shared topics that any translator of that combination subscribes
to.

## The focus mark

`liken/media/remotes/<namespace>/<name>/focus` carries the mark, the
topic plan 03 named and left unused. It is `retained` control-plane
state the operator writes, and it holds the `Play` that currently owns
this controller. Every translator sidecar for the controller subscribes
to it and gates on it: it applies the keymap and publishes commands
only when the mark names its own `Play`, and it stays quiet otherwise.

The press stays on the data plane and the mark stays on the control
plane. A press reaches the owning film with the operator up or down.
Only a change of owner needs the operator, because only the operator
holds the graph the owner is computed from.

## The default is most-recent steals

When a `Play` starts on a unit a controller drives, the operator sets
that controller's mark to the new `Play`. The most recent film steals
the controller. This is the friendly default for the common act: start
a film, and the controller in your hand drives it.

Whether a running film should hold a controller against a new film that
steals it is a question of feel, not of logic, and it is answered on
hardware and not on paper. This plan builds most-recent-steals and the
source-button switch below. A hold-instead-of-steal knob is a follow-on
decided on the studio rig, and the switch covers the case either way.

## The source button switches focus

A `cycle-focus` action joins the vocabulary, the controller's own
input or source button. It is not a media command and never reaches
`mpv`. The translator sidecar that currently holds focus is the only
one that acts on a press, so it is the only one that publishes a cycle
request, to `liken/media/remotes/<namespace>/<name>/focus/cycle`, an
event topic the operator reads.

The operator computes the controller's bound-and-active `Play`s in a
stable order, advances one step from the current mark, and rewrites the
retained mark. Every translator re-reads the mark and re-gates: the
next `Play`'s sidecar goes live, and the one that held focus goes
quiet. The operator acts only on the focus path, which is control-plane
by definition, and never on the media path a press travels.

The mark is one retained topic with two triggers. A new `Play` moves it
by the most-recent-steals rule, and a source press moves it by the
cycle. Focus is not built twice.

## The operator owns the graph

Only the operator reads every `Player` and every `Play`, so only it
holds a controller's full set of bound-and-active `Play`s and their
order. A translator sidecar holds only its own `Play`, so it cannot
compute the next in a cycle, and the standing remote pod holds its
controller's bindings but not which of their `Play`s are live. The
operator holds the whole graph, so the operator writes the mark for
both the automatic steal and the manual cycle.

The degraded case is honest. With the operator down, a source press
cannot switch focus, because nothing recomputes the mark. The film that
holds focus keeps taking presses, because the mark is retained and the
translators keep gating on the value the broker still holds. This is
the same trade the whole design makes: the data plane outlives the
control plane.

## Set aside for this plan

* **The hold-instead-of-steal knob.** This plan builds
  most-recent-steals. Whether a running film should refuse a steal is
  decided on the studio rig, and the source-button cycle covers the
  switch under either default.
* **Outside control that starts a `Play`.** A command topic and a
  standing controller could create a `Play` from a button, and that is
  the first sliver of a frontend, out of scope until the frontend is
  designed.
* **Arbitration between a press and an outside command.** A `Remote`
  and a phone can both command one `Play` at once. Focus arbitrates
  between controllers, and nothing arbitrates between a press and a
  command that a program publishes.
* **Broker access control.** A translator publishes only its own
  commands by convention, not by an ACL. That is
  `open-problems/the-bus-authorizes-nothing.md`.

## How it will be proved

On `liken-1`, with both studio monitors, the BOE and the LG, as two
`Player`s and one paired DualSense named on both. A `Play` runs on
each.

The drill checks each claim:

* A cross press pauses the film on the focused unit and leaves the
  other playing. This is the contention the earlier plans could not
  create and this one resolves.
* A second `Play` starts, and the mark steals to it. The controller now
  drives the newer film, most-recent-steals proven.
* A source press cycles the mark back, and the other unit's `Play`
  takes the presses. The switch is proven under a person's thumb.
* The operator is killed. Presses still reach the film that holds
  focus, and a source press does not switch until the operator returns.
* A controller is added to a running unit's `Player`, and its pod
  recreates at the film's place within about a second through plan 04's
  graceful recreate.
