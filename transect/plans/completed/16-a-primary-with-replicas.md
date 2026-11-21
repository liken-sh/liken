# 16, A primary with replicas

Proposed on 2026-11-21.  Built on 2026-11-21.  The switchover drill
of "How the work is proved" has not run.

Revised on 2026-11-21 for plans 14, 15, 17, and 19 as built.  The
decisions and the evidence are as they were.  These statements changed:

- A role of `pattern` finds its parts through the `Fill` of plan 14,
  so `Plan::build` draws it with the same code as every other role.
  A derived role adds one first step, which reads the parts that the
  pattern's module derived, from the `Board`.
- Each role of the catalog states a `label`, and each `Service` role
  states its readings.  A claim is a reading, `Claim`.
- Every fault of this plan is in the document itself, so each gives
  `Invalid` and none gives `Unresolved`.
- The catalog's shapes follow plan 19's order, and the plan says why
  the roles state them.
- The plan names the files where the code is now: `stored` in
  `pages/object/find.rs`, `readings` in `pages/known/readings.rs`,
  `render` in `knowledge/referenced.rs`, and `reads_fields`, which
  returns a `bool` and needs no new case.
- The head takes its author from each elevation, and an elevation URL
  of a kind with only the pattern's elevations is found.
- The place of no replica is drawn as plan 14's `Elsewhere` state is,
  with the role's label and the reason that the pattern's module
  gives.  A run that meets such a place is dashed in `--rule`, and a
  dimmed part is drawn at rest.
- The middle column keeps its width under plan 14's rule as built,
  with a gap track on each side.  A wire to a role with several parts
  ends on a bar, as plan 15 built it.
- Plan 17's build drew no stack, so this plan builds it from the
  shape's `outline`, with no new field of a `Shape`.
- The fixture's store is a kind that the fixture invents.

This plan builds the first pattern of `transect`'s catalog, as "Who
draws a pattern" in [00-design.md](../00-design.md) describes it.  A
project states which of its parts has which role, and `transect` draws
the pattern the same way for every project that has it.  The first
pattern is a primary with replicas, for a `Cluster` of CloudNativePG
and a `Redis` of `redis-operator`.

## The problem

The page of a `Cluster` draws the ring of boxes: the primary `Pod`,
the `Service` for writes, and the `Service` for reads, each as the
same box.  It does not draw the replicas, so the reader cannot see
which pods follow the primary or where each one runs.  The proof of
concept reads `primary: status.currentPrimary`, and only the loss page
uses it.  No knowledge names the model, so nothing tells the reader to
bring it, and `transect` checks nothing that the model expects.

Most stores have no replica.  On 2026-11-21 a home cluster had 16
`Cluster`s and 8 `Redis`es, and 14 of the `Cluster`s and 5 of the
`Redis`es had one instance.  The drawing has to read correctly for
them too.

## The design

### A pattern in a `KindKnowledge`

A `KindKnowledge` will take a field `spec.pattern`.  It names one
pattern of the catalog, and its `roles` fill the roles that the
pattern asks the kind to fill.  Each role names an entry of the
document's `references` with `reference`, as a role of `spec.roles`
does, and has no other field, because the catalog states each role's
shape, label, and readings.  A role of `pattern` takes neither
`namedBy` nor `through` of
[plan 14](14-the-rig-of-a-telescope.md), so the entry that
it names is always in the same document:

```yaml
pattern:
  name: primary-with-replicas
  roles:
    instances:     {reference: "status.instanceNames[]"}
    primary:       {reference: status.currentPrimary}
    target:        {reference: status.targetPrimary}
    write-service: {reference: status.writeService}
    read-service:  {reference: "{name}-ro"}
```

The roles that `primary-with-replicas` asks a kind to fill:

- `instances` (required): each `Pod` that is an instance, the primary
  included.
- `primary` (required): the primary `Pod`.  The top-level `primary` of
  the proof of concept moves here, and the loss page reads it from
  here.
- `target` (optional): the `Pod` that the operator is making the
  primary.
- `write-service` and `read-service` (optional): the `Service` for
  writes, and the `Service` that selects the replicas for reads.

The catalog derives the other roles.  The replicas are the instances
other than the primary, and the promoter is the kind's controller,
which `controller.image` finds.

### How a role of the pattern finds its parts

A role of `spec.roles` finds its parts through its `Fill`, in
`knowledge/role/fill.rs`.  A `Fill` is a first step, which reads the
values of a reference (`reference`) or the objects that name the
page's object (`namedBy`), and one more reference to follow
(`through`).  `fill::found`, in `transect-map`'s `sketch/fill.rs`,
reads the parts of a role, and `Plan::build`, in `sketch/plan.rs`,
draws every elevation from them.  A role of the catalog will use the
same `Fill`, so its parts take the same path through the page:

- **A role that the kind fills** gets the `Fill` of the entry that its
  `reference` names, `First::Reference` with the entry's path or
  template, as a role of `spec.roles` that writes `reference` does.
- **A derived role** gets one more kind of first step, which names the
  role.  The pattern's module has one function for each `derive`, and
  the function returns the role's parts, or no part and the reason.
  The page calls each function when it builds the `Board`, and
  `fill::found` reads the result from a field of the `Board` beside
  `named_by`, so the rest of `Plan::build` does not change.
- **`replicas`**, with `derive: instances-but-primary`, is each `Pod`
  of `instances` that `primary` does not name.  The expectations call
  the same function, so the drawing and the expectations agree on which
  `Pod` is a replica.  With no replica, the function returns the reason
  "`<instances reference>` lists only the primary".
- **`promoter`**, with `derive: controller`, is the object that the
  page's object is `controlled by`.  `controller` in `transect-map`'s
  `map/workload/mod.rs` finds that object from `controller.image` for
  the card's "Described by", and the function calls it.
- **An optional role that the kind does not fill** has no part.  Its
  place says why, in the way that a role whose fill `Kinds::resolve`
  could not confirm shows `Role::unresolved`.

The expectations are code of the pattern's module, and they read the
same instances, primary, and `Service`s.

### A reference by a name template

An operator often names a `Service` for its object and records the
name in no field.  So an entry of `references` will take `name` in
place of `path`: a template of the referenced object's name, with
`{name}` and `{namespace}` for the page's object, in the syntax and
through the code of `referencedBy`, the function `render` in
`knowledge/referenced.rs`.

```yaml
references:
  - {name: "{name}-ro", kind: Service, label: reads from the replicas through}
```

The entry refers to the one object of `kind` with that name, in the
page's object's namespace unless the kind is cluster-scoped.  It draws
the same relation, box, and edge as an entry with a `path`, and a
role's `reference` names it by its template.  A path never contains a
brace, so a role's `reference` matches an entry's `path` or its `name`
without ambiguity.  `relations` in `knowledge/references.rs` gives the
entry one relation, whose `path` is the template, so `fill::found`
matches it as it matches a path.  The check in
`snapshot/matched/kinds.rs`, which reports a path of `references` that
no object of the kind has, skips an entry with `name`, because a
template reads no field.

### The faults

Each fault below shows in the document alone, because a role of
`pattern` names an entry of the same document, and the catalog is in
the binary.  Each one gives `Invalid` when the document is read.  None
gives `Unresolved`, the condition that plans 14 and 15 give a fault
that only the knowledge of another kind shows.

`transect` will leave out a document, with an `Invalid` condition that
names the fault, when:

- `pattern.name` is not a pattern of the catalog;
- `pattern.roles` has a role that the pattern does not ask a kind to
  fill, lacks a required one, or gives a role a field other than
  `reference`;
- a role's `reference` names no entry of `references`, or names an
  entry whose `kind` is not the kind that the pattern's role takes,
  `Pod` or `Service`;
- `instances` names an entry whose path has no `[]`, or an entry with
  `name`, so it cannot read a list;
- an entry of `references` has both `path` and `name`, or neither, or
  has `name` with no `kind`, no `{name}`, or a brace that is not part
  of `{name}` or `{namespace}`;
- a role of `spec.roles` has both `stack` and `elevation`, because
  plan 14 draws each part of a role with `elevation` in full;
- the document has the top-level `primary`.  The message says that
  `primary` is now a role of `pattern`.

A cluster's own file that still has the top-level `primary` loses the
whole document until it changes, so the change of those files goes in
the same deploy as this plan.  `KindKnowledge::reads_fields` returns
`true` when `references` is not empty.  Each role of `pattern` names an
entry of `references`, so the trimmed watch already keeps the whole
object of a kind with a pattern, and the function needs no new case.
Its case for `primary` goes with the field.  `Snapshot::primaries`, in
`snapshot/known.rs`, gives the loss page the primary of each store, and
it will read the entry that the role `primary` names.

### Where the catalog is

The catalog is a directory of YAML files in `transect-resources`,
`src/catalog/`, one file for each pattern, which the binary embeds
with `include_str!`, as `shapes/library.rs` embeds the library's
shapes.  A fault in a file of the catalog is a fault of the binary, so
a test fails when a file does not load.  A file has the form of
[plan 15](15-the-elevations-of-a-player.md): `roles` states
each role once, and each elevation has a `name`, a `question`, a
`sketch`, and `wires`.  One parser, `elevation::read` in
`knowledge/elevation.rs`, reads the elevations of a catalog pattern and
of a `KindKnowledge`, against the roles that each states.  One function
reads the fields that a role of either has: `label`, `shape`,
`expected`, `reads`, `shows`, and `stack`.  A role of a `KindKnowledge`
then reads its `Fill` from `reference` or `namedBy`, and `through`.  A
catalog role has neither `reference` nor `namedBy`, as plan 14 states.
It has `fill`, which says that the kind's `pattern` fills it and
whether that is `required` or `optional`, or `derive`.  A
`KindKnowledge` cannot state `fill` or `derive`.

The roles of the catalog and the roles of a kind are separate, so a
role of `spec.roles` can have the name of a role of its pattern.
`Plan::build` reads the roles of the elevation's author.

A part takes the shape of its role, then the shape that the knowledge
of its kind states, then `box`, by
[plan 19](19-a-kind-states-its-shape.md)'s rule.  The
catalog states `drum` and `socket` on its roles, and that is correct
here.  A `Pod` and a `Service` are core kinds whose knowledge no
operator ships, so no kind's shape applies, and a `Pod` is an instance
of a store only because of the role that it fills.

A catalog role writes `expected: true`.  Plan 14's
`{while: <condition type>}` names a condition of one kind, and a
pattern of the catalog describes every kind that fills it, so the test
of the catalog fails on a `while`.  Each catalog role states plan 14's
`label`, so a part's label reads the same in every project that has the
pattern.  The labels of a kind's `references` stay on its other pages.

A catalog pattern is its YAML file, for the roles, the sketch, and the
wires, and a Rust module of the same name, `catalog/primary_with_replicas.rs`,
for its derived roles and its expectations.  `derive` names a function
of that module.  This is the one place where data names code, because
an expectation and a derived role are logic, and the catalog is
`transect`'s own, so no author outside the binary can name a function.

```yaml
name: primary-with-replicas
title: primary with replicas
roles:
  instances:     {fill: required, label: instance}
  primary:       {fill: required, label: primary, shape: drum, expected: true, reads: [Ready, Node, Age, Claim]}
  target:        {fill: optional, label: the operator's target}
  write-service: {fill: optional, label: writes through, shape: socket, expected: true, reads: [Ports, Pods]}
  read-service:  {fill: optional, label: reads through, shape: socket, expected: true, reads: [Ports, Pods]}
  replicas:      {derive: instances-but-primary, label: replica, shape: drum, expected: true, stack: true, reads: [Ready, Node, Age, Claim]}
  promoter:      {derive: controller, label: promoted by, shape: tag, expected: true}
elevations:
  - name: the store
    question: Where do writes and reads go, and which instances follow the primary?
    sketch: |
      write-service  write-service  .  read-service  read-service
      primary        primary        .  replicas      replicas
      promoter       promoter       .  .             .
    wires:
      - {from: write-service, to: primary, carries: writes}
      - {from: read-service, to: replicas, carries: reads}
      - {from: primary, to: replicas, carries: streams}
```

`instances` and `target` are placed by no elevation.  They are
knowledge that the expectations and an agent read.  The middle column
of the sketch has only `.` cells.  Plan 14's rule, as `sketch/tracks.rs`
builds it, takes a column's width away only when every cell in it
belongs to a role that the page leaves out, so a column of `.` keeps
its width, with a gap track open on each side of it.  In a sketch with
wires each of those gaps is 7.5% of the sketch's width, from 64 to 104
pixels, and the wire that streams crosses both gaps and the column.
Each wire joins the facing edges of its two roles' areas, which
`Grid::facing` finds, so the wires from the `Service`s are vertical and
the wire that streams is horizontal.  The gap between two rows stays
`--sketch-gap`, 28 pixels, because plan 15's build widened only the
gaps between columns.  `wires.js` puts the words of a wire with no
horizontal segment beside its first segment.  `drum` and `socket` are
shapes of the library, in
`crates/transect-resources/src/shapes/library/`.  The promoter is a
`tag`, because the operator is in the cluster and not part of the
store.  A `tag` has no port, and no wire meets the promoter.

The catalog's elevations follow the kind's own, in plan 15's tabs and
URLs, with `transect` as their author.  When an elevation of the
catalog has the slug of one of the kind's, its URL gets the suffix
`-by-transect`, as plan 15 states.  The page of a `Cluster`, whose
kind states no elevation, opens on "the store", at
`.../elevations/the-store`.  The head of the drawing also names the
pattern, "primary with replicas".  `sketch::choose`, in
`sketch/head.rs`, takes one author for all the elevations of a page.
It will take an author with each elevation, so the head names the
`KindKnowledge` or `transect` for the current one.  `known_page`, in
`pages/known.rs`, finds no page for an elevation URL of an object whose
kind states no elevation, and it will count the elevations of the
kind's pattern too.

### A `Pod` as a part

A sketch draws a part from the stores that `stored` reads, in
`pages/object/find.rs`, which `pages::object` exports.  `stored`
returns nothing for a `Pod`, whose kind is `ReadFrom::OwnPage` in
`transect-map`'s `objects/kinds.rs`, so a sketch draws a `Pod` plainly
and with no readings.  It returns a `Service` with only its status,
and `api_version` empty, so a sketch draws a `Service` with no
readings.  The `Board`'s `standing` and `readings` are answered by
`standing` in `pages/known.rs` and by `readings` in
`pages/known/readings.rs`.  Both will read a `Pod` from
`Snapshot::pods`, and `readings` will read a `Service` from
`Snapshot::services`:

- **A `Pod`'s standing.**  `Missing` when no `Pod` has the name.
  Otherwise the level of
  `Health::from_reasons(health::pod_reasons(pod, now))`, the rules of
  `transect-observe` that mark a `Pod` on every page:
  `Standing::Failing` for `Failing`, `Standing::Warning` for `Warning`,
  and `Standing::Plain` for a lower level, with the first reason as the
  part's line.  `health::pod_reasons` is `reasons` in
  `transect-observe/src/health/pods.rs`.
- **A `Pod`'s readings**, under the names that `kubectl get pods -o wide`
  prints, because a `Pod` has no printer columns of a CRD: `Ready`
  from the counts of ready containers, `Node` from `spec.nodeName`,
  and `Age` from `metadata.creationTimestamp`, in the format of `age`
  in `knowledge/age.rs`.
- **A `Pod`'s claim**, as one more reading, `Claim`, which a role names
  in `reads` as it names the others.  Its value is each claim of
  `spec.volumes[].persistentVolumeClaim` with its
  `status.capacity.storage` from `Paged::claims`, and the reading's
  `from` links to the claim's page.  So plan 17's text block draws it
  with no new line.
- **A `Service`'s readings**: `Ports` from `spec.ports`, as
  `kubectl get services` prints `PORT(S)`, and `Pods`, the pods behind
  the ready endpoints of its `EndpointSlice`s in
  `Snapshot::endpoint_slices`, which the expectations on writes and
  reads also read.

### A stack

A role of `spec.roles` takes `stack: true`, in the form of plan 15, so
any author can state it, because "Stack what is alike" is a rule of
every view.  The parts of the role that are not marked are drawn as
one part with their count, the name of each part, and each reading
with its values, such as the machines.  The reader opens the stack to
see each part.  A stack of one part has no count.

A part that is marked stands apart from the stack, as a part of its
own beside it.  A part is marked by its health, as on every page, or
by an unmet expectation that names it.  An unmet expectation draws
the part in plan 17's warning state, with the expectation's words as
its reason, unless its health is failing.  So a replica that is not
ready, one that restarted within the hour, and a name in `instances`
whose `Pod` is gone each stand apart, and the part shows why.

### What the shape library gives this pattern

[Plan 17](17-the-shapes-of-a-sketch.md) defines `drum` and
`socket` in the library, in `drum.yaml` and `socket.yaml`, because a
pattern of the catalog draws them, and this plan defines no shape.

- **A `drum`**, a database, for an instance.  Its text is the same
  block as on every shape: the object's name and its readings, with
  the claim it stores on and the claim's size as the reading `Claim`.
  Its ports are on its top edge, from 30% to 70%, and on its left and
  right edges, from 45% to 75%.  The wires of "the store" use the top
  port and the port on the side that faces the other drum.
- **A `socket`**, a plate with a port, for a `Service` where traffic
  enters.  It shows the `Service`'s name and its readings, `Ports` and
  `Pods`.  Its only port is on its bottom edge, from 47% to 53%, under
  the piece that draws the port, so a wire meets a socket only from
  below.
- **A stack**, which `transect` draws on any shape: the outline twice
  more behind the part, each 8 units up and to the right.  Plan 17
  describes the stack, and its build drew none, so this plan builds it,
  and `/shapes` gets a cell with a stack of each shape.  The copies are
  the shape's `outline`, with its `draw`, `round`, and `at`, and no
  piece.  So the form of a `Shape` needs no new field, and no code names
  a shape.  A stacked part keeps its copies inside its area.  It keeps
  their 16 units as room above it, as `Shape::above` keeps room for a
  piece above the top edge, and its width is 16 units less.  The server
  writes the port marks of the top and right edges on the last copy,
  and `wires.js` measures the marks, so the script does not change.
  When the stack is the only part of its role, the arrow of the wire
  for reads ends on the stack's outer edge, and the wire that streams
  enters the front part's left edge.  When a part stands apart beside
  the stack, each wire ends on a bar along the area's edge, with a short
  line to the port of each part, as plan 15 built it.
- **The states of plan 17** on each, with a dashed outline for an
  expected role with no part.

A port is a span along an edge, so the wires that meet one edge spread
across it.  `Kinds::find_shapes` leaves out a wire to a shape with no
port on the facing edge and gives the document `NoPort`, of the type
`ShapesFound`.  The catalog is in the binary, so a test of the catalog
fails on `NoPort`, as the test that loads each first-party `transect/`
directory does.  "The store" passes that check with the ports above.

### What the page draws

Every part draws the states of plan 17, and every wire is drawn as
plan 15 draws it.  This pattern adds these:

- **The replicas** are a stack of drums, with how many are ready, the
  machines, and the claims.
- **No replica.**  `instances` names only the primary, so the function
  of `replicas` returns no part and its reason.  The replicas' place is
  drawn as plan 14's state `Elsewhere` is, a dashed drum in the color
  of rest, because the role is empty and no part is missing.  Its words
  are the role's label and the function's reason: "no replica", and
  "status.instanceNames[] lists only the primary".  This is a role with
  no part, and it is not a fault.
  The read `Service` and the promoter are drawn at rest, plan 17's
  dimmed state, because nothing is in their role now.  The `Service`
  reads "selects the replicas, and there is none", and the promoter
  reads "no replica to promote".  `sketch/wires.rs` draws a run that
  meets an empty place dashed, and its `Presence` does not tell an
  `Elsewhere` place from an `Empty` one.  This plan draws a run that
  meets an `Elsewhere` place dashed in `--rule`, as the outline of the
  place is, in every sketch.  So the wire that streams and the wire for
  reads are dimmed, and they do not read as missing.
- **A role that the kind does not fill.**  When `write-service` or
  `read-service` is absent, the role keeps its dashed place with "the
  `KindKnowledge` names no `Service` for writes".
- **A wire** is also marked when the expectation about the part at its
  end is unmet.  `sketch/wires.rs` will give a run that state beside
  solid and dashed, drawn in `--warm`.
- **Below 640 pixels** no wire is drawn, as plan 15 built it, and the
  list of wires shows in place of the drawing.

Under the drawing are the list "What the pattern expects" and the
caption that `Sketch::caption` writes.  For an elevation of the catalog
the caption names the source of each kind of fact: the parts and their
states from the Kubernetes API, the roles from the `KindKnowledge`, and
the places, shapes, and expectations from `transect`'s catalog.

### The expectations

A line that is met is drawn plainly, with a check.  A line that is
unmet is marked in `--warm`, names the part, and marks that part in
the drawing.  A line with no signal says what is missing.  A line that
does not apply, because the store has no replica, is dimmed and says
why.  Each line shows its source when the reader points at it.

- **The primary is ready, and it is the operator's target.**  The
  `Pod` of `primary` is one of `instances`, its `Ready` condition is
  `True`, and `target` names the same `Pod`.  Unmet lines read "the
  primary is `<a>`, and the operator's target is `<b>`", "`<a>` is not
  one of the instances", or "`<a>` is not ready".  A target that
  differs is unmet while the operator moves the primary, so the reader
  sees the move.  Without `target`, the line checks the rest and says
  that the knowledge names no target.
- **Writes reach only the primary.**  The ready endpoints of the
  `EndpointSlice`s of `write-service` are the primary `Pod` and no
  other.  Unmet lines read "`<Service>` has no ready endpoint" or
  "`<Service>` also reaches `<pod>`".  Without `write-service`, the
  line has no signal.
- **Each replica is ready.**  The `Ready` condition of each replica's
  `Pod`, as "2 of 2".  It does not apply with no replica.
- **Reads reach only replicas.**  The ready endpoints of
  `read-service` are replicas, and there is at least one.  It does not
  apply with no replica, and it has no signal without `read-service`.

### The knowledge of each kind

The knowledge of CloudNativePG is stock, in
`transect/examples/cloudnative-pg/kinds.yaml`:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: clusters.postgresql.cnpg.io
spec:
  summary: A PostgreSQL primary and the replicas that stream from it.
  healthy: {condition: Ready}
  references:
    - {path: status.currentPrimary, kind: Pod, label: primary is}
    - {path: "status.instanceNames[]", kind: Pod, label: instance}
    - {path: status.targetPrimary, kind: Pod, label: the operator's target is}
    - {path: status.writeService, kind: Service, label: writes through}
    - {path: status.readService, kind: Service, label: reads through}
    # status.readService names the `-r` Service, which selects every
    # instance, the primary included.  The `-ro` Service selects only
    # the replicas, and no field of the status names it.
    - {name: "{name}-ro", kind: Service, label: reads from the replicas through}
  controller: {image: cloudnative-pg/cloudnative-pg}
  disruptions: operator
  pattern:
    name: primary-with-replicas
    roles:
      instances:     {reference: "status.instanceNames[]"}
      primary:       {reference: status.currentPrimary}
      target:        {reference: status.targetPrimary}
      write-service: {reference: status.writeService}
      read-service:  {reference: "{name}-ro"}
  docs: https://cloudnative-pg.io/documentation/current/
```

The knowledge of a `Redis` ships with `redis-operator`, in its own
repository.  Until it does, a workstation runs this file from
`transect/local/resources/`:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: redis.redis.guid.foo
spec:
  summary: A Redis or Valkey primary and its replicas, kept writable through drains and failovers.
  healthy: {condition: Ready}
  references:
    - {path: status.currentPrimary, kind: Pod, label: primary is}
    - {path: "status.instances[].name", kind: Pod, label: instance}
    - {path: status.targetPrimary, kind: Pod, label: the operator's target is}
    # The status of a Redis names no Service, and the operator names
    # them for the Redis.
    - {name: "{name}-rw", kind: Service, label: writes through}
    - {name: "{name}-ro", kind: Service, label: reads from the replicas through}
  controller: {image: chrisguidry/redis-operator}
  disruptions: operator
  pattern:
    name: primary-with-replicas
    roles:
      instances:     {reference: "status.instances[].name"}
      primary:       {reference: status.currentPrimary}
      target:        {reference: status.targetPrimary}
      write-service: {reference: "{name}-rw"}
      read-service:  {reference: "{name}-ro"}
  docs: https://github.com/chrisguidry/redis-operator
```

## What this plan leaves out

- **The lag of a replica.**  It is the reading that this view most
  lacks.  CloudNativePG's exporter reports `cnpg_pg_replication_lag`
  on each replica, and the exporter beside a `Redis` reports
  `redis_connected_slave_lag_seconds` on the primary, by the replica's
  address.  A later plan adds a query to `MetricsSource` for it, and
  until then the page draws no line for it.
- **The elevation by machine.**  It would answer "Where does each
  instance run?" with a column for each machine and its instances, the
  primary marked.  Plan 15's tabs are built.  The elevation still needs
  a way to group a role's parts by a fact about them, such as
  `spec.nodeName`, which no plan has.
- **Readings that one project has**, such as how many replicas a
  `Redis` primary needs before it accepts a write.
- **The name.**  The design says that one of `LogPattern` and the
  pattern of this document changes its name before the code has a
  pattern.  This plan names the field `pattern` and the module
  `catalog`, and leaves the choice to the maintainer before the build.
- **A sync replica, a replica cluster, and a pooler.**  CloudNativePG
  has each, and none is in the model of a primary with replicas.

## The alternatives

- **A sketch that each project states.**  Every store would be drawn
  another way, and the design gives the catalog's patterns to
  `transect` so that a reader learns the view once.
- **Field paths and templates directly in `pattern`.**  That would be
  a way to find a role's parts beside `reference` and plan 14's
  `namedBy`, and a name template would have no relation, box, or edge
  of its own.
- **`replicas` as the fill of `instances`, with plan 14's rule of each
  object once to leave the primary to `primary`.**  It needs no
  function.  But which `Pod` is a replica would then depend on the
  order of a sketch's cells, and an elevation that places `replicas`
  and not `primary` would draw the primary as a replica.  The
  expectations need the function in any case.
- **The role label on each `Pod`**, such as
  `cnpg.io/instanceRole: primary`.  It needs a key and two values from
  each project, and it says where the `Service`s send traffic, which
  the expectation on `write-service` shows already.
- **The `Pod`s that the object owns, or the scale subresource's
  `labelSelectorPath`.**  CloudNativePG creates and joins an instance
  with a `Job` that the `Cluster` owns, whose `Pod` is not an
  instance, and `redis-operator`'s CRD has no scale subresource.  A
  list in status names the instances exactly, and names one whose
  `Pod` is gone.
- **The `Service`s that the object owns, sorted by their endpoints.**
  `transect` would decide which `Service` takes the writes.
- **"Each replica runs on another machine than the primary."**  The
  design gives the rule that a workload's replicas spread across
  machines to the owner's conventions, in "Where expectations come
  from".
- **"A `PodDisruptionBudget` covers the primary."**  The budget is
  what an operator does, which `disruptions: operator` states, and the
  design gives the guards to the owner's conventions.  A store with
  one instance has no such budget on purpose, so the line would be
  unmet on most stores.
- **"A replica follows closely."**  It needs a threshold, and
  `transect` sets none.
- **A replica that restarted stands apart.**  A restart marks a `Pod`
  only when `health::pod_reasons` counts it: a container in
  `CrashLoopBackOff`, or one that restarted within `RECENT_MINUTES`,
  an hour, which is a warning unless it was one clean exit.  An older
  restart is not health, so it does not set a replica apart.  A rule
  of the stack's own would make the stack disagree with every other
  page.
- **"A backup exists."**  Each project records backups in kinds of its
  own, and a store with no backup still fits the model.

## How the work is proved

Unit tests cover the parsing of `pattern`, of an entry with `name`,
and each fault above, and the reading of the catalog's files through
the one parser.  A test of the catalog loads each file, checks each
wire against the ports of its shapes, refuses a `while`, and checks
that `primary` comes before `replicas` in each elevation that places
both.  Table-driven tests cover each expectation in each of its
states, the standing and readings of a `Pod` part and of a `Service`
part, a stack with a part that its health marks and one that an
expectation marks, the room and the ports of a stack, and a run that
meets an `Elsewhere` place.  The test that loads each first-party
`transect/` directory with `examples/` covers the knowledge of
CloudNativePG with its `pattern`.

The fixture cluster gets a kind of its own for a store, such as a
`Ledger` of the group `ledgers.example.test`, with its knowledge in
the fixture's `resources.yaml`.  It has a store of three instances
whose replica restarted within the hour, and a store of one instance,
with their `Pod`s, their claims, and `Service`s that the knowledge
names by a template.  The golden text of their pages covers the
template, and `/shapes` covers the stack.  The tests, the fixture, and
the golden pages name no real project.  `make test` passes with the
coverage floor unchanged.

On a workstation, `transect` runs against a home cluster with the
stock CloudNativePG set and the `Redis` file above under
`--resources`.  The page of each `Cluster` and each `Redis` is read
against `kubectl get pods -o wide`, `kubectl get endpointslices`, and
the printer columns of the store.  A switchover that the maintainer
starts on a store with replicas should show the target line unmet
until the primary moves, then met with the new primary.

## What the build changed

On 2026-11-21 a workstation ran `transect` against a home cluster with
the stock CloudNativePG set and the `Redis` file above under
`--resources`, and read the page of a `Cluster` with two instances, a
`Cluster` with one, a `Redis` with three, and a `Redis` with one, at
1680, 800, and 420 pixels in the dark scheme and at 1680 in the light.
Each primary, replica, machine, age, claim, and size, and each pod
behind the `-rw` and `-ro` `Service`s, was the one that
`kubectl get pods -o wide`, `kubectl get endpointslices`, and
`kubectl get pvc` printed.  Every store was healthy, so every line of
what the pattern expects was met, or did not apply to a store with one
instance.  The page of a store with one instance drew its replicas'
place, its read `Service`, and its operator at rest.  No switchover
ran that day.  A review of the build on the same day changed the rules
that several items below record.  After it, the room and the signal
path of each `Player` and the rig of the `Telescope` drew the same
sketch as the build before at 1680 pixels in the dark scheme.  Their
boards list the objects under "managed by it" in order, which before
changed from one load of the page to the next.  The build differs from
the proposal in these ways:

- **The catalog is `knowledge/catalog/` in `transect-resources`, not
  `src/catalog/`.**  The parser of a `KindKnowledge` checks `pattern`
  against the catalog, and the catalog reads its roles and elevations
  with the parsers of `knowledge`.  Two sibling modules would depend
  on each other.
- **A role that the kind fills states the kind of its parts, with its
  `group` when it is not the core group, and `instances` states
  `list: true`.**  The faults of a reference of the wrong kind and of a
  list read as one value need the catalog to say which kind and which
  roles read a list, and the proposal's file wrote neither.  The kind
  is compared with its group, so a `Service` of another group does not
  fill a role of `Service`s.
- **`primary` states `failover: true`, a field that any role of a
  pattern can state.**  The loss of the role's one part stops what the
  pattern serves until the controller promotes another part, so the
  loss page reads the part's name from the reference that fills that
  role.  It is a fact, so it is in the catalog's file, and a pattern
  has at most one such role.  The pattern's Rust module has only its
  derived roles and its expectations.
- **`role/look.rs` reads the fields that both kinds of role have.**
  `Role::new` then checks what needs both the fill and the look, such
  as `stack` with `elevation`.  `elevation::read` takes the names of
  the roles, so it reads a catalog pattern and a `KindKnowledge` alike.
  A `KindKnowledge` that writes `fill` or `derive` gets a message that
  says those are fields of the catalog.
- **`First` has two more steps, `Derived` and `Unfilled`.**  A role
  that the kind's `pattern` does not fill has the step `Unfilled` and
  the reason "pattern.roles has no write-service, so the KindKnowledge
  names no Service for it", which its empty place shows as
  `Role::unresolved` does.  The line of what the pattern expects says
  "the KindKnowledge names no Service for writes".
- **`catalog::judge` runs the pattern's module once, when the page
  builds the `Board`.**  It takes the `KindKnowledge` and returns a
  `Judged`: the parts of each derived role, the lines of what the
  pattern expects, and the parts that the lines mark.  The module reads
  one `Occurrence`, the object, its knowledge, and the cluster, and the
  expectations read the derived parts of the same run, so the drawing
  and the lines agree.  It reads the cluster through the trait
  `catalog::Cluster`, which extends `ReferenceEnds`, so the instances
  and the primary are followed by `knowledge::follow`.  The binary
  answers the controller with `controller`, a pod's readiness with
  `transect_graph::pods::ready`, the one readiness function of every
  page, and the pods behind a `Service`'s ready endpoints with
  `Endpoints::ready_pods`, which the page builds once.
- **A derived role can be missing its part.**  `Derived::Missing`
  draws the place as an expected role with no part, as when the
  knowledge names its controller's image and nothing that runs it
  controls the object.  `Derived::Nothing` draws it at rest, as for a
  store with no replica, or for knowledge that names no controller.
- **The pattern's marks are a `Marks`, which keeps one mark for each
  part.**  An unmet line's mark replaces a mark at rest.  The page
  applies them only in the pattern's own elevation, where the lines
  are listed, so the kind's own elevations draw the same parts as
  their health says.  On a store with no replica, the read `Service`
  and the operator are marked at rest with the proposal's words.  An
  unmet line marks the part that it names with short words, such as
  "not ready", which name no other object, and the line itself names
  each object, linked to its page.
- **The promoter's label is "operator".**  "promoted by" made the
  empty place read "no promoted by".
- **The head names the pattern on a short line under the question,**
  "primary with replicas, drawn the same way for every kind that fills
  its roles".  The caption under the sketch names the `KindKnowledge`
  that fills the roles.  The pages do not say "transect's catalog": the
  fixture has a `Deployment` named `catalog`, and the test that every
  name with a page is a link counts the word.
- **A source names the name that a template makes,** such as
  `orders-ro` for `{name}-ro`, linked to the object, in a part's source
  line and in a wire's.
- **The reason on a `Pod` part is brief.**  `Reason::brief` in
  `transect-observe` leaves out the names of the pod and the container,
  because the part names its pod.
- **Each pod behind a `Service` is a reading of its own, `Pods`,
  linked to the pod, as each claim is.**  `Ports` is written as the
  `Service`'s box on a workload's page writes it, `6379` and `53/UDP`.
- **A stack is a part with the names of its parts.**  `Part::stack`
  has each name, linked, and the label counts them, "replica × 2".  A
  part stacks when it is drawn plainly.  A reading that every part has
  with one value is written once, followed by "each", as "ready 1/1
  each".  Other readings list each part's value in the order of the
  names, with "—" for a part that has none.  A stack draws one fewer
  copy of its outline than it has parts, at most two.  The copies stay
  inside the part: `--room-above` adds their height, and
  `--stack-room` makes the body narrower by their offset.
- **An elevation can state `align: top`,** in the one schema of a
  `KindKnowledge` and the catalog, and the default is `bottom`.  The
  store states it, so the primary faces the stack when a replica stands
  apart under it, and the two `Service`s line up.
- **A grid with a wire that crosses a gap between rows has rows 64
  pixels apart,** as a wired grid has wider columns, so the wire has
  room to bend and to carry its words beside it.
- **Each line of a wire takes the state of the part at its end.**
  `wires.js` marks a line to a part that warns or fails in
  `--shape-warm`, with its arrow, and the lines to the other parts of
  the role stay plain.  A role with several parts gets a bar in the gap
  12 pixels beyond its area's edge, so no line lies on a part's
  outline, and an arrow on the short line into each part's port.  A run
  meets a bar at the place nearest its other end.  A wire that leaves
  the top or the bottom of its area has its words beside its longest
  vertical segment, and words stand on no horizontal segment shorter
  than 48 pixels.  The proposal said that the script does not change.
- **An empty place that a wire meets stands in the middle of its
  area,** so the wire does not step sideways.  An empty place that no
  wire meets stands where it stood, beside the parts it belongs with,
  so the rig of a `Telescope` does not change.
- **The reason of a marked part wraps and is never cut, and a reading
  that lists several values breaks only after ", ".**
- **The light scheme has inks of its own for a warning's outline and
  lines, an empty place's dashed outline, and a part at rest,** and the
  plain fill is lighter.  The port of a `socket` is a `mark`, not
  `glass`, because a dark port was the heaviest mark on a light page.
- **A value does not say that it reaches the page's own object,** and
  the line of the parts that an elevation does not place leaves out an
  object that the elevation draws in another role.
- **An entry with `name` also refuses `match`,** which reads a field.
- **The words of a condition's chip keep an acronym whole,**
  "Consistent system ID" and not "Consistent system iD".
- **The fixture's journal has four instances.**  Two replicas are
  alike, so the page stacks them, and the third crash-loops, so it
  stands apart and the line "each replica is ready" is unmet.  The
  replica crash-loops and did not only restart, because `--fixture`
  reads the wall clock, and a restart at the fixture's time is not
  recent there.  The journal's `Ledger` states no `disruptions`.

On the home cluster, `transect/local/resources/` had a link
`00-cloudnative-pg` to `examples/cloudnative-pg` and the file
`00-redis.yaml`.  `/resources` showed both loaded, and showed the
cluster's own `kinds/postgres.yaml` and `kinds/redis.yaml` not loaded,
with the message that `primary` is now a role of `pattern`, because the
parser refuses them before the loader compares the files.  Those files
change in the cluster's own repository.
