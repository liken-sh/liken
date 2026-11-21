# 15, The elevations of a player

Proposed and built on 2026-11-21.

This plan builds what [plan 13](13-sketches.md) left out of
"Elevations of a pattern" in [00-design.md](../00-design.md), with the
signal path of a `Player` as its first use. Everything it draws reads
fields that a cluster has today. [Plan 18](../18-the-speakers-of-a-room.md)
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
[plan 17](17-the-shapes-of-a-sketch.md) draws it, and it ends on a bar
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
    receiver: {reference: status.receiver.name, shape: amplifier, reads: [Model, Power, Input, Volume, Sound]}
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
        .        .        display  display  display   display   remote  .
        .        .        sink     sink     receiver  receiver  .       .
        machine  machine  idle     idle     bus       bus       claim   claim
    - name: the signal path
      question: Where do picture and sound go?
      sketch: |
        sink  machine  receiver  display  display
      wires:
        - {from: sink, to: machine, carries: sound}
        - {from: machine, to: display, via: receiver, carries: picture and sound}
  controller: {image: liken-sh/media-operator}
  docs: media:declare-a-player
```

The `Remote`'s document in the same file keeps its fields and gains
`healthy: {through: status.peripheral}`.

The room has the screen on top with the remote beside it, the sink and
the receiver side by side directly below the screen, and the machine
and the things a viewer does not see in the last row. The receiver is
not expected, because the `Player` CRD describes a `Player` with no
`status.receiver` as one that plays directly to its panel. The receiver is an
`amplifier`, which resolves to `amplifier.media.liken.sh`, the shape
that `media-operator` ships. The room has no speaker until plan 18, and
the `Sink` and the machine are plain `box`es in both elevations,
because the `Sink` is the machine's sound output and not a speaker.

### The signal path

One HDMI cable leaves the machine's output and goes through the
receiver, when the `Player` has one, to the panel. It carries picture
and sound. The `Sink` is the machine's sound output onto that cable,
and the `Display` is the picture on it.

- **A `Player` with a receiver** draws the `Sink`, a solid wire
  "sound" to the machine, and a solid wire "picture and sound" from
  the machine through the receiver to the screen. A Denon `Receiver`
  on a home cluster reports `status.denon.hdmi.audioOut` as `avr`, so
  the receiver plays the sound. Plan 18 adds the speakers after it.
- **A `Player` with no receiver**, which the `Player` CRD describes as
  one "that plays directly to its panel", draws the `Sink`, the
  machine, and a solid wire "picture and sound" from the machine
  straight to the screen. The receiver is not expected, so its place
  is blank.

The page draws what `status.receiver` states. A home cluster has a
`Player` whose `status.receiver` names a receiver on an optical input,
which takes sound from the television and no picture, so the CRD's
description of the field, "The Receiver that this Player's video goes
through", is not true of that object, and the page draws the picture
through that receiver.

### What plan 17 gives these elevations

[Plan 17](17-the-shapes-of-a-sketch.md) gives the screen, the
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

## What the build changed

On 2026-11-21 a workstation ran `transect` against a home cluster with
the `transect/` directories of `media-operator`, `display-operator`,
`equipment-operator`, and `audio-operator` linked under `--resources`,
and read both elevations of each `Player` at 1680 and 420 pixels. After the
changes below, the signal path of a `Player` with a receiver drew the
sound from the `Sink` to the machine, and one wire "picture and sound"
from the machine through the receiver to the screen. The signal path
of the `Player` with no receiver drew the same wire straight from the
machine to the screen. The room drew the remote of each `Player`
whose `Peripheral` was `Asleep` dimmed, with the reason, and the
battery of the remote whose `Peripheral` was connected, linked to the
`Peripheral`. The build differs from the proposal in these ways:

- **A wire to a shape with no port on the facing edge is checked when
  the shapes are found.** A role's shape is known only after
  `Kinds::find_shapes` resolves it, which plan 17's build moved out of
  the load. So the wire is left out, and its document gets the
  condition `NoPort`, of the type `ShapesFound`, as a missing shape
  gets `UnknownShape`. The document stays, so one wire does not hide
  the rest of a kind's knowledge. The test that loads each operator's
  `transect/` directory fails on `NoPort` too.
- **The health functions take a `ReferenceEnds`, and only the callers
  that read a health change.** `ReferenceEnds` is a trait of
  `transect-resources`, which the binary's `Snapshot` implements from
  the relations and the stores that it has, through the one function
  `knowledge::follow`. `condition`, `reported`, `resting`, and
  `at_rest` take it, and the callers that pass it are
  `snapshot/known.rs`, `pages/known.rs`, `pages/kinds.rs`,
  `pages/object/mod.rs`, and `transect-map`'s `objects/mod.rs`, whose
  `Sources` draw the dot of each custom resource on the namespace page.
  `map/workload/mod.rs` and `pages/machine.rs` call only `Deciding::of`,
  which takes none, so neither changes. A reading through a reference
  uses the same `ReferenceEnds`.
- **A health that leads to no object has a reason of its own,
  `Reason::Unfollowed`.** Its sentence is the object's name and why,
  such as "living-room: status.peripheral names nothing". A reason read
  through a reference names the object, not the object at the end of
  the reference, because the namespace page counts each reason for the
  object that it names. A reference to a kind that `transect` does not
  watch gives no health, because `transect` cannot say whether the
  object exists.
- **`KindKnowledge::healthy` is a condition or a `Through`.** One
  field has either a condition with its `calm` reasons or a reference
  to follow, so no document can state both, and
  `Deciding::condition` is empty for a kind whose health is through a
  reference. The parser refuses a `healthy` with both, or with
  neither, and a `through` with `calm`, because the knowledge of the
  kind at the end of the reference states which reasons are calm.
- **A reading through a reference needs a role whose reference names a
  kind.** Without a kind, the part has no knowledge in which to look
  for the reference, so the parser refuses the role.
- **A reading that does not resolve shows the message as its value.**
- **Two elevations with one slug is the one fault for two names.** Two
  elevations with one name have one slug, so the check of slugs
  replaces plan 13's check of names.
- **A script draws the path of each wire.** The browser lays out the
  grid and sizes each part to its text, so the server cannot know where
  a port is. The server writes each wire with its areas, their edges,
  and each end's place along its edge, and draws each part's port marks
  hidden. `wires.js` measures the marks and writes each path after each
  load, each swap, and each change of the sketch's size.
- **A wire is drawn under the parts.** Plan 17 put wires over the parts.
  Under them, a short wire from a bar to the port of a part behind
  another part is hidden where it passes behind, so no wire crosses
  text.
- **A wire that is alone at each of its ports runs straight when the
  two spans overlap.** Two parts of different heights on one floor
  otherwise drew a step of a few pixels halfway.
- **A sketch with wires has wider gaps between its columns,** so a wire
  can bend and carry its words between two parts that fill their
  areas. A sketch without wires keeps the gaps of plan 13.
- **Below 640 pixels no wire is drawn.** The areas gather into lines,
  so no area faces the area that it faces in the grid, and the list of
  wires shows in place of the drawing.
- **The head names the author as "described by"**, with the link that
  the card's "Described by" has.
- **The routes of the binary moved to `object_route.rs`.** The route of
  an elevation made `web/mod.rs` longer than 500 lines, so the routes
  of one object's pages moved to a file of their own. `Kinds` moved to
  `knowledge/kinds.rs`, a role to `knowledge/role.rs`, the tests of
  `knowledge/parse.rs` to a file of their own, and the readings of a
  page of a known kind to `pages/known/readings.rs`, for the same
  reason. The modules of `knowledge` depend on each other one way:
  `grid.rs` has the grid of a sketch and the edges where two areas
  face, `through.rs` follows a reference and depends on no other check,
  and `kinds/resolve.rs` confirms each `through` against another
  kind's knowledge. The enum of what a referenced object does for its
  referrer, `IssuesTls`, is `Duty`, so `Role` names one thing.
- **The signal path is one cable, and the receiver is optional.** The
  proposal drew the `Sink` and the machine as two boxes, each with a
  wire into the receiver, and a dashed sound wire into the empty
  receiver of a `Player` with no receiver. A reviewer read the first
  drawing as two cables, and read a `Player` that plays directly to its
  panel, which the `Player` CRD describes, as broken. So the `Sink`'s
  wire ends at the machine, one wire carries "picture and sound" from
  the machine through the receiver to the screen, and the receiver is
  not expected. The screen spans two of the five columns, so its title
  has room at desktop width.
- **The room puts the sink and the receiver directly below the
  screen.** In the proposal's four columns the screen, whose width is a
  share of the sketch, stood in the middle, the sink stood outside its
  width, and the receiver, whose area crosses the middle, stood in the
  middle of its two columns, 90 pixels from the sink. In eight columns
  the sink and the receiver each take two of the middle four, under the
  screen, and the remote stands beside the screen.
- **A wire names the parts as the drawing does.** The list of wires
  and the line under the sketch named roles, such as `machine` and
  `claim`, which the drawing shows nowhere. Each wire is now a sentence
  such as "picture and sound from Node a through Receiver b to Display
  c", each part linked, followed by the fields it was read from and the
  `KindKnowledge` that states it. A wire shows the same on hover and
  focus. The line under the sketch names the parts of each role that
  the elevation does not place, by the role's label, as "Not in the
  signal path: controlled by Remote a, …", and links the elevations
  that draw them.
- **A wire shows only once it is placed.** Each wire is hidden until
  `wires.js` places it, and the list of wires shows until the script
  has drawn them, so a page without the script names each wire and a
  page with it never shows a label at a corner. A mutation observer
  draws each sketch that the page swaps in, from a link or from the
  event stream, before the browser paints it.
- **A label wraps at spaces** to fit its segment less 8 pixels at each
  end, and the gap between the columns of a sketch with wires is 7.5% of
  its width, from 64 to 104 pixels.
- **A value on a display surface takes a size from the part's width**,
  and a narrow surface leaves out the names of its shown columns, so a
  value keeps its two lines and its ellipsis at every width. A part's
  name wraps balanced.
- **A reference that reads several values has an end for each.**
  `follow` returns each end, which plan 14's role `through` needs, and
  a health and a reading take the first. A value that matches several
  objects is its own end, and a health says so.
- **An elevation URL of an object whose kind states no elevation is not
  found.**
- **The fixture has a thermostat and two vents.** The shelf's room
  reads the temperature of the room's `Thermostat`, and a `Vent` is as
  healthy as the `Thermostat` that it names: one sleeps, and the other
  names one that the cluster does not have. The shelf's second
  elevation, "the restock", has a wire with `via` and a wire to an
  empty role, and the golden pages `sketch-restock.txt` and `vent.txt`
  cover the second elevation and a health through a reference.
