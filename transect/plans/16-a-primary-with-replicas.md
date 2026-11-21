# 16, A primary with replicas

Proposed on 2026-11-21.  Nothing is built.

This plan builds the first pattern of `transect`'s catalog, as "Who
draws a pattern" in [00-design.md](00-design.md) describes it.  A
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
document's `references`, as a role of a sketch does, and has no other
field, because the catalog states the shapes:

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

### A reference by a name template

An operator often names a `Service` for its object and records the
name in no field.  So an entry of `references` will take `name` in
place of `path`: a template of the referenced object's name, with
`{name}` and `{namespace}` for the page's object, in the syntax and
through the code (`referenced::render`) of `referencedBy`.

```yaml
references:
  - {name: "{name}-ro", kind: Service, label: reads from the replicas through}
```

The entry refers to the one object of `kind` with that name, in the
page's object's namespace unless the kind is cluster-scoped.  It draws
the same relation, box, and edge as an entry with a `path`, and a
role's `reference` names it by its template.  A path never contains a
brace, so a role's `reference` matches an entry's `path` or its `name`
without ambiguity.

### The faults

`transect` will leave out a document, with an `Invalid` condition that
names the fault, when:

- `pattern.name` is not a pattern of the catalog;
- `pattern.roles` has a role that the pattern does not ask a kind to
  fill, lacks a required one, or gives a role a field other than
  `reference`;
- a role's `reference` names no entry of `references`, or names an
  entry whose `kind` is not the kind that the pattern's role takes,
  `Pod` or `Service`;
- `instances` names an entry whose path has no `[]`, so it cannot read
  a list;
- an entry of `references` has both `path` and `name`, or neither, or
  has `name` with no `kind`, no `{name}`, or a brace that is not part
  of `{name}` or `{namespace}`;
- the document has the top-level `primary`.  The message says that
  `primary` is now a role of `pattern`.

A cluster's own file that still has the top-level `primary` loses the
whole document until it changes, so the change of those files goes in
the same deploy as this plan.  `KindKnowledge::reads_fields` will count
the paths that the pattern's roles read, so the trimmed watch keeps
them.

### Where the catalog is

The catalog is a directory of YAML files in `transect-resources`,
`src/catalog/`, one file for each pattern, which the binary embeds.  A
file has the form of plan 15: `roles` states each role once, and each
elevation has a `name`, a `question`, a `sketch`, and `wires`.  One
parser reads the elevations of a catalog pattern and of a
`KindKnowledge`.  A catalog role has no `reference`.  It has `fill`,
which says that the kind's `pattern` fills it and whether that is
`required` or `optional`, or `derive`.  A `KindKnowledge` cannot state
`fill` or `derive`.

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
  instances:     {fill: required}
  primary:       {fill: required, shape: drum, expected: true, reads: [Ready, Node, Age]}
  target:        {fill: optional}
  write-service: {fill: optional, shape: socket, expected: true}
  read-service:  {fill: optional, shape: socket, expected: true}
  replicas:      {derive: instances-but-primary, shape: drum, expected: true, stack: true, reads: [Ready, Node, Age]}
  promoter:      {derive: controller, shape: tag, expected: true}
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
of the sketch has only empty cells, so plan 14's rule keeps its width,
and the wire that streams crosses it.  Each wire joins the facing
edges of its two roles' areas, so the wires from the `Service`s are
vertical and the wire that streams is horizontal.  `drum` and `socket`
are shapes of plan 17's library.  The promoter is a `tag`, because the
operator is in the cluster and not part of the store.

The catalog's elevations follow the kind's own, in plan 15's tabs and
URLs, with `transect` as their author.  The page of a `Cluster`, whose
kind states no elevation, opens on "the store", at
`.../elevations/the-store`.  The head of the drawing also names the
pattern, "primary with replicas".

### A `Pod` as a part

A sketch draws a part from the stores that `pages::object::stored`
reads, and that function returns nothing for a `Pod`, whose kind is
`ReadFrom::OwnPage`.  So `standing` and `readings` in
`pages/known.rs` will read a `Pod` from `Snapshot::pods`:

- **Its standing.**  `Missing` when no `Pod` has the name.  Otherwise
  the level of `Health::from_reasons(health::pod_reasons(pod, now))`,
  the rules of `transect-observe` that mark a `Pod` on every page,
  with the first reason as the part's line.
- **Its readings**, under the names that `kubectl get pods -o wide`
  prints, because a `Pod` has no printer columns of a CRD: `Ready`
  from the counts of ready containers, `Node` from `spec.nodeName`,
  and `Age` from `metadata.creationTimestamp`.
- **Its claims**, from `spec.volumes[].persistentVolumeClaim`, with
  each claim's `status.capacity.storage` from `Paged::claims`.

### A stack

A role takes `stack: true` in the schema of plan 15, so any author can
state it, because "Stack what is alike" is a rule of every view.  The
parts of the role that are not marked are drawn as one part with their
count, the name of each part, and each reading with its values, such
as the machines.  The reader opens the stack to see each part.  A
stack of one part has no count.

A part that is marked stands apart from the stack, as a part of its
own beside it.  A part is marked by its health, as on every page, or
by an unmet expectation that names it.  So a replica that is not
ready, one that restarted within the hour, and a name in `instances`
whose `Pod` is gone each stand apart, and the part shows why.

### What the shape library gives this pattern

Plan 17 defines `drum` and `socket` in the library, because a pattern
of the catalog draws them, and this plan defines no shape.

- **A `drum`**, a database, for an instance.  Its text is the same
  block as on every shape: the object's name, its readings, and the
  claim it stores on with the claim's size, as one more line.  The
  wires of "the store" use ports on its top edge and on both sides.
- **A `socket`**, a plate with a port, for a `Service` where traffic
  enters.  It shows the `Service`'s name, its port, and the pods that
  it reaches, and its wire uses the port on its bottom edge.
- **A stack**, which `transect` draws on any shape: the outline twice
  more behind the part, each 8 units up and to the right.  The ports
  on its top and right edges move out to the last copy, so the arrow
  of the wire for reads ends on the stack's outer edge, and the wire
  that streams enters the front part's left edge.
- **The states of plan 17** on each, with a dashed outline for an
  expected role with no part.

A port is a span along an edge, so the wires that meet one edge spread
across it.  A wire to a shape with no port on that edge is a fault of
the elevation, so the build checks "the store" against the ports that
plan 17 gives `drum` and `socket`.

### What the page draws

Every part draws the states of plan 17, and every wire is drawn as
plan 15 draws it.  This pattern adds these:

- **The replicas** are a stack of drums, with how many are ready, the
  machines, and the claims.
- **No replica.**  The replicas' place keeps a dashed drum with "no
  replica" and "`<instances reference>` lists only the primary".  This
  is a role with no part, and it is not a fault.  The read `Service`,
  the wire that streams, and the promoter are dimmed, because nothing
  is in their role now.  The `Service` reads "selects the replicas,
  and there is none", and the promoter reads "no replica to promote".
- **A role that the kind does not fill.**  When `write-service` or
  `read-service` is absent, the role keeps its dashed place with "the
  `KindKnowledge` names no `Service` for writes".
- **A wire** is also marked when the expectation about the part at its
  end is unmet.

Under the drawing are the list "What the pattern expects" and a line
that names the source of each kind of fact: the parts and their states
from the Kubernetes API, the roles from the `KindKnowledge`, and the
places, shapes, and expectations from `transect`'s catalog.

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
  primary marked.  It needs plan 15's tabs, and a way to group a
  role's parts by a fact about them, such as `spec.nodeName`, which no
  plan has.
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
the one parser.  Table-driven tests cover each expectation in each of
its states, the standing and readings of a `Pod` part, and a stack
with a part that its health marks and one that an expectation marks.
The fixture cluster gets a store of three instances whose replica
restarted within the hour, and a store of one instance, and the golden
text of their pages covers the template.  `make test` passes with the
coverage floor unchanged.

On a workstation, `transect` runs against a home cluster with the
stock CloudNativePG set and the `Redis` file above under
`--resources`.  The page of each `Cluster` and each `Redis` is read
against `kubectl get pods -o wide`, `kubectl get endpointslices`, and
the printer columns of the store.  A switchover that the maintainer
starts on a store with replicas should show the target line unmet
until the primary moves, then met with the new primary.
