# 14, The rig of a telescope

Proposed on 2026-11-21. Nothing is built.

This plan draws the page of a `Telescope` from the side, the second
sketch that an author states after [plan 13](completed/13-sketches.md)'s
room, as "Who draws a pattern" in [00-design.md](00-design.md) asks. It
adds a role that the objects which name this one fill, an arrangement
that repeats, and a role expected only in some state, and it ships the
rig's shapes in the forms of [plan 17](completed/17-the-shapes-of-a-sketch.md)
and [plan 19](completed/19-a-kind-states-its-shape.md).

## The problem

A plan 13 role reads a field of the page's own object, and a
telescope's equipment points the other way. For a `Telescope` named
`refractor` with the trains `main` and `guide`, each
`OpticalTrain`, `OpticalTube`, `Mount`, and `Guider` names `refractor`
in `spec.telescope`. Each `Camera`, `Focuser`, `FilterWheel`,
`Rotator`, `DustCap`, and `FlatPanel` names its train in
`spec.opticalTrain`, and the `Guider` names `guide` there too. The spec
of `refractor` names only its `Observatory`, so its page draws nothing
else.

A telescope has any number of trains, each with the same order of
parts, and some parts are optional. The INDI server runs only while a
reservation uses the telescope, so a role that is always expected would
mark every idle telescope as missing its server.

## The design

Roles are in `spec.roles`, and an elevation has a `name`, a `question`,
a `sketch`, and `wires`, as [plan 15](completed/15-the-elevations-of-a-player.md)
built them. This plan adds fields to a role and rules to every sketch.

### A role filled by the objects that name this one

A role will take `namedBy` in place of `reference`:

```yaml
mount: {namedBy: {kind: Mount, path: spec.telescope}, expected: true}
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

### A role that follows one more reference

A role will take `through`, the path of a reference of the kind that
its first step reads. The first step is the role's `reference` or
`namedBy`:

```yaml
guide: {namedBy: {kind: Guider, path: spec.telescope}, through: spec.opticalTrain, elevation: the train, label: guide train}
```

For each object of the first step, `transect` follows the reference at
`through` with `knowledge::follow`, through the `ReferenceEnds` that
the binary's `Snapshot` implements, and adds no second way to follow a
reference. A reading and a health take the first end, and a role takes
every end as a part: `Followed::Found` as any part, `Missing` marked as
plan 13 marks a name with no object, `Unwatched` plainly, and
`Ambiguous` as plan 13 draws an ambiguous name. An object of the first
step whose field names nothing adds no part, and two objects that name
one object add one part. An expected role with no part says "no Guider
names an OpticalTrain in spec.opticalTrain", and a part's source line
names both steps: "from Guider spec.telescope, then spec.opticalTrain".
A role follows at most one `through`. In a reading or a health the
object at the end gives a value to a part, and in a role it is the part.

### A part drawn as its own kind's elevation

A role will take `elevation`, the name of an elevation of its parts'
kind. Each part is drawn as that elevation, with the roles of the
part's kind read against the part, in the role's area, and with no head
and no tabs. So the author sketches one train, in the knowledge of
`OpticalTrain`, and the rig draws it once for each train. The page of
an `OpticalTrain` draws the same elevation, at
`.../elevations/the-train`, as its only elevation.

- The parts are drawn one under another, in the order of their names,
  and each in full. They are not a stack in plan 16's sense, because
  each train is a different instrument.
- After its last column, a nested part draws its label, kind, and name,
  linked to its page. That line is marked as the head of a sketch is
  when the part deviates.
- `transect` nests one level. Inside a nested elevation, and when the
  part's kind has no elevation of that name, a role with `elevation`
  draws each part in its shape.
- A nested elevation leaves out a part that is the page's own object,
  because the page around it is that object. A role with no part for
  that reason leaves its place blank, even when it is expected. So a
  shelf drawn on its room's page does not draw the room.

### The nested board under a sketch

`KnownPage::with_board` leaves out of the board under a diagram each
value that the sketch draws, matched by its link. `KnownPage::with_nesting`
then adds, unfiltered, the `Nesting` that `part_of::nesting` builds from
the `part of` edges: the `frame` and `also`, which the object is part
of, and `parts`, each with its own `parts`. So the rig would draw each
train, tube, mount, guider, and camera a second time.

When the page draws a sketch, `with_nesting` will apply the test of
`with_board` against every part of the sketch, nested elevations
included. An own part of a `Part` goes when the sketch draws it. A
`Part` that the sketch draws goes when it has no own part left, and
stays with the own parts that are left, so each keeps the box of its
whole. A `Part` that the sketch does not draw stays. The `frame` and
`also` stay, because they are what the object belongs to, and a sketch
draws what it is made of. So the rig's board keeps the `Observatory` as
its frame, and only the parts that no role reads.

### Each object once in an elevation

This is a rule of every sketch: an object is a part of at most one role
in an elevation, and the order of the roles in the grid decides which
role. The roles are taken in the order of their first cells, row by row
from the top and from left to right in a row. When several roles read
one object, the first of them draws it, and each later role leaves it
out. A sketch is a picture of one thing, and an object drawn twice
reads as two things, such as a third train on a telescope that has
two. The rule counts objects only. Plan 18's `speakers` reads the same
reference as `receiver`, and its parts are values with no object, so
both roles still draw.

`guide` is above `main`, so `guide` draws the train that the `Guider`
names, and `main` draws each other train. A sketch that placed `guide`
below `main`, or to its right in the same row, would move the guide
train into `main` and leave `guide` with no part. With no `Guider`,
`main` draws every train. The source line of `main` names each object
that another role drew: "from OpticalTrain spec.telescope, except
OpticalTrain guide, which guide train draws". An expected role whose
objects other roles drew is drawn empty and at rest, with that line as
its note, because it is not missing a part.

A column takes no width when every cell in it belongs to a role that
the page leaves out, which is a role with no part that is not expected.
A column with a `.` keeps its width, so a spacer column stays. A guide
train with no focuser and no filter wheel draws its camera against its
tube. Each column of a plan 13 sketch has a role that draws or a `.`,
so those sketches do not change.

### A role that is expected in one state

`expected` will also take a condition type of the page's object, and
the role is expected while that condition is `True`:

```yaml
server: {reference: status.server.service, expected: {while: Active}, shows: [Host, Port]}
```

`observatory-operator` sets `Active` `True` on a `Telescope` from the
start of its reservation's activation until its deactivation begins.
The server's pod also runs during the deactivation steps, and a part
that is present is drawn whether or not its role is expected.

### The shapes of the rig

The rig is seen from the side, with the light entering at the left. A
part takes its shape from its role, then from the knowledge of its
kind, then `box`, as plan 19 states. So the knowledge of each kind of
the observatory states its shape once, and the roles state none, except
the reservation, which the rig draws as a `tag`.

| kind | shape | home |
|---|---|---|
| `OpticalTube` | `tube` | `observatory-operator` |
| `Focuser`, `Rotator` | `drawtube` | `observatory-operator` |
| `DustCap`, `FlatPanel` | `cover` | `observatory-operator` |
| `Camera` | `camera` | `observatory-operator` |
| `Mount` | `equatorial-mount` | `observatory-operator` |
| `FilterWheel` | `disc` | the library |
| `Guider`, `Switch`, `GPS`, `PolarAligner`, the server's `Service` | `box`, with no window and no knob | the library, by default |

This plan ships the five shapes of `observatory-operator` as `Shape`
documents in `observatory-operator/transect/shapes.yaml`, in the group
`observatory.liken.sh`, so `shape: tube` in that group's knowledge
resolves to `tube.observatory.liken.sh`. `camera` moves to the library
when a second shipped set of knowledge uses it, by plan 17's rule. The
definitions start from plan 17's prototype of that file, such as the
tube:

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

The prototype has a fault that the build fixes by tuning these
definitions: at 1272 pixels the guide train's camera is taller than the
guide tube, and its last line of text is hidden under the main tube.

### The parts touch

The rig's parts touch through plan 17's `joins`, in the wide layout
only, because below 640 pixels nothing joins.

- **In a row.** The parts of a train join on their left and right, so
  they touch end to end on the light's axis. They differ in height, so
  this plan builds plan 17's negative block margin, which plan 17's
  build left out because no sketch mixed such shapes. On an edge of a
  row that a part joins, each other part takes a negative block margin
  and adds nothing to the row's height there, so the joining part's
  edge is the row's edge, and a taller part stands past it. The rule is
  in the shapes' code and holds for every shape.
- **Across rows.** A gap between two rows closes when every pair of
  areas that face each other across it join or have an empty side. The
  area of a nested elevation joins on an edge where a part in its row
  at that edge joins. So the guide tube joins the main tube, and the
  main tube joins the mount's saddle. The row under `main` has `.` cells
  beside the mount, because a box does not join and would keep the gap
  open.

### Faults

Beside the faults of plans 13, 15, 17, and 19, `transect` will leave a
document out, with an `Invalid` condition that names the fault, when:

- a role of a `KindKnowledge` has both `reference` and `namedBy`, or
  neither. A role in a file of a catalog pattern of plan 16 has
  neither, because the kind's `pattern` fills it;
- a `namedBy` has no `kind` or no `path`, or an `elevation` or a
  `label` is empty;
- `expected` is not `true`, `false`, or `{while: <condition type>}`;
- a `through` is empty.

The faults that need the knowledge of another kind get the condition
`Unresolved`, from the checks in `knowledge/kinds/resolve.rs` that
`Kinds::resolve` runs: a `namedBy` whose kind's knowledge has no
reference at `path` to this kind, a `through` that the first step's
kind's knowledge has no reference at or that names no kind there, and
an `elevation` that the parts' kind does not state. A role's `through`
keeps its reason in `Through::unresolved`, as a reading's does. As in
every plan, the document stays loaded, and the role that could not
resolve shows the condition's message in its place.

### What the page draws

Each state of a part and of an empty role is drawn as in plan 13, in
the part's shape. A part at rest is dimmed, which between reservations
is every device of the rig, and the tubes stay plain because they have
no `Ready`. An expected `namedBy` role with no part says "no Camera
names this OpticalTrain in spec.opticalTrain". A role expected in
another state is drawn empty and at rest: a dashed outline in the color
of rest, "no Service", and "expected while Active is True. Active is
False now: Inactive." While the condition is `True`, it is an expected
role. The rig is the `Telescope`'s only elevation, at
`.../elevations/the-rig`, so its head names it with no tabs.

### The knowledge of `observatory-operator`

The knowledge in `transect/examples/liken/observatory.yaml` will move
to `observatory-operator/transect/kinds.yaml`, as plan 13 moved the
`Player`'s. The documents of `domes`, `guiders`, `observatories`,
`receivers`, and `weatherstations` move as they are. Each document
keeps `controller: {image: liken-sh/observatory-operator}`. The `spec`
of `telescopes` and `opticaltrains` becomes:

```yaml
# telescopes: from the side, the light entering at the left. The boxes
# stand beside the mount, below the row where the main tube meets it.
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
  mount:       {namedBy: {kind: Mount, path: spec.telescope}, expected: true, reads: [State, RA, Dec, Driver]}
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
      mount  mount  mount  .        .            .
      mount  mount  mount  power    server       server
      mount  mount  mount  gps      reservation  reservation
      mount  mount  mount  aligner  .            .
---
# opticaltrains: the light's order, with the guider that reads the camera last.
summary: The path of light from one tube to a camera, and the devices on it.
healthy: {condition: ParentFound}
references:
  - {path: spec.telescope, kind: Telescope, group: observatory.liken.sh, edge: part of}
  - {path: spec.opticalTube, kind: OpticalTube, group: observatory.liken.sh, label: optical tube}
roles:
  panel:   {namedBy: {kind: FlatPanel, path: spec.opticalTrain}, reads: [Light, Brightness]}
  cap:     {namedBy: {kind: DustCap, path: spec.opticalTrain}, reads: [Cover]}
  tube:    {reference: spec.opticalTube, expected: true, reads: [Aperture, Focal Length]}
  focuser: {namedBy: {kind: Focuser, path: spec.opticalTrain}, reads: [Position]}
  rotator: {namedBy: {kind: Rotator, path: spec.opticalTrain}, reads: [Angle]}
  wheel:   {namedBy: {kind: FilterWheel, path: spec.opticalTrain}, reads: [Filter, Slot]}
  camera:  {namedBy: {kind: Camera, path: spec.opticalTrain}, expected: true, reads: [Temp, Setpoint, Cooler, Exposure]}
  guider:  {namedBy: {kind: Guider, path: spec.opticalTrain}, reads: [State, RMS]}
elevations:
  - name: the train
    question: What does the light pass through, in order?
    sketch: |
      panel  cap  tube  tube  tube  focuser  rotator  wheel  camera  guider
```

The documents of `opticaltubes`, `focusers`, `rotators`, `dustcaps`,
`flatpanels`, `cameras`, `mounts`, and `filterwheels` gain the `shape`
that the table above gives their kind, and `opticaltubes` also gains `healthy: {condition: ParentFound}`.
`rotators`, `dustcaps`, and `flatpanels` gain a reference at
`spec.opticalTrain` to `OpticalTrain`, and `switches`, `gpses`, and
`polaraligners` one at `spec.telescope` to `Telescope`, each with
`edge: part of`.

## What this plan leaves out

- **The patterns of `transect`'s catalog**, which plan 16 draws.
  `namedBy` is open to them.
- **A tube that two trains share**, as with an off-axis guider. Each
  train draws its tube, so the rig draws it twice, because each nested
  elevation is an elevation of its own.
- **A size read from the cluster.** The guide train is smaller because
  its area is narrower in the sketch, not because of its aperture.
- **The reasons between `Idle` and `Ready`.** While a reservation
  starts, a device's `Ready` is `False` with a reason such as
  `Starting` or `Connecting`, and a `Telescope`'s has `Activating`.
  These are not in `calm`, so the page marks each part as failing until
  it connects, while `failing` in `observatory-operator`'s `events.go`
  counts only `Error` and a failed step. Saying that a reason is normal
  is a separate change to `healthy`.

## The alternatives

- **Read the `Telescope`'s status**, `status.trains[]`,
  `status.devices[]`, `status.guider.name`, and `status.tubes[]`. Its
  `devices[]` lists put several kinds in one list with a `kind` field,
  and a reference has one kind. The field that names the parent is on
  every kind, and only some operators write such a summary.
- **A repeated sketch in the `Telescope`'s document.** Its roles would
  read a train's fields, which only the knowledge of `OpticalTrain`
  describes, and an `OpticalTrain`'s page would have no sketch.
- **The server as optional, or as always expected.** Optional hides that
  a reservation needs it. Always expected draws an empty server on
  every idle telescope, which reads as a fault.
- **An `except` on `main` that names `guide`.** An author can read it,
  but an author who forgets one draws a train twice, which is never
  right in a picture of one thing. So the rule is needed anyway, and
  with it an `except` adds nothing.
- **A filter on the objects of `namedBy`**, such as "the trains that no
  `Guider` names", which is a query language for what one rule does.
- **`while` on a field's presence**, such as `status.reservation`. A
  condition is the Kubernetes record of what is true now, and `Active`
  covers the span.

## How the work is proved

Unit tests cover the parsing of each new field and each fault, and each
`Unresolved` check. They cover a `namedBy` role with several parts,
none, a part in another namespace, and a referring kind with no
knowledge; a `through` for each kind of `Followed`, one that names
nothing, and two objects that name one; a nested elevation for each
part, inside another nested one, with a name that the part's kind does
not state, and with a role that names the page's own object; an object
that more than one role reads, the source line of the later role, and
an expected role whose objects other roles drew; a column of roles that
the page leaves out, and a column with a `.`; the nesting under a
sketch, with a `Part` that the sketch draws, one with an own part that
it does not, and the `frame`; a `while` condition that is `True`,
`False`, and absent; and the negative block margin for every shape of
the library, in a row with a part that joins and parts that do not.

The fixture cluster's stock room gets an elevation whose shelves name
it, each drawn as the shelf's elevation with the bins that name it, and
a keeper expected while the room's `Open` condition is `True`. One room
is open with no keeper, and one is closed. A golden page of the room
covers the template. `transect`'s code, `examples/`, and fixture name
nothing of a telescope. The test that loads each first-party
`transect/` directory will also resolve them together, so a `namedBy`
or an `elevation` that names knowledge nowhere in the repository fails.
A role or a kind that names a shape that is not loaded is drawn as a
`box` with the condition `UnknownShape`, and the document stays, so the
rig draws in boxes before its shapes load. `make test` passes with the
coverage floor unchanged.

On a workstation, `transect` runs against a home cluster with
`observatory-operator/transect/` linked under `--resources`, by a name
that sorts before the cluster's own files. Each `Telescope` and
`OpticalTrain` page is read against `kubectl get` for the same objects,
between reservations and during one. The rig is read at 1272, 1000,
and 800 pixels wide, where the trains touch, the guide train stands on
the main tube, the main tube stands on the mount, and no text is
hidden, and at 420 pixels, where nothing joins.
