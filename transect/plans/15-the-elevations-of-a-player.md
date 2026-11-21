# 15, The elevations of a player

Proposed on 2026-11-21. Nothing is built.

This plan builds what [plan 13](completed/13-sketches.md) left out of
"Elevations of a pattern" in [00-design.md](00-design.md), with the
signal path of a `Player` as its first use. Everything it draws reads
fields that a cluster has today. [Plan 18](18-the-speakers-of-a-room.md)
adds the speakers and the title on the screen, which wait on new
fields of two operators.

## The problem

A sleeping remote is drawn plainly in the room of a `Player`, because
a `Remote` has no conditions, and the `Connected` condition that says
it sleeps is on its `Peripheral`. The fact is on an object that a part
names, and plan 13 reads only the page's object and each part's own
object. Plan 13 also states the roles inside an elevation and draws
only the first, so the signal path of a `Player` has no place to go.

## The design

### A reading through a reference

An entry of `reads` or `shows` can be a map that names a printer
column of the object at the end of a reference:

```yaml
reads: [Keymap, {column: Battery, through: status.peripheral}]
```

`through` is the `path` of an entry of `references` that names a kind.
In `shows` it is a reference of the document's own kind. In `reads` it
is a reference of the part's kind, whose knowledge is often another
document. `transect` follows the reference and reads the column as
`kubectl get` prints it. A trimmed watch keeps every printer column of
a custom kind under `transect.liken.sh/columns` already, so the
watches do not change.

A reading has a value only when the reference names an object that
the cluster has and the column has a value. Otherwise it is left out,
and a `shows` entry falls to its next column. The value links to the
object it was read from, and its `title` says "Battery of Peripheral
aa-bb-cc-dd-ee-ff, through status.peripheral".

### A health through a reference

`healthy: {through: <path>}` says that an object is as healthy as the
object at the end of the reference, by that object's own knowledge.
The `Remote` states no condition and no `calm` of its own:

```yaml
healthy: {through: status.peripheral}
```

The `Peripheral`'s knowledge in `transect/examples/liken/bluetooth.yaml`
states `Connected` and `calm: [Asleep]` today. This work adds
`ClosedByDevice` to that `calm`, because the `Peripheral` CRD describes
it as a device that a person switched off. A `Remote` whose reference
names nothing, or names a `Peripheral` that the cluster does not have,
is a warning with the reason "status.peripheral names nothing" or "the
cluster has no such Peripheral".

`health::known::condition`, `reported`, `resting`, and `at_rest` in
`transect-observe` each read one object today. Each will take one more
argument: a lookup, which the binary builds from the snapshot, that
returns the stored object that a reference of the object names.
`Deciding::of` takes none, because the reference's `kind` names the
knowledge to use, and a kind whose health is `through` a reference has
no condition of its own, so its own conditions are drawn gray. Every
caller passes the lookup in the same change: `snapshot/known.rs`, which
gives the namespace page its health, `pages/known.rs`, `pages/kinds.rs`,
`pages/machine.rs`, `pages/object/mod.rs`, and `transect-map`'s
`map/workload/mod.rs`. A caller left out would mark every `Remote` as a
warning, because a `Remote` has no condition.

### Roles, stated once

`spec.roles` states each role once, with plan 13's fields
`reference`, `shape`, `expected`, `reads`, and `shows`. An elevation
has a `name`, a `question`, a `sketch` that places the roles it needs,
and `wires` when it has any. A role that no elevation places is still
knowledge that an agent can read. The catalog of
[plan 16](16-a-primary-with-replicas.md) uses the same form: `roles`
once at the top of a pattern's file, and elevations with `name`,
`question`, `sketch`, and `wires`.

This changes plan 13's form. An elevation with `roles` is refused with
a message that roles are stated once, in `spec.roles`. The documents in
that form are `media-operator/transect/kinds.yaml` and the fixture
`crates/transect/src/web/fixture/resources.yaml`, and the change moves
both.

### Wires

`wires` is a list of `from`, `to`, and `carries`, with `via` when a
role may stand between the two. `from`, `to`, and `via` are roles that
the same sketch places. A sketch whose wires run from left to right is
the design's pipeline form. The author places the stages, so the grid
in the YAML shows the order, and `transect` draws the wires in one SVG
under the parts, as the ring draws its lines.

A wire joins the facing edges of two roles' areas, horizontally or
vertically as the grid places them. It meets each part at a port of
the part's shape, a span along that edge, as
[plan 17](completed/17-the-shapes-of-a-sketch.md) draws it, and it ends on a bar
along the area's edge when the role has several parts. The wire has an
arrow at `to` and the words of `carries` on it.

- A wire between two roles that have parts is solid.
- A wire with `via` is drawn as two wires, `from` to `via` and `via` to
  `to`, when the `via` role has a part. When it has none, the wire is
  drawn straight from `from` to `to`, solid, past the `via` role's
  area.
- An expected role with no part is drawn empty, and each other wire
  that meets it is dashed.
- A role that is not expected and has no part is left out with its
  wires. `transect` does not join its neighbors unless a `via` says to.
- A line under the sketch names the roles that it does not place.

### Several elevations on one page

The object's URL draws the kind's first elevation, as now. Each
elevation also has a URL of its own,
`/namespaces/<namespace>/<resource>/<name>/elevations/<slug>`. A
cluster-scoped object is served at `/cluster/<resource>/<name>` by a
handler that draws no sketch, so it has no elevations. The slug is the
name in lower case with each run of characters other than letters and
digits made one hyphen, such as `the-signal-path`. The overview lens
tab is current on each, and the events stream follows the path, as on
every page. An unknown slug draws the first elevation with a line that
names the slug it did not find.

When a kind has more than one elevation, the head of the drawing has a
tab for each, in the look of the lens tabs and in the document's order.
The current tab is marked, and its question and its author follow. The
author is named as the card's "Described by" names it: the controller
that the knowledge names, or else the `KindKnowledge`. A kind with one
elevation shows its name, question, and author, with no tabs.

### The faults

Beside plan 13's faults, `transect` will leave out a document, with an
`Invalid` condition that names the fault, when:

- an elevation has `roles`;
- a sketch has a name that is not a role;
- a wire names a role that its sketch does not place, joins a role to
  itself, has a `via` that is its `from` or its `to`, repeats another
  wire, carries no words, or meets a role whose shape has no port on
  the facing edge, such as a `handheld` or a `tag`;
- a `through` in `shows` or `healthy` names no entry of `references`
  with a kind;
- two elevations have one slug, or a name gives an empty slug.

A `through` in `reads`, and a `healthy` `through` a reference to a kind
whose knowledge states no condition of its own, can be checked only
against another document. `Kinds::resolve` checks them. A fault gives
the document the condition `Unresolved`, the reason that plan 14 uses
too. The document stays loaded, and the reading or the health that
could not resolve shows the condition's message in its place.

### The knowledge of the `Player`

The `Player`'s document in `media-operator/transect/kinds.yaml` will be
this, with its comments rewritten for the new form:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: players.media.liken.sh
spec:
  summary: A screen and the speakers beside it that play media.
  healthy: {condition: Screen}
  references:
    - {path: "spec.remotes[].name", kind: Remote, group: media.liken.sh, label: controlled by}
    - {path: status.screen.monitor, kind: Display, group: display.liken.sh, label: shows on}
    - {path: status.receiver.name, kind: Receiver, group: equipment.liken.sh, label: plays through}
    - {path: spec.idle.controller, label: idle screen from}
    - {path: "status.sinks[].name", kind: Sink, group: audio.liken.sh, label: sound through}
    - {path: status.idle.bus.address, address: true, edge: depends on, label: talks over}
    - {path: status.screen.node, kind: Node, edge: runs on}
    - {path: status.idle.claim, kind: ResourceClaim, group: resource.k8s.io, edge: holds}
  roles:
    display:  {reference: status.screen.monitor, shape: screen, expected: true, reads: [Connector, Mode], shows: [Activity, [Play, Idle]]}
    receiver: {reference: status.receiver.name, shape: amplifier, expected: true, reads: [Model, Power, Input, Volume, Sound]}
    sink:     {reference: "status.sinks[].name", shape: box, expected: true, reads: [Layout, Volume, Mute]}
    machine:  {reference: status.screen.node, shape: box, expected: true}
    remote:   {reference: "spec.remotes[].name", shape: handheld, expected: true, reads: [Keymap, {column: Battery, through: status.peripheral}]}
    idle:     {reference: spec.idle.controller, shape: tag}
    bus:      {reference: status.idle.bus.address, shape: tag}
    claim:    {reference: status.idle.claim, shape: tag}
  elevations:
    - name: the room
      question: What is this player made of?
      sketch: |
        display  display   display   display
        sink     receiver  receiver  remote
        machine  idle      bus       claim
    - name: the signal path
      question: Where do picture and sound go?
      sketch: |
        machine  receiver  display
        sink     receiver  .
      wires:
        - {from: machine, to: display, via: receiver, carries: picture}
        - {from: sink, to: receiver, carries: sound}
  controller: {image: liken-sh/media-operator}
  docs: media:declare-a-player
```

The `Remote`'s document in the same file keeps its fields and gains
`healthy: {through: status.peripheral}`.

The room has the screen on top, the sink and the receiver side by side
below it, the remote beside the receiver, and the machine and the
things a viewer does not see in the last row. The receiver is an
`amplifier`, which resolves to `amplifier.media.liken.sh`, the shape
that `media-operator` ships. The room has no speaker until plan 18, and
the `Sink` and the machine are plain `box`es in both elevations,
because the `Sink` is the machine's sound output and not a speaker.

### The signal path

One HDMI cable leaves the machine's output and goes through the
receiver, when the `Player` has one, to the panel. The `Display` is
the picture on that cable and the `Sink` is the sound.

- **A `Player` with a receiver** draws the machine, a solid wire
  "picture" to the receiver, and a solid wire "picture" from the
  receiver to the screen. The `Sink` has a solid wire "sound" to the
  receiver. The sound ends at the receiver, which plays it: a Denon
  `Receiver` on a home cluster reports `status.denon.hdmi.audioOut` as
  `avr`. Plan 18 adds the speakers after it.
- **A `Player` with no receiver**, which the `Player` CRD describes as
  one "that plays directly to its panel", draws a solid wire "picture"
  from the machine straight to the screen, past the receiver's empty
  dashed place. The `Sink`'s wire to the empty receiver is dashed. The
  panel plays the sound, and no field of the `Player` states it, so no
  wire claims it.

The page draws what `status.receiver` states. A home cluster has a
`Player` whose `status.receiver` names a receiver on an optical input,
which takes sound from the television and no picture, so the CRD's
description of the field, "The Receiver that this Player's video goes
through", is not true of that object, and the page draws the picture
through that receiver.

### What plan 17 gives these elevations

[Plan 17](completed/17-the-shapes-of-a-sketch.md) gives the screen, the
`amplifier.media.liken.sh` of the receiver, and the `box` of the `Sink`
and the machine, each with the ports of its definition. A `box` and an
`amplifier` have ports on their left and right edges, and the wires of
one edge spread within the span. A wire leaves a port at a right angle
and bends halfway between two areas, so a part that stands on its
area's floor needs no other rule as a stage. The signal path also needs a
port on the left edge of the library's `screen`, and plan 17's
definition of the screen states its ports.

### At the boundary with plans 14, 16, and 18

`through` is the one word for following a reference. In a reading and
in `healthy`, it gives a part a fact of another object. As plan 14's
role field, it gives a role its parts: the objects at the end of the
reference, such as the `OpticalTrain` that a `Guider` names. This plan
assumes that plan 14 states its ways to fill a role as fields of a role
in `spec.roles`, and that its repeated arrangement applies to a
`sketch`. Plan 16's catalog elevations follow the kind's own, in the
same tabs and the same kind of URL, with `transect` as the author. When
one has the slug of a `KindKnowledge`'s elevation, its URL gets the
suffix `-by-transect`. Plan 18 adds a role with a part for each value
to `spec.roles`, and the speakers' places to the room.

## What this plan leaves out

- **The speakers and the title on the screen**, which plan 18 designs.
- **The `Television`**, whose `status.displays[].via` names the
  `Receiver` that the picture passes through. The `Player` does not
  name it.
- **Readings and signals on a wire**, the schematic and sequence forms,
  and a role that looks different from one elevation to the next.
- **The shapes** (plan 17), and plan 14's and plan 16's parts.

## The alternatives

- **A path into the other object**, such as
  `status.peripheral/status.battery.percentage`. It skips the printer
  column that the operator documents, and a trimmed watch drops it.
- **A string form, `Battery from status.peripheral`.** `from` would
  also be a wire's end, and plan 14 names the same act `through`.
- **A `Remote` that names the `Peripheral`'s condition and calm
  reasons.** The `Peripheral`'s knowledge states them already, and two
  copies can differ.
- **Roles inside each elevation**, as plan 13 has them. Two copies of
  a role can differ, and the design draws every elevation over the
  same roles.
- **Chains such as `machine --picture--> receiver`** that `transect`
  lays out. Plan 16 states wires as a list beside a grid, and one form
  of wire is one form for an author and a reader to learn.
- **Separate wires for a `Player` with a receiver and one without.**
  Every `Player` would draw the wires of the other case as dashed,
  which reads as a fault. A `via` draws each case as it is.
- **Elevations as lens tabs.** A lens is `transect`'s own and applies
  to every object of a sort, and an elevation comes from an author.

## How the work is proved

Unit tests cover each fault above. They cover a reading through a
reference with a value, with a reference that names nothing, and with
no such object, and a health through a reference in each of those
cases. They cover `Unresolved` for a reading and for a health. They
cover each state of a wire, and a `via` with and without a part. They
cover the slug and the routes, with an unknown slug, and the head with
one elevation and with several.

The fixture's shelf moves to `spec.roles`, reads a column of its room
through a reference, and gets a second elevation with wires and a
`via`, and a golden page covers each elevation. The fixture gets a
kind whose health is through a reference, and a test reads its health
on the namespace page and on its own page. `make test` passes with the
coverage floor unchanged.

On a workstation, `transect` runs against a home cluster with the
operators' `transect/` directories linked under `--resources`. Both
elevations of a `Player` with a receiver and of one with none are read
against `kubectl get players,receivers,sinks,peripherals -o wide`, and
a screenshot of each is compared with the design's mocks.
