# 13, Sketches

Proposed and built on 2026-11-20.  A workstation ran it against a
home cluster on the same day, and "What the run showed" at the end
has the result.

This plan builds the first part of "Who draws a pattern" in
[00-design.md](../00-design.md): the author of a kind states an
arrangement of roles, and `transect` draws it.  The first sketch is the
room of a `Player`.

## The problem

The page of a custom resource whose kind has a `KindKnowledge` draws
the object in the middle and a box for each reference around it.  Every
kind gets the same ring of equal boxes, so the drawing says nothing
about what the object is.  A `Player` is a screen with a receiver below
it, speakers beside it, and a remote in front, and the page draws its
display, its receiver, its message bus, and its `ResourceClaim` as the
same box at the same distance.

The knowledge of the `Player` is also in `transect/examples/liken`.
The design says that first-party knowledge ships with its operator.

## The design

### An elevation in a `KindKnowledge`

A `KindKnowledge` will take a list, `spec.elevations`.  Each elevation
has these fields:

- `name`: what the page calls the elevation, such as "the room".
- `question`: the question that it answers, as one sentence.
- `sketch`: lines of names separated by spaces.  Each name is a role,
  and `.` is an empty cell.  The cells of one name are the role's area.
- `roles`: one entry for each name in the sketch.

```yaml
elevations:
  - name: the room
    question: What is this player made of?
    sketch: |
      sound    display   display   display
      sound    display   display   display
      .        machine   receiver  receiver
      .        .         remote    .
    roles:
      display:  {reference: status.screen.monitor, shape: screen, expected: true, reads: [Connector, Mode]}
      receiver: {reference: status.receiver.name, shape: box, expected: true, reads: [Model, Power, Input]}
      sound:    {reference: "status.sinks[].name", shape: speaker, expected: true}
      machine:  {reference: status.screen.node, shape: box, expected: true}
      remote:   {reference: "spec.remotes[].name", shape: handheld, expected: true}
```

A role has these fields:

- `reference`: the `path` of one entry of the same document's
  `references`.  The objects or values that the reference reads are the
  role's parts.  The role's label is the reference's label.
- `shape`: one of the shapes that `transect` draws.  The first are
  `screen`, `box`, `speaker`, `handheld`, and `tag`.  A `tag` is a low
  outline for a thing that is in the cluster and not in the picture,
  such as a message bus beside a home theater.  The default is `box`.
- `expected`: whether a role with no part is drawn.  The default is
  `false`.
- `reads`: names of printer columns of the part's kind, as its CRD
  declares them.  The part shows each one that has a value, as the
  column's name in lower case and the value.

`transect` will leave out a document, with an `Invalid` condition that
names the fault, when:

- a row of the sketch has another count of cells than the first row;
- the cells of a name do not form one filled rectangle;
- a name is not a lower-case letter followed by lower-case letters,
  digits, and hyphens;
- the sketch has a name that `roles` does not, or `roles` has one that
  the sketch does not;
- a role's `reference` is the `path` of no entry of `references`;
- a `shape` is not one that `transect` draws;
- two elevations have the same name.

### What the page draws

When the knowledge of a kind has an elevation, the page of an object of
that kind will draw the first elevation in place of the ring of boxes.
Every other part of the page stays: the column of the kind's objects,
the lens tabs, the board of boxes under the diagram, and the card.  A
kind with no elevation keeps the ring.

The sketch is a grid with the sketch's rows and columns.  Above it, the
page shows the object's kind and name, its condition chips, its resting
line, and the elevation's name and question.  The author states only
the places, the shapes, and the readings.  `transect` draws the rest
the same way in every sketch:

- **A part.**  Each value that the role's reference reads is one part,
  in the role's shape.  It shows the role's label, the kind and name of
  the object with a link to its page, the readings, the note that the
  ring shows under a value today, and the field that it was read from.
  A role with several parts shows them side by side in its area.
- **A part that deviates.**  A part takes the health that its own
  kind's knowledge gives its object.  A failing or warning object is
  marked as a box on the namespace page is, and an object at rest is
  dimmed.  A kind with no knowledge is drawn plainly.
- **A part with no object.**  A reference with no kind reads a value,
  not an object.  Its part has an outline and no fill, and it shows the
  note "not an object in the cluster", as the ring does.
- **A name with no object.**  When a reference names an object of a
  watched kind and the cluster has no such object, the part is marked
  as a warning, with the note "the cluster has no such <Kind>".  The
  ring does not mark this, and the design calls a stated name the
  simplest expectation.
- **A role with no part.**  An expected role is drawn in its shape,
  with a dashed outline, "no <Kind>" or "nothing", and "<path> names
  nothing now".  A role that is not expected leaves its area blank.

The sketch draws every role whatever the row of edge kinds selects,
because a role that the reader hid would look like a role with no part.

The page draws only the first elevation.  A later plan adds the other
forms and the way to move between elevations.

### Where the knowledge is

The knowledge of the `Player` and the `Remote` will move from
`transect/examples/liken/media.yaml` to `media-operator/transect/`,
with the elevation added.  The operators of the kinds that a `Player`
refers to will each get a `transect/` directory with the condition that
reports the health of their kind: `display-operator` for the `Display`,
`equipment-operator` for the `Receiver`, and `audio-operator` for the
`Sink`.  So the receiver in the sketch of a `Player` is marked when
`equipment-operator`'s knowledge says that it is not reachable.

`transect` reads each of these as it reads any resource: as files under
its `--resources` directory.  The loader follows a symbolic link, so a
workstation links each operator's `transect/` directory and the
cluster's own resources into one directory.  When two files describe
one kind, the file whose path sorts first is used, which is the
loader's rule today.

`transect`'s code and its `examples/` will contain nothing about a
`Player`.  The test that loads `examples/` will also load each
first-party `transect/` directory of the repository, so a file that
does not load fails a test.

## What this plan leaves out

- **A role through a second reference.**  The speakers of a home
  theater are channels that a `Sink` reports, and the sleep of a remote
  is a condition of its `Peripheral`.  Each is read from an object that
  the `Player` names, not from the `Player`.
- **A shape that an author adds.**  The library has the shapes that the
  room needs.
- **The other forms**, the catalog's patterns, and moving between
  elevations.
- **How a cluster mounts an operator's knowledge.**  An image volume of
  the operator's image and a volume of the operator's repository both
  fit under `--resources`.  Nothing here chooses.
- **The other knowledge in `examples/liken`.**  It moves to its
  operators in a later change.
- **A check in each operator's own tests.**  A change to an operator's
  `transect/` directory is checked only when `transect`'s tests run.

## The alternatives

- **A template for each kind.**  An askama template compiles into the
  binary, so the knowledge of a `Player` would be in `transect`.  A
  template loaded at runtime would put markup from a resource in the
  page, and each author would draw health in another way.
- **A drawing that the author ships**, as SVG with named regions.  It
  has the same fault: the author draws the states.  It also gives an
  agent nothing to query.
- **Positions in pixels for each role.**  The ring computes pixel
  positions from the text of each box.  A grid of named areas needs no
  such computing, the browser sizes the rows to their text, and the
  author can read the sketch in the YAML.

## How the work is proved

Unit tests cover the parsing of a sketch and each fault above, and the
parts that the page builds for a role: one part, several, none when
expected, none when not, a value, a name with no object, and each
level of health.  The fixture cluster gets a kind with an elevation,
and the golden text of its page covers the template.  `make test`
passes with the coverage floor unchanged.

On a workstation, `transect` runs against a home cluster with the
operators' `transect/` directories linked under `--resources`, and the
page of each `Player` is read against `kubectl get` for the same
objects.

## What the run showed

On 2026-11-20 `transect` ran on a workstation against a home cluster
that has several `Player`s, with the `transect/` directories of
`media-operator`, `display-operator`, `equipment-operator`, and
`audio-operator` linked under `--resources` beside the cluster's own
resources.

- Each `Player` drew the room.  A `Player` with a receiver showed the
  receiver's model, power, input, and volume, as `kubectl get` printed
  them.  A `Player` with no receiver drew the receiver's place with a
  dashed outline and "no Receiver".
- The resources page listed every document as loaded, with no
  condition from the new files.
- For a short time after `transect` starts, the page of a `Player` has
  no sketch, because the store has not listed the custom resources yet.
  The page had the same gap before this plan.

The build differs from the proposal in these ways:

- **The `Sink` reads `Connected`, not `Ready`.**  `audio-operator`
  writes `Ready` as `False` with one reason both for an output with no
  device and for a connected device that PipeWire has no node for.  A
  television that is off is the common case, so `Ready` would mark a
  speaker as failing on most days.  `Connected` with its reasons in
  `calm` dims that speaker.  `transect` then does not show a connected
  `Sink` with no node.
- **The shape `tag` was added** while the room was drawn, for the
  message bus, the `ResourceClaim`, and the controller of the idle
  screen.  As boxes they looked like equipment in the room.
- **A role has a field `shows`.**  It names printer columns of the
  page's own object, and the part draws each one that has a value.  An
  entry that is a list draws the first of its columns that has a
  value.  The display of a `Player` shows the `Player`'s activity, and
  the play when one plays or the idle screen when none does.  The
  first drawing left the screen empty while a film played.
- **A part that is failing, a warning, or at rest shows the reason of
  its health condition,** with the condition's message in its title.
  Every part that is not plain also has a state word that only a
  screen reader reads.  A review found that the outline alone did not
  say why a part was marked, and said nothing to a reader who cannot
  see the outline.
- **The field that a part was read from shows when the pointer or the
  focus is on the part.**  The proposal put it on every part all the
  time, and a review found that those lines were a fifth of the text
  in the sketch.  The line under the sketch tells the reader to hover.
- **The parts of a sketch gather toward its middle.**  An area that is
  left of the middle of the grid puts its parts at its right edge, an
  area that is right of the middle puts them at its left edge, and an
  area that the middle crosses or touches keeps them in its center.
  Without the rule, the speaker of a `Player` stood far from the
  receiver beside it.
- **A value breaks after a separator.**  A list of channels and an
  address broke in the middle of a word.
- **Critics reviewed the drawing in rounds.**  Three agents reviewed
  the first drawing, one each for how it looks, whether a reader can
  answer the questions of the design from it, and how the stylesheet
  takes content it was not tuned for.  The shapes then got the
  proportions of the things they draw, and a role with many parts
  wraps its parts.

The room showed what the next plan needs.  The sound of a `Player` is
one `Sink`, so the sketch draws one speaker beside the screen.  The
`Sink` reports its channels, and a home theater has a speaker for each
one.  A role that reads through a second reference would draw them.
