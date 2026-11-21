# 14, The rig of a telescope

Proposed on 2026-11-21. Nothing is built.

This plan draws the page of a `Telescope` as the instrument looks from
the side, the second sketch that an author states after the room of
[plan 13](completed/13-sketches.md). It adds a role that the objects
which name this one fill, an arrangement that repeats, and a role that
is expected only in some state, from "Who draws a pattern" in
[00-design.md](00-design.md). It ships the shapes that only the rig
uses, in the form of plan 17, the shapes of a sketch.

## The problem

A plan 13 role reads a field of the page's own object, and the
equipment of a telescope points the other way. Take a `Telescope`
named `refractor` with the trains `main` and `guide`. Each
`OpticalTrain`, `OpticalTube`, `Mount`, and `Guider` names `refractor`
in `spec.telescope`. Each `Camera`, `Focuser`, `FilterWheel`,
`Rotator`, `DustCap`, and `FlatPanel` names its train in
`spec.opticalTrain`, and the `Guider` names `guide` there too. The spec
of `refractor` names only its `Observatory`, so its page draws nothing
else.

A telescope has any number of trains, each with the same order of
parts, and some parts are optional. The INDI server runs only
while a reservation uses the telescope, so a role that is always
expected would mark every idle telescope as missing its server.

## The design

Roles are stated once, in `spec.roles`, and an elevation has a `name`,
a `question`, a `sketch`, and `wires`, as plan 15 defines them. This
plan adds fields to a role in `spec.roles` and rules to every sketch.

### A role filled by the objects that name this one

A role will take `namedBy` in place of `reference`:

```yaml
mount: {namedBy: {kind: Mount, path: spec.telescope}, shape: equatorial-mount, expected: true}
```

The role's parts are the objects of `kind` whose field at `path` names
the page's object. `group` defaults to the group of the page's kind.
`transect` reads the relation from the `references` entry that the
knowledge of `kind` states at `path`, so the entry's rules of matching
apply, and the entry already makes `transect` keep that kind's spec in
its watch. The form fits any kind whose objects name their parent, such
as the `HTTPRoute`s that name a `Gateway` in `parentRefs`.

A role also takes `label`, which replaces the reference's label. A
`namedBy` role's label is otherwise its kind in lower-case words, so
`FilterWheel` reads "filter wheel".

### A part drawn as its own kind's elevation

A role will take `elevation`, the name of an elevation of its parts'
kind. Each part is drawn as that elevation, with the roles of the
part's own kind read against the part, in the role's area. So the
author sketches one train, in the knowledge of `OpticalTrain`, and the
rig draws it once for each train. The train's roles state what a train
expects. The page of an `OpticalTrain` draws the same elevation as its
own sketch.

The parts of such a role are drawn one under another, in the order of
their names, and each is drawn in full. They are not a stack in plan
16's sense, because each train is a different instrument. `transect`
nests one level: inside a nested elevation, a role that names an
elevation draws its parts in its `shape`. It does the same when the
part's kind has no elevation of that name.

A nested elevation leaves out a part that is the page's own object,
because the page around it is that object. A role that has no part for
that reason leaves its place blank, even when it is expected. So a
shelf drawn on its room's page does not draw the room in its `room`
role.

### The nested board under a sketch

Today the page of a known kind also draws the board under its diagram.
`KnownPage::with_board` leaves out of the board's sections each value
that a box of the ring or a part of the sketch already draws, matched by
its link. `KnownPage::with_nesting` then adds the `Nesting` that
`part_of::nesting` builds from the `part of` edges: the `frame` and
`also`, which are what the object is part of, and `parts`, each with its
own `parts`. The nesting is not filtered, so the rig would draw each
train, tube, mount, guider, and camera a second time on the board.

When the page draws a sketch, `with_nesting` will apply the same test
as `with_board`, against every part of the sketch, nested elevations
included:

- An own part of a `Part` goes when the sketch draws it.
- A `Part` that the sketch draws goes when it has no own part left. A
  `Part` with an own part left stays, so the own part keeps the box of
  the whole that it belongs to.
- A `Part` that the sketch does not draw stays, as it is today.
- The `frame` and `also` stay, because the board draws them around the
  object as what it belongs to, and a sketch draws what the object is
  made of.

So the rig's board keeps the `Observatory` as its frame and loses every
part that the rig draws. A part that no role reads, such as a device of
a kind that no role names, is still on the board.

### The guide train

The drawing in 00-design.md puts the guide train on top of the main
tube, and the guide train is the train that the `Guider` names:

```yaml
guide: {namedBy: {kind: Guider, path: spec.telescope}, through: spec.opticalTrain, elevation: the train, label: guide train}
```

A role will take `through`, the path of a reference of the kind that
its first step reads. The first step is the role's `reference` or
`namedBy`. For each object that the first step reads, `transect`
follows the `references` entry at `through` in the knowledge of that
object's kind, and the objects at its end are the role's parts. Here
the first step reads each `Guider` of the telescope, and the part is
the `OpticalTrain` that the `Guider` names. A part's source line names
both steps: "from Guider spec.telescope, then spec.opticalTrain".
`through` is the one word for following a reference in every plan.
Plan 15 uses it in a reading, `{column: Battery, through:
status.peripheral}`, and in health, `healthy: {through:
status.peripheral}`. There the object at the end gives a reading or a
health to a part, and in a role's source the object at the end is the
part.

- An object of the first step whose field at `through` names nothing
  adds no part. When no object adds one, the role has no part, and an
  expected role's note says so: "no Guider names an OpticalTrain in
  spec.opticalTrain".
- A name at `through` that the cluster has no object for is a part
  marked as plan 13 marks a name with no object.
- Two objects of the first step that name one object add one part.

`through` follows one reference, so a role reads at most two steps.

### Each object once in an elevation

This is a rule of every sketch: an object is a part of at most one role
in an elevation, and the order of the roles in the grid decides which
role. The roles are taken in the order of their first cells, row by row
from the top and from left to right in a row. When several roles read
one object, the first of them draws it, and each later role leaves it
out. A sketch is a picture of one thing, and an object drawn twice
reads as two things, such as a third train on a telescope that has two.
The rule counts objects only. Plan 18's `speakers` reads the same
reference as `receiver`, and its parts are values with no object, so
both roles still draw.

The rig depends on that order. `guide` is above `main`, so `guide`
draws the train that the `Guider` names, and `main` draws each other
train. A sketch that placed `guide` below `main`, or to its right in
the same row, would move the guide train into `main` and leave `guide`
with no part. With no `Guider`, `main` draws every train. The page does not let a role claim what it left out. The source
line of `main` names each object that another role drew: "from
OpticalTrain spec.telescope, except OpticalTrain guide, which guide
train draws". An expected role whose objects other roles drew is not
missing a part, so it is drawn empty and at rest, as a role expected in
another state is, with that line as its note.

A second rule keeps an optional role from leaving a hole in a train: a
column takes no width when every cell in it belongs to a role that the
page leaves out, which is a role with no part that is not expected. A
column with a `.` in it keeps its width, so a column of `.` that an
author places as a spacer stays. A guide train with no focuser and no
filter wheel draws its camera against its tube. Each column of a plan
13 sketch has a role that draws or a `.`, so those sketches do not
change.

### A role that is expected in one state

`expected` will also take a condition type of the page's object, and
the role is expected while that condition is `True`:

```yaml
server: {reference: status.server.service, expected: {while: Active}, shows: [Host, Port]}
```

`observatory-operator` sets `Active` `True` on a `Telescope` from the
start of its reservation's activation until its deactivation begins.
The server's pod also runs during the deactivation steps, which reach
the devices through it. A part that is present is drawn whether or not
its role is expected, so that span does not matter.

### What the rig needs of the shape library

[Plan 17](completed/17-the-shapes-of-a-sketch.md) defines every shape. A role
names a shape by its bare name, and roles that draw the same physical
thing share one shape. In the knowledge of a kind in
`observatory.liken.sh`, `shape: tube` resolves to
`tube.observatory.liken.sh` first and then to the library. The rig is
seen from the side, with the light entering at the left.

- **From the library**: `disc` for a filter wheel, `tag` for the
  reservation, and a plain `box`, with no window and no knob, for a
  guider, a power switch, a GPS, a polar aligner, and the INDI server.
- **From `observatory-operator`**, because only its knowledge uses
  them: `tube`, a telescope's tube with its dew shield and rings;
  `drawtube`, for a focuser and a rotator; `cover`, for a dust cap and
  a flat panel, with its text to its left; `camera`, a cooled camera
  from the side; and `equatorial-mount`, a mount head on a tripod.
  `camera` moves to the library when a second shipped set of knowledge
  uses it, by plan 17's rule.

This plan ships those five as `Shape` documents in
`observatory-operator/transect/shapes.yaml`, in the group
`observatory.liken.sh`. Their starting point is plan 17's prototype of
that file, such as the tube:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: Shape
metadata:
  name: tube.observatory.liken.sh
spec:
  summary: A telescope's tube from the side, with the dew shield at the light's end on the left and two rings under it.
  size: {design: [512, 150], width: [200, 560]}
  axis: 60
  outline: {round: [12, 8, 8, 12], at: {left: 0, right: 0, top: 0, bottom: 30}}
  text: {inset: [14, 24, 44, 24]}
  joins: [left, right, top, bottom]
  pieces:
    - {name: dew shield, at: {left: 0, width: 48, top: -12, height: 144}, round: 12, clear: left}
    - {name: front ring, behind: true, at: {left: 30%, width: 16, bottom: 0, height: 34}}
    - {name: back ring, behind: true, at: {right: 30%, width: 16, bottom: 0, height: 34}}
```

The parts of the rig touch through plan 17's `joins`. When the facing
edges of two neighboring parts both join, no gap is left between them,
in a row and across rows. So the parts of a train stand on the light's
axis and touch end to end, the guide tube stands on its rings on the
main tube, and the main tube stands on the mount's saddle. Contact is a
fact of the wide layout: below 640 pixels, where the areas wrap into
lines, the rig does not rely on it. The rig has no wires.

The prototype has a fault that the build fixes by tuning these
definitions. At a width of 1272 pixels the guide train's camera is
taller than the guide tube, and its last line of text is hidden under
the main tube.

### Faults

Beside the faults of plans 13 and 15, `transect` will leave a document
out, with an `Invalid` condition that names the fault, when:

- a role of a `KindKnowledge` has both `reference` and `namedBy`, or
  neither. A role in a file of a catalog pattern of plan 16 has
  neither, because the kind's `pattern` fills it;
- a `namedBy` has no `kind` or no `path`, or an `elevation` or a
  `label` is empty;
- `expected` is not `true`, `false`, or `{while: <condition type>}`;
- a `through` is empty.

A fault that needs another document gets the condition `Unresolved`
from `Kinds::resolve`: a `namedBy` whose kind's knowledge has no
reference at `path` to this kind, a `through` that the first step's
kind's knowledge has no reference at, or that names no kind there, and
an `elevation` that the parts' kind does not state. As in every plan,
the document stays loaded, and the role that could not resolve shows
the condition's message in its place.

### What the page draws

- **A part** is drawn plainly in its shape, as in plan 13.
- **A part that deviates** is marked as in plan 13, on every shape.
- **A part at rest**, whose health condition is `False` with a `calm`
  reason, is dimmed. Between reservations that is every device of the
  rig, and the tubes stay plain because they have no `Ready`.
- **An expected role with no part** has a dashed outline, "no Camera",
  and "no Camera names this OpticalTrain in spec.opticalTrain".
- **An optional role with no part** leaves its place blank.
- **A role expected in another state** is drawn empty and at rest: a
  dashed outline in the color of rest, "no Service", and "expected while
  Active is True. Active is False now: Inactive." While the condition is
  `True`, it is an expected role.
- **A nested part** draws its elevation, then its label, kind, and name,
  linked to its page, after its last column. That line is marked as the
  head of a sketch is when the part deviates.

### The knowledge of `observatory-operator`

The knowledge in `transect/examples/liken/observatory.yaml` will move
to `observatory-operator/transect/kinds.yaml`, as plan 13 moved the
`Player`'s. The documents of `cameras`, `domes`, `filterwheels`,
`focusers`, `guiders`, `mounts`, `observatories`, `receivers`, and
`weatherstations` move as they are. Each document keeps
`controller: {image: liken-sh/observatory-operator}`. The `spec` of
`telescopes` and `opticaltrains` becomes:

```yaml
# telescopes: the telescope from the side, the light entering at the
# left. The guide train is above the trains that image. The mount is
# under them, with the devices that the rig carries and the INDI server.
summary: A mount and the optical trains on it, with the INDI server that a reservation connects to.
healthy: {condition: Ready, calm: [Idle, Inventory]}
references:
  - {path: spec.observatory, kind: Observatory, group: observatory.liken.sh, edge: part of}
  - {path: status.server.service, kind: Service, label: INDI server}
  - {path: status.server.node, kind: Node, edge: runs on}
  - {path: status.reservation.name, kind: Reservation, group: observatory.liken.sh, label: reserved by}
roles:
  guide:       {namedBy: {kind: Guider, path: spec.telescope}, through: spec.opticalTrain, elevation: the train, label: guide train}
  main:        {namedBy: {kind: OpticalTrain, path: spec.telescope}, elevation: the train, expected: true}
  mount:       {namedBy: {kind: Mount, path: spec.telescope}, shape: equatorial-mount, expected: true, reads: [State, RA, Dec, Driver]}
  power:       {namedBy: {kind: Switch, path: spec.telescope}, reads: [On]}
  gps:         {namedBy: {kind: GPS, path: spec.telescope}, reads: [Fix]}
  aligner:     {namedBy: {kind: PolarAligner, path: spec.telescope}, reads: [Adjustment]}
  server:      {reference: status.server.service, expected: {while: Active}, shows: [Host, Port]}
  reservation: {reference: status.reservation.name, shape: tag, reads: [Holder, Phase]}
elevations:
  - name: the rig
    question: What is this telescope made of?
    sketch: |
      guide  guide  guide  guide    .            .
      main   main   main   main     main         main
      mount  mount  mount  power    server       server
      mount  mount  mount  gps      reservation  reservation
      mount  mount  mount  aligner  .            .
---
# opticaltrains: the light's order, from a cover at the front to the
# camera, with the guider that reads the camera last.
summary: The path of light from one tube to a camera, and the devices on it.
healthy: {condition: ParentFound}
references:
  - {path: spec.telescope, kind: Telescope, group: observatory.liken.sh, edge: part of}
  - {path: spec.opticalTube, kind: OpticalTube, group: observatory.liken.sh, label: optical tube}
roles:
  panel:   {namedBy: {kind: FlatPanel, path: spec.opticalTrain}, shape: cover, reads: [Light, Brightness]}
  cap:     {namedBy: {kind: DustCap, path: spec.opticalTrain}, shape: cover, reads: [Cover]}
  tube:    {reference: spec.opticalTube, shape: tube, expected: true, reads: [Aperture, Focal Length]}
  focuser: {namedBy: {kind: Focuser, path: spec.opticalTrain}, shape: drawtube, reads: [Position]}
  rotator: {namedBy: {kind: Rotator, path: spec.opticalTrain}, shape: drawtube, reads: [Angle]}
  wheel:   {namedBy: {kind: FilterWheel, path: spec.opticalTrain}, shape: disc, reads: [Filter, Slot]}
  camera:  {namedBy: {kind: Camera, path: spec.opticalTrain}, shape: camera, expected: true, reads: [Temp, Setpoint, Cooler, Exposure]}
  guider:  {namedBy: {kind: Guider, path: spec.opticalTrain}, reads: [State, RMS]}
elevations:
  - name: the train
    question: What does the light pass through, in order?
    sketch: |
      panel  cap  tube  tube  tube  focuser  rotator  wheel  camera  guider
```

`opticaltubes` gains `healthy: {condition: ParentFound}`. The documents
of `rotators`, `dustcaps`, and `flatpanels` gain a reference at
`spec.opticalTrain` to `OpticalTrain`, and those of `switches`,
`gpses`, and `polaraligners` gain one at `spec.telescope` to
`Telescope`, each with `edge: part of`. The `namedBy` roles read them.

## What this plan leaves out

- **Wires and the move between elevations**, which plan 15 builds. The
  rig has no wires.
- **The patterns of `transect`'s catalog**, which plan 16 draws. This
  plan assumes nothing of them, and `namedBy` is open to them.
- **A tube that two trains share**, as with an off-axis guider. Each
  train draws its tube, so the rig draws it twice, because each nested
  elevation is an elevation of its own.
- **A size read from the cluster.** The guide train is smaller because
  its area is narrower in the sketch, not because of its aperture.
- **The reasons between `Idle` and `Ready`.** While a reservation
  starts, a device's `Ready` is `False` with a reason such as `Starting`
  or `Connecting`, and a `Telescope`'s has `Activating`. These are not in
  `calm`, so the page marks each part as failing until it connects,
  while `failing` in `observatory-operator`'s `events.go` counts only
  `Error` and a failed step. The form of `healthy` needs a way to say
  that a reason is normal, and that is a separate change.

## The alternatives

- **Read the `Telescope`'s status.** `observatory-operator` writes
  `status.trains[]`, `status.devices[]`, `status.guider.name`, and
  `status.tubes[]`, and a plan 13 `reference` can read the trains and
  the guider. `status.devices[]` and each train's `devices[]` put
  several kinds in one list with a `kind` field, and a reference has
  one kind. The field that names the parent is on every kind, and a
  summary like this is written only by some operators.
- **A repeated sketch in the `Telescope`'s document.** Its roles would
  read a train's fields, which only the knowledge of `OpticalTrain`
  describes, and an `OpticalTrain`'s page would have no sketch.
- **The server as optional, or as always expected.** Optional hides that
  a reservation needs it. Always expected draws an empty server on
  every idle telescope, which reads as a fault.
- **An `except` on `main` that names `guide`**, in place of the rule
  that draws each object once. It is explicit, and an author can read
  it in the YAML. The rule is still needed, because without it an
  author who forgets an `except` draws one train twice, and an object
  drawn twice is never right in a picture of one thing. With the rule,
  the `except` would add nothing, and the source line of `main` shows
  the reader what was left out.
- **A filter on the objects of `namedBy`**, such as "the trains that no
  `Guider` names". It is a query language in a role, for what one rule
  of the sketch does.
- **`while` on a field's presence**, such as `status.reservation`. A
  condition is the Kubernetes record of what is true now, and `Active`
  covers the span, so the form needs no path of the author's own.

## How the work is proved

Unit tests cover the parsing of each new field and each fault, and the
`Unresolved` condition. They cover a `namedBy` role with several parts,
none, a part in another namespace, and a referring kind with no
knowledge; a `through` that names nothing, names an object the cluster
lacks, or is named by two objects of the first step; a nested elevation
for each part, inside another nested one, with a name that the part's
kind does not state, and with a role that names the page's own object;
an object that more than one role reads, with the source line of the
later role, and an expected role whose objects other roles drew; a
column of roles that the page leaves out, and a column with a `.`; the
nesting under a sketch, with a `Part` that the sketch draws, one with an
own part that it does not, and the `frame`; and a `while` condition that
is `True`, `False`, and absent.

The fixture cluster's stock room gets an elevation whose shelves name
it, each drawn as the shelf's elevation, with the bins that name each
shelf, and a keeper expected while the room's `Open` condition is
`True`. One room is open with no keeper, and one is closed. A golden
page of the room covers the template. `transect`'s code, `examples/`,
and fixture name nothing of a telescope. The test
that loads each first-party `transect/` directory will also resolve
them together, so a `namedBy` or an `elevation` that names knowledge
nowhere in the repository fails. `make test` passes with the coverage
floor unchanged. A role that names a shape that is not loaded is drawn
as a `box`, and its `KindKnowledge` gets the condition `UnknownShape`
while the document stays, so the rig draws before its shapes load, in
boxes.

On a workstation, `transect` runs against a home cluster with
`observatory-operator/transect/` linked under `--resources`, by a name
that sorts before the cluster's own files. Each `Telescope` and
`OpticalTrain` page is read against `kubectl get` for the same objects,
between reservations and during one.
