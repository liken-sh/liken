# 19, A kind states its shape

Proposed and built on 2026-11-21.

This plan moves the choice of a part's shape from the sketch that
draws the part to the knowledge of the part's kind. It follows
[plan 17](17-the-shapes-of-a-sketch.md), which made a shape
a resource that an operator ships.

## The problem

A role of a sketch names the shape of its parts. The room of a
`Player` says that its receiver is an `amplifier`, so `media-operator`
ships the `Shape` of an amplifier, and `equipment-operator`, which owns
the `Receiver`, states nothing about how a receiver looks. A second
sketch that draws a `Receiver`, in the knowledge of another operator,
would name a shape again, and the two could differ.

The maintainer settled the position on 2026-11-21: a domain owns its
representation. The operator that owns a kind states how an object of
that kind is drawn, and every sketch that draws one agrees.

## The design

### A shape in a `KindKnowledge`

A `KindKnowledge` will take `spec.shape`, the name of the shape that
draws an object of the kind as a part of any sketch:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: receivers.equipment.liken.sh
spec:
  shape: amplifier
```

The name resolves as plan 17 resolves a role's shape: to the shape of
that name in the kind's own group first, here
`amplifier.equipment.liken.sh`, and then to the library's. The whole
name of a shape is also accepted.

### Which shape a part takes

`transect` draws a part in the first of these that is stated:

- the `shape` of the role, which stays as a way for one sketch to draw
  a part otherwise, such as a `ResourceClaim` as a `tag` in a room
  that does not show it;
- the `shape` of the knowledge of the part's kind;
- `box`.

A role whose reference names no kind reads a value, so only the role
can state its shape. An expected role with no part is drawn in the
shape of the kind that its reference names. A role of a catalog
pattern states its shape, because the catalog draws a `Pod` as a
`drum` only in one pattern.

A `shape` that is not loaded gives the `KindKnowledge` that states it
the condition `UnknownShape`, as plan 17 gives it for a role, and the
part is drawn as a `box`.

### The shapes page

Each shape on `/shapes` lists the kinds whose knowledge states it, with
a link to each kind's page, so a reader sees what each shape draws in
this cluster.

### The knowledge that moves

- `amplifier` moves from `media-operator/transect/shapes.yaml` to
  `equipment-operator/transect/shapes.yaml`, as
  `amplifier.equipment.liken.sh`, and the knowledge of `Receiver`
  states it.
- `display-operator`'s knowledge of `Display` states `screen`.
- `media-operator`'s knowledge of `Remote` states `handheld`.
- The roles of the `Player` state no shape for the display, the
  receiver, and the remote. They keep `tag` for the idle screen's
  controller, the message bus, and the `ResourceClaim`.

[Plan 14](../14-the-rig-of-a-telescope.md) is written after this one, so
the knowledge of each kind of the observatory states its shape, and
the roles of a train state none.

## What this plan leaves out

- **A kind with more than one drawing.** A kind states one shape. A
  kind whose objects look different from each other, such as a
  `Receiver` that is a soundbar, needs a rule that reads a field, and
  no case asks for it yet.
- **A shape for a kind of the core group**, such as a `Node`. The
  stock sets can state one, and none does.

## The alternatives

- **The role writes the whole name of another operator's shape**,
  `amplifier.equipment.liken.sh`. The shape ships with the right
  operator, and each sketch still chooses it, so two sketches can draw
  one kind in two ways.
- **A shape named for its kind**, found by a rule, with no field. A
  shape is named for the physical thing, and two kinds can share one,
  as a focuser and a rotator share a drawtube.

## How the work is proved

Unit tests cover the parsing of `spec.shape`, the order in which a part
takes its shape, an expected role with no part, and `UnknownShape` on
the kind's knowledge. The fixture's kinds state their shapes, and a
role of the fixture keeps one override. The golden pages of the sketch
and of `/shapes` cover the text. `make test` passes with the coverage
floor unchanged.

On a workstation, the page of each `Player` on a home cluster is
compared with screenshots from before the change, and no part changes
its shape.

## What the build changed

On 2026-11-21 a workstation compared the room and the signal path of
each `Player` of a home cluster with screenshots and pages taken before
the change, at 1680 pixels. With the numbers masked, the HTML of each
sketch was the same except its caption, so no part changed its shape or
its place. `/shapes` listed `amplifier.equipment.liken.sh` under the
file of `equipment-operator`, with the `Receiver` beside it. The build
differs from the proposal in these ways:

- **The kinds' shapes are found before the roles' shapes.**
  `Kinds::find_shapes` finds the shape that the knowledge of each kind
  states, and then the shape of each role, because a role that states
  no shape takes the shape of its kind. `KindKnowledge::draw_with`
  takes the shape of each kind as an argument, so a test of one
  document passes none. A part of a kind that the cluster does not
  serve has no knowledge in `Kinds`, so it takes its role's shape or
  `box`.
- **A role that names a shape that is not loaded is drawn as a `box`,**
  with `UnknownShape`, and not in the shape of its kind, because the
  role stated a shape.
- **The test that loads each operator's `transect/` directory checks
  the shape of each kind, and draws each part in its role's shape or
  `box`.** One directory does not say which kind each `KindKnowledge`
  describes, because a reference names a kind and a document is named
  for a plural. So a wire to a part that takes the shape of its kind is
  checked against that shape only when a cluster serves the kind. A
  fault then gives `NoPort` to the knowledge that states the wire.
- **The caption under a sketch and the lede of `/shapes` changed.** The
  caption said that the shapes are from the sketch's knowledge. It now
  says that a part's shape is from that knowledge or from the knowledge
  of the part's kind.
- **The `Player`'s sink and machine state no shape.** The proposal did
  not name them. The knowledge of a `Sink` and of a `Node` states no
  shape, so each is drawn as a `box`. `audio-operator` owns how a `Sink`
  looks, and the room takes the shape that it states when it states
  one.
- **The fixture's room states `crate` and its thermostat `disc`.** The
  shelf's `room` role states `box`, so the fixture draws one part in
  its kind's shape and one in its role's, and `/shapes` lists a kind
  under a library shape and under an author's shape.
