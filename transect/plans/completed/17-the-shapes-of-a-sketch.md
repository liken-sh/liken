# 17, The shapes of a sketch

Proposed and built on 2026-11-21.

This plan settles the open position of [00-design.md](../00-design.md)
on "which shapes the library has first, and the form in which an
author adds a shape". It defines how every shape of a sketch is
stated, named, sized, joined to its neighbors, and drawn in each
state, and it gives the shapes that plans
[14](../14-the-rig-of-a-telescope.md), [15](../15-the-elevations-of-a-player.md),
[16](../16-a-primary-with-replicas.md), and [18](../18-the-speakers-of-a-room.md)
ask for.

## The problem

[Plan 13](13-sketches.md) draws each of its shapes with CSS
written for that shape. `sketch-shapes.css` has pseudo-elements,
sizes, and special cases for `screen`, `box`, `speaker`, `handheld`,
and `tag`, and `sketch-states.css` repeats rules for some of them in
some states. A box with readings grows a window and a knob, and the
text of a screen takes the inks of glass, because rules name
`.sketch-box` and `.sketch-screen`. The shapes are an enum in
`elevation.rs`, so an author cannot add one.

The plans above ask for more shapes, wires, stacks, a speaker
on its side, and parts that touch across rows. Added as plan 13 added
its shapes, each new state would need a rule for each shape.

## The principle

A shape is a small drawing in its own units: an outline, a few pieces,
a region for text, the edges where it meets a neighbor, and the spans
where wires attach. It states geometry and the material of each piece,
and nothing else. `transect` draws every shape with the same code and
one stylesheet, so a state, a text rule, a contact, or a wire works the
same way on every shape, including a shape that an author adds. A shape
is named for the physical thing that it draws.

## The form

A shape is a document of a new kind, `Shape`, in
`transect.liken.sh/v1alpha1`. The library's shapes and an author's
shapes are written in the same form. This is the library's `box`:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: Shape
metadata:
  name: box
spec:
  summary: A piece of equipment on a shelf, such as a computer, a network switch, or a hub.
  size: {design: [360, 120], width: [160, 460]}
  outline: {round: 4}
  text: {inset: [12, 14, 22, 14]}
  pieces:
    - {name: front line, draw: line, at: {left: 0, right: 0, bottom: 8, height: 1}, view: [360, 1], path: "M0 0.5H360"}
    - {name: left foot, at: {left: 12, top: 100%, width: 18, height: 4}, round: [0, 0, 2, 2]}
    - {name: right foot, at: {right: 12, top: 100%, width: 18, height: 4}, round: [0, 0, 2, 2]}
    - {name: power light, draw: mark, at: {right: 16, bottom: 16, width: 6, height: 6}, round: 50%}
  ports: {left: [25%, 75%], right: [25%, 75%]}
```

This is an author's shape, in `observatory-operator/transect/shapes.yaml`:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: Shape
metadata:
  name: equatorial-mount.observatory.liken.sh
spec:
  summary: An equatorial mount from the side, a head with a saddle on top, a counterweight on a shaft, and a tripod under it.
  size: {design: [240, 280], width: [200, 280]}
  outline: {round: 8, at: {left: 0, right: 0, top: 14, bottom: 162}}
  text: {inset: [24, 16, 174, 16]}
  joins: [top]
  pieces:
    - {name: saddle, at: {left: 20, right: 20, top: 0, height: 15}, round: 2}
    - {name: shaft, behind: true, at: {right: -96, width: 100, top: 58, height: 8}}
    - {name: counterweight, at: {right: -132, width: 36, top: 36, height: 52}, round: 6}
    - {name: tripod, behind: true, at: {left: 50% - 150, bottom: 0, width: 300, height: 166},
       view: [300, 166], path: "M128 0H172L300 166H276L150 34L24 166H0Z"}
```

A length is in design units unless it says otherwise. The fields of
`spec`:

| field | required, default | what it states |
|---|---|---|
| `summary` | required | the physical thing, with examples from more than one kind of system |
| `size.design`, `size.width` | both required, except `design` with `follow` | the width and height in which every length is written; the narrowest and widest width of a part, in pixels |
| `size.share`, `size.follow` | none | the widest width as a share of the sketch; `follow: text` makes a part as wide as its text, with no `design` and no pieces |
| `axis` | half the design height | the height, from the top, of the line on which a row of parts aligns |
| `outline` | `draw: solid`, `round: 0`, the whole part | the body: its material, its corners, and its place when it does not fill the part |
| `text` | `inset: [12, 14, 12, 14]`, `align: start`, `fit: wrap` | the text region as top, right, bottom, and left; `start`, `center`, or `end`; `wrap` grows the part taller, `cut` keeps its proportions |
| `display` | none | `window` or `surface`, which "The content rules" describes |
| `pieces` | none, at most 12 | each needs `name` and `at`, two of `left`, `right`, `width` and two of `top`, `bottom`, `height` |
| a piece's `draw`, `round`, `behind` | `solid`, `0`, `false` | its material, its corners, and whether it is drawn under the outline |
| a piece's `view` and `path` | both or neither | SVG path data in a box of `view` units, stretched to the piece's place; without them the piece is a rectangle |
| a piece's `clear` | none | an edge; the text keeps clear of the piece on that edge |
| `joins` | none | the edges where the shape meets a neighbor with no gap |
| `ports` | none, so no wire | each edge where a wire attaches, with the span along it |
| `turns` | `false` | the shape lies on its side in an area wider than it is tall |

The materials are `solid` for the stuff of the body, `dark` for a
screen's body, which is dark in both color schemes, `glass` for a
window, `line` for an edge with no fill, and `mark` for a small filled
detail such as a button.

### The grammar

Every value that an author writes reaches the page only as a number
that the parser produced or a word from a fixed list:

- **A length** is `term (("+" | "-") term)*`, and the first term may
  have a sign. A term is digits with an optional fraction and no
  exponent, which is design units, or the same followed by `%`, which
  is a share of the part's width for `left`, `right`, and `width` and
  of its height for the others. A number is at most 10000 and a
  percentage at most 200%. A YAML number is a length of one term.
  `transect` writes `calc()` from the parsed terms, never from the text.
- **A radius** is one term of zero or more, or `H / V` for an
  elliptical corner. `round` is one radius for every corner, or a list
  of four in the order top-left, top-right, bottom-right, bottom-left,
  as CSS has them.
- **`view`** is two numbers from 0.001 to 10000. **`path`** starts with
  `M` or `m` and has only the commands `M`, `L`, `H`, `V`, `C`, `Q`,
  `A`, and `Z` in either case, digits, `.`, `-`, commas, and spaces, in
  at most 1000 characters.
- **`size.design`** is two numbers from 1 to 10000, **`size.width`** two
  numbers from 16 to 2000, the first not greater, **`size.share`** a
  percentage from 1% to 100%, **`axis`** a number from 0 to the design
  height, and **`text.inset`** four numbers from 0 to the design's
  larger side.
- **`ports`** maps an edge to `[from, to]`, two percentages of the
  edge's length from 0% to 100%, the first not greater.
- **The words** are `draw`, `display`, `align`, `fit`, `follow`, an edge
  in `joins` (each edge once), and an edge in `clear`. `clear` also
  needs the piece's offset from that edge and its size across it as
  single numbers of units. **`behind`** and **`turns`** are `true` or
  `false`.
- **A shape that follows its text** has no `size.design`, `axis`,
  `size.share`, or pieces, does not state `fit: cut`, and does not
  turn, because its body has no proportions: it is as tall as its text.
  **A shape that turns** states no `axis`, because on its side the
  axis would be a vertical line. Its axis is the middle of its height,
  standing and on its side.
- **Text**: `summary` is 1 to 200 characters and a piece's `name` 1 to
  40, with no control characters. `transect` escapes both.
- **The bound on a piece**: at the design size, a piece lies at most one
  design width past the left or right edge, at most one design height
  above the top, and at most 20 pixels below the bottom at the shape's
  widest width.

## Names, and where the shapes live

A library shape has a bare name, such as `box`. An author's shape is
named `<shape>.<group>`, as a `KindKnowledge` is named, with a group
that the author owns: `tube.observatory.liken.sh`. A role's `shape:
tube`, in the knowledge of a kind in `observatory.liken.sh`, resolves
to `tube.observatory.liken.sh` first and then to the library's `tube`.
A role may write the whole name. A kind of the core group has no
group, so its bare names resolve in the library only, and a stock set
for a core kind writes the whole name of a shape in a group that its
project owns.

A shape is in the library when 00-design.md names it there (a screen,
a box, a handheld, a speaker, a store, and a disc), when plan 13 added
it (`tag`), when a pattern of `transect`'s catalog draws it, or when two
shipped sets of knowledge use it. Every other shape ships with the one
author that uses it. When a second author needs it, the file moves
into the library with a bare name, and each author deletes its copy. A
size or an orientation that one shape covers gets no name of its own.

| shape | draws | home |
|---|---|---|
| `box` | a plain chassis with a power light; the default | library: 00-design.md |
| `screen` | a television or a monitor | library: 00-design.md |
| `speaker` | a loudspeaker; it turns | library: 00-design.md |
| `handheld` | a remote or a game controller | library: 00-design.md |
| `disc` | a filter wheel, a fan, a reel | library: 00-design.md |
| `drum` | a database, the design's store | library: 00-design.md and plan 16's catalog |
| `tag` | a thing the picture does not show | library: plan 13 |
| `socket` | a plate with a port, the way into a service | library: plan 16's catalog |
| `amplifier.media.liken.sh` | an A/V receiver, with a display window and a knob | `media-operator` |
| `camera.observatory.liken.sh` | a cooled camera from the side | `observatory-operator` |
| `tube.observatory.liken.sh` | a telescope's tube with its dew shield and rings | `observatory-operator` |
| `drawtube.observatory.liken.sh` | a focuser's or a rotator's drawtube | `observatory-operator` |
| `cover.observatory.liken.sh` | a dust cap or a flat panel, its text to its left | `observatory-operator` |
| `equatorial-mount.observatory.liken.sh` | a mount head on a tripod | `observatory-operator` |

The library is one file for each shape in
`transect-resources/src/shapes/library/`, which `shapes/library.rs`
embeds with `include_str!`. A test fails when a file is missing from
that list or does not load. An author's shapes ship in the operator's
`transect/` directory, under `--resources`.

## How a part is drawn

Each part is a CSS size container with `aspect-ratio` from
`size.design`, its outline and pieces placed absolutely, and its text
as one block. A design unit is `calc(100cqi / <design width>)`, so the
drawing scales with the part's width by one factor and the text does
not. A part starts at its widest and shrinks with its area, and with
`fit: wrap` it grows taller when its text needs room. A piece placed
against one edge keeps its size and its distance from that edge, and a
piece placed against two opposite edges stretches. A `path` is an
inline `svg` whose strokes do not scale. A part's inline style sets
only properties, such as `--u` and `--w-top`, so `shapes.css` in
`transect-map` can override a size.

A piece above the top edge gets that room as a margin above the part,
unless the top joins a neighbor. A piece past a side gets no room, so
the author leaves room in the sketch. A piece below the bottom stands
in the gap under its row. A clear piece moves that edge of the text
region to the piece's far side plus 8 units, so a speaker's cone keeps
its room and the cabinet grows taller when its text needs more.

`transect` turns a shape that states `turns` when its area spans more
columns than rows. Each placement, inset, port, join, and corner moves
a quarter turn, top to left and left to bottom, and a corner's radii
swap, so a drum's `50% / 22` turns into `22 / 50%`. A stack draws the
outline twice more behind the part, each 8 units up and to the right.

## Contact between parts

When the facing edges of two neighboring parts both join, there is no
gap between them, across a row and across rows, and the arrangement
needs no field for it.

- **In a row**, two neighbors whose facing sides join touch. A part's
  axis is its first baseline, a line of no height at `axis`, and a row
  with a joined pair aligns its parts' baselines, so a train's parts
  stand on the light's axis.
- **Across rows**, the grid's rows alternate with gap tracks. The track
  is zero when an area above and an area below share a column, and the
  upper area's parts join on their bottom and the lower area's on their
  top. A `tube` joins on all four edges and a mount on its top, so the
  guide train, the main train, and the mount touch, and a tube above a
  `box` keeps the gap.
- **On an edge of a row that a part joins**, the other parts take a
  negative block margin, so they add nothing to the row's height there,
  and the joining part's edge is the row's edge. A taller part stands
  past it.

A tube's rings are inside its box, under its body, so they meet the
tube or the saddle below at every width with no computed length.
Contact is a fact of the wide layout: when the areas gather into lines
below 640 pixels, nothing joins, and every part keeps its gap.

## Ports and wires

A port is a span along an edge, as two percentages of its length. The
wires that meet one edge of a part spread evenly across its span, in
the order of their other ends along that edge. A wire leaves its port
at a right angle to the edge. When the part is narrower than its area,
the wire runs straight on to the area's edge, and between two areas it
bends at right angles halfway, so it meets the port wherever the part
stands in its area. A wire to a role with several parts ends on a line
along the area's facing edge that spans their ports, with a short wire
from the line to each port. Wires are drawn over the parts, and the
ports on the top and right edges of a stack move out to the last copy,
so an arrow into a stack ends on the stack's outer edge. A wire to a
shape with no port is a fault of the elevation that states it. Plans
15 and 16 own the wire format.

## The states

One stylesheet, `shapes.css`, draws the states of every shape. A state
sets a few properties on the part, and each material reads them:

| state | outline | `solid` and `dark` fill | pieces | text |
|---|---|---|---|---|
| plain | `--rule-strong` | the body | as drawn | page inks, or glass inks on a surface |
| a name with no object, or a warning | `--warm`, 2 px | the body | as drawn | mark before the label |
| failing | `--warm`, 3 px | tinted with `--warm` | as drawn | filled mark, label in `--warm` |
| at rest | `--rule` | the color of rest | edges in `--rule` | name muted |
| a value with no object | `--rule-strong` | none | glass has no fill | page inks |
| an expected role with no part | dashed | none | dashed, no fill | muted |
| expected in another state | dashed, in `--rule` | none | dashed, no fill | muted |

"No fill" is drawn in the frame's own color, so a piece from a
neighbor still ends at the part's edge. In forced colors the browser
drops backgrounds and does not force an SVG's fill, so `shapes.css`
draws each material in system colors with `forced-color-adjust: none`:
edges in `CanvasText`, fills in `Canvas`, marks filled in `CanvasText`,
and each state as a real border, 3 pixels for a warning, a double
border for a failure, `GrayText` at rest, and dashed when empty.

## The content rules

The text of a part is the same block on every shape: the label, the
state word for a screen reader, the name with its link, the reason,
the readings, what the page's object shows, the note, and the field it
was read from. A shape decides where the block goes and these rules:

- **`fit: cut`** keeps the proportions. A line that is too long is cut
  with an ellipsis, and its `title` has the whole text.
- **`display: window`** shows the readings and what the page's object
  shows in a pane of glass in the text, with the values in the main ink
  of glass. The amplifier has a window.
- **`display: surface`** makes the text region a display: the text
  takes the inks of glass, the name is one size larger, and what the
  page's object shows is the title in the middle, at most two lines.
  The screen is a surface.
- **No display.** The readings are one muted line that wraps between
  readings. A `box` has no display, so its readings never look lit.

## The sketch at narrow widths

Below 760 pixels `sketch.css` keeps narrowing the grid's gaps, and its
rules for a part's padding, a box's knob, and a screen's inset go away,
because insets scale. Below 640 pixels the areas gather into lines, and
`shapes.css` gives a part with `share` a line of its own at the full
width, so a screen keeps its second line. An area whose part follows its
text keeps that width, and a row on one axis takes a line of its own.
These rules read `.part.share` and `.part.follow`, which come from
`size`.

## Where an author's shape ships

`transect` leaves a `Shape` out, with a condition, when:

- `Invalid`: `spec` breaks the grammar, has a field that the form does
  not have, such as a color, or has more than 12 pieces, or the name is
  not `<shape>.<group>` for an author's file or a bare name for the
  library's.
- `Duplicate`: a file whose path sorts first has a shape of that name.

The binary sorts each `Shape` into a set beside the `KindKnowledge`.
`Kinds::resolve` takes that set and replaces each role's shape name
with the resolved definition, so `Sketch::build` reads the shape from
the role and `transect-map` needs no new handle. A role whose shape
resolves to nothing is drawn as a `box`, and its `KindKnowledge` gets
the condition `UnknownShape`, which names the role and the shape. The
document stays, so one missing shape does not hide the rest of a
kind's knowledge. `Kinds::resolve` skips a document whose kind the
cluster does not serve, with `NotServed`, so its shapes are checked
when the cluster serves the kind.

## The specimen page

`/shapes` draws every shape that `transect` has loaded: the library's
by name, then each author's, under the file it came from. Each shape
shows its name, its summary, its joins, its ports, and its display, and
is drawn in every state with the same texts on every shape: a part
with readings, a long name, what the page's object shows, each state of
the table above, a stack, and, for a shape that turns, a turned part.
The first drawing marks each port's span. Each cell leaves the room
that a piece past a side or below the bottom needs. A last section
lists each `Shape` that was left out, with its condition.

`/kinds` links to `/shapes` in its head, every sketch's caption names
`/shapes`, `/resources` links each `Shape` to its section, and search
lists the page. The fixture gets an author's shape, and a golden page,
`shapes.txt`, covers it.

## The existing shapes

The shapes of plan 13 become files of the library with the geometry of
today's page at desktop width: the 16:9 glass and stand of a screen, a
speaker's 96-unit cone and tweeter above the line of its source, and a
handheld's pad and buttons. The knob and window of a box with readings
become `amplifier.media.liken.sh`, and the `Player`'s receiver role
names `amplifier`, so the room looks the same. `Shape` in
`elevation.rs` becomes a name. `sketch-shapes.css` and
`sketch-states.css` are deleted, with every class `sketch-<shape>`.
The golden `sketch.txt` changes in its caption, which names `/shapes`.

These details of today's page change. A tag's name is 13 pixels, as on
every shape, in place of 12. A handheld's readings wrap in place of
being cut. A knob scales with its part, so it is smaller on a narrow
amplifier.

## What this plan leaves out

The wire format (plans 15 and 16), which roles nest, repeat, or stand
as a stage (plans 14 and 15), a size that a role states, and a rule
line inside the text, which plan 16 draws over a drum's claim. At the
width of a phone the main train is wider than the frame, which scrolls
sideways, as the ring of boxes does now.

## The alternatives

- **CSS for each shape, or CSS that an author ships.** Each state needs
  a rule for each shape, and an author's CSS can draw its own states.
- **SVG with named regions that the author ships**, which plan 13 set
  aside because the author draws the states. **SVG stretched in a unit
  box** turns a knob and a cone into ellipses when a part grows.
- **A length that reaches across the gap**, such as `gap + 18`. The
  rings met the saddle at one width and stopped short at others,
  because other parts set the height of the tube's row.
- **A script that lays out the geometry.** The browser lays out text,
  aspect ratios, baselines, and container units already.
- **Groups of shapes**, a label with one member in one group, and **one
  namespace of bare names**, in which two operators' `drum`s collide.
- **A piece drawn only when a part has readings.** It made every box
  with readings an amplifier, so a guider and a GPS got a knob.

## How the work is proved

Unit tests cover the grammar of each field, each fault and its
condition, `UnknownShape` and `NotServed`, name resolution, lengths,
turning with elliptical corners, `clear`, and the room above a piece.
A table test draws every library shape in every state and checks that
no class names a shape. The golden pages cover `/shapes` and the
sketch, and `make test` passes with the coverage floor unchanged.

A prototype in Python drew this form: the library, an author's shapes
for a telescope and a receiver, and a reviewer's router. It refused
each hostile definition of an adversarial review, matched today's room
at desktop and phone width, turned the center speaker, and drew a rig
whose rings met the tube and the saddle at 1272, 1000, 800, 600, and
420 pixels. On a workstation, each `Player`'s page is compared with
screenshots taken before the change, and `/shapes` is read on a phone
and in forced colors.

## What the build changed

On 2026-11-21 a workstation compared the page of each `Player` of a
home cluster with screenshots taken before the change, at 1680 and 420
pixels, in both color schemes and in forced colors, and read `/shapes`
at both widths. The build differs from the proposal in these ways:

- **The box and the amplifier have a design of 460 by 153.** The
  proposal wrote 360 by 120. A design as wide as the widest part makes
  a design unit one pixel at that width, so a receiver at desktop width
  has today's feet, knob, and insets. With 360, each detail was 28%
  larger.
- **Nothing joins below 640 pixels.** The proposal gave an area that
  joins the area before it a negative top margin when the areas gather
  into lines. Contact needs the grid, so `sketch.css` applies it only
  in the wide layout, and the section above now says so.
- **The negative block margin on an edge of a row is not built.** Every
  part of one area has the shape of its role, so in this build a row of
  parts never mixes a part that joins with one that does not. The rule
  comes with the arrangements of plans 14 and 15, which put several
  roles on one axis. A role whose shape joins on its left and its right
  draws its parts touching and on their axes, which `sketch-abutting`
  does.
- **The source line stands at the bottom inset that the shape states.**
  In the flow of the text, under a piece that the text keeps clear of,
  the line that names the field a part was read from made the speaker
  and the remote taller than today by its own height. It is now
  absolutely placed at the stated inset, under the cone and the
  buttons, as it is today, and the text region keeps a line of room
  for it.
- **A part on a line of its own grows below 640 pixels.** At 420
  pixels the screen is 290 pixels wide, and `fit: cut` cut the title of
  the play. A part with `share` takes a line of its own in the narrow
  layout, and there its text flows and makes it taller, as today's
  screen does.
- **A shape that follows its text has no axis, share, cut, or turn,
  and a shape that turns has no axis.** A shape that follows its text
  has no design height, so with `fit: cut` its text left the part no
  height at all. The grammar above now states both rules.
- **A gap track closes only when every pair across it joins.** A gap
  track runs across every column of the grid, so it cannot close for
  one pair of areas and stay open for the pair beside it. It closes
  when every pair of areas that face each other across it joins, and
  a column with an empty side or with one area across both rows does
  not keep it open. When one pair joins and another does not, both
  pairs keep the gap. A per-column gap would need each area to draw
  its own gap, which the grid's rows then could not align.
- **Edges are listed left, right, top, and bottom,** in a shape's
  joins and ports and on `/shapes`, so a reader meets the left edge
  before the right.
- **The text of a part is in `text.css`.** `shapes.css` draws the
  materials and every state, and `text.css` beside it lays out the text
  block and the displays, so each stylesheet stays under 500 lines.
  `specimen.css` lays out `/shapes`.
- **`/kinds` links to `/shapes` in the text of its card,** because the
  head of `/kinds` is its breadcrumbs.
- **A role's shape is found in a step of its own.** `Kinds::resolve`
  keeps its arguments, because most of its callers draw no sketch.
  The parser keeps the name that a role writes, and
  `Kinds::find_shapes`, which the snapshot of a page calls after
  `resolve`, finds each definition and posts `UnknownShape`, of the
  condition type `ShapesFound`. The test that loads each operator's
  `transect/` directory also fails when a role names a shape that
  neither the directory nor the library has.
- **The text block of a part belongs to `shapes`.** Its types, its
  template `shapes/parts.html`, and `text.css` are in the `shapes`
  module of `transect-map`, with classes named `shape-*`, so the sketch
  uses the shapes and `/shapes` uses nothing of the sketch.
- **`/shapes` draws a warning and a name with no object in two cells,**
  each state of the table has a cell, and a shape with `share` has a
  cell wider by its share, so it is drawn at its widest width.

These details of the page differ from today and the proposal did not
list them. Each follows from one rule for every shape:

- The title on a screen stands in the middle of the glass under the
  readings, about 18 pixels lower than today, because the source line
  no longer takes a share of the free room.
- A tag that is a value has its outline in `--rule-strong`, as every
  value has, in place of `--rule`.
- A screen that is a value draws its stand as an outline, and an empty
  role draws its feet, its knob, and its buttons dashed, as the table
  says for pieces.
- The dome of a tweeter, the dust cap of a cone, and the center of a
  pad are marks in `--rule-strong`, as every mark is, in place of
  `--rule`.
- An amplifier's window is 5 pixels wider, because the text keeps 8
  units from the knob, as from every clear piece, in place of 13
  pixels.
- A box narrower than its widest width has smaller insets, because
  insets scale.
