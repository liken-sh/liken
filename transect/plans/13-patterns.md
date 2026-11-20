# 13: Patterns

This plan is a design for review.  The maintainer and an agent talked it
through on 2026-11-19, and nothing in it is built.  It gives the idea,
the decisions so far, the stages, and the questions to answer before the
first stage starts.

`transect` draws every workload the same way: a box, its pods, and its
labelled edges.  A pool of workers that reads a queue, a database with a
primary and replicas, and a `Job` whose containers hand work to each
other all look alike on the page, though each works in a different way
and each fails in a different way.  A person who opens the page of a
lost worker reads the same loss as for a lost database.

A pattern is a named arrangement of parts, such as a work queue, in
which each part has a role.  With this plan, the people who know how a
system is arranged write that down as data, and `transect` draws the
arrangement from the live cluster, with a reading of health and loss
that fits each role.  The maintainer's examples are "here's a work
queue" and "here's a web and worker deployment with a database and a
broker".

## What the knowledge states today

Kind knowledge is a vocabulary for edges and labels.  `references`,
`healthy`, `controller`, and `docs` tell `transect` what an object
connects to and how to read its health.  One field describes how a kind
works inside: `primary` names the pod that takes writes, and the loss
view reads it to state that writes stop until the operator promotes a
replica.  This plan extends that one field into a general form.

## Decisions

### `transect` owns a small catalog of patterns

Each pattern in the catalog has a name, its roles, one drawing, and a
reading of health and loss for each role.  For a work queue, the roles
are the producers, the queue, and the workers.  The loss of one worker
of five costs nothing, the loss of the queue stops all of the work, and
the reading that matters is the backlog.

A pattern enters the catalog only when its drawing or its reading of a
failure differs from the patterns already in it.  An author of knowledge
cannot define a pattern, because a person recognizes a drawing only when
every work queue is drawn the same way.

The catalog has no pattern for a whole application.  An application is
several patterns over the same parts: a web tier and its workers are a
work queue through the broker, and both tiers store in one database.
An object can have a role in more than one pattern.

### A `find` block binds each role to the cluster

Knowledge names a pattern and states which objects have which role.
The block uses the forms that Kubernetes and `transect`'s resources
already use, so a person who has written a `NetworkPolicy` can write
one:

- a kind, as `kind` and `group`;
- a `selector`, the `LabelSelector` of Kubernetes, with `matchLabels`
  and `matchExpressions`;
- a `namespaceSelector`, in the same form;
- ownership, for an object that the starting object owns;
- a name, or a field path that holds one, by the conventions in "How
  the resources name their fields" of the design.

A role can be a workload, a container of a pod, or a medium that has no
object in the API, such as a stream in a Redis or a topic on a broker.
The design already lets a reference name "a value `transect` has no
object for".

Knowledge states the whole arrangement that is possible, and each match
in the cluster selects from it.  A stage or a role that matches nothing
in one match is left out of that drawing.  So one piece of knowledge
covers a `Job` whose set of containers differs from one run to the
next.

### Where a `find` block starts

**Each object of a custom kind.**  The `find` block is in the kind's
`KindKnowledge`.  The operator ships it beside its CRDs, or a person
who does not own the operator writes it, as `examples/` does today for
CloudNativePG.  `KindKnowledge` is a cluster-scoped resource of its
own, so a CRD carries none of this text and anyone can ship it.  This
sketch is for `library-operator`'s walk `Job`, and its label values and
field names are not checked against the code:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: libraries.library.liken.sh
spec:
  patterns:
    - pattern: pipeline
      find:
        owned: true
        kind: Job
        group: batch
        selector:
          matchLabels: {library.liken.sh/worker: walk}
      stages:
        scan: {}
        probe: {after: [scan]}
        arrival: {after: [scan]}
        identity: {after: [scan]}
        nfo: {after: [identity]}
        art: {after: [identity]}
        trailer: {after: [identity]}
        marks: {after: [identity, probe]}
        trailer-files: {after: [trailer]}
        close: {after: all}
      backlog:
        query: library_fact_gap
        labels: {library: library, stage: fact}
      rate:
        query: 'rate(library_attempts_total[5m])'
        labels: {library: library, stage: fact}
```

Each stage names a container of the `Job`'s pod.  All of the containers
start together, so the pod's spec does not state that `nfo` waits for
the id that `identity` writes.  That order is in the operator's code and
manual only, and this knowledge is where `transect` reads it.

**A selection of plain objects.**  An application with no CRD has no
kind to attach knowledge to, so a resource of its own holds the same
block.  The author of the application or the owner of the cluster
ships it.  `Pattern` is a placeholder for its name:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: Pattern
metadata:
  name: shop-orders
spec:
  pattern: work-queue
  namespaceSelector:
    matchLabels: {kubernetes.io/metadata.name: shop}
  find:
    producers:
      kind: Deployment
      group: apps
      selector:
        matchLabels: {app.kubernetes.io/component: web}
    workers:
      kind: Deployment
      group: apps
      selector:
        matchLabels: {app.kubernetes.io/component: worker}
    queue:
      kind: Redis
      group: redis.example.io
      name: broker
      medium: {stream: orders}
```

A chart that sets the labels that the Kubernetes project recommends can
ship one of these, and it matches every install of the chart.

### Each role can name a backlog and a rate

A pipeline's stage and a work queue read the same way: how much work
waits here, and how fast it moves.  For a stream, the backlog is the
consumers' lag and the rate is the acknowledgements in a second.  For a
phase of `library-operator`'s walk, `library_fact_gap` is the backlog
and the rate of `library_attempts_total` is the rate.  A stage with a
backlog and no rate waits, and a stage with both works.

Knowledge names each reading as a Prometheus query and the labels that
key its answers, in the form that a `MetricsSource` gives `containerCpu`.
`transect` sends one query for each reading for the whole cluster, as it
does for `containerCpu`, and not one for each stage.  The label names of
one cluster's Prometheus stay in the `MetricsSource`.  A pattern with no
readings still draws its arrangement, and the API alone shows which
containers ended and which failed.

### A pattern shows where it matched

A matched pattern is a lens tab on the page of each object that has a
role in it, as the vulnerabilities tab shows where kind knowledge names
`findings`.  Each pattern has its own drawing, and a page with several
shows them one under another.  Each role's box links to its object's
page, and each fact carries the kind mark or the intent mark that
"Provenance marks" of the design defines.

### `transect` infers no pattern

A pattern is drawn only where knowledge states it.  `transect` does not
guess a pattern from traffic or from the shape of a workload, because a
wrong guess teaches a wrong model of the system.  The runtime still
calls no language model.

### A mismatch shows on `/resources`

Knowledge and the code that builds an object are separate files, so
they can differ.  A pattern whose `find` matches no object, or whose
stage names a container that no matched pod has, gets the `Matched
False` condition that the design already gives a resource that names
something no object has.  A pod of a matched `Job` with a container
that no stage names is reported the same way.  An operator of the
`liken` repository also tests its knowledge against the pod that it
builds, so the two change in one commit.

## Not in this plan

- **The order of calls in one request.**  A sequence is what the code
  does over time, and no object in the cluster records it.  A trace
  from an OpenTelemetry store is a recorded sequence, and reading
  traces would be a new source of signals with a plan of its own.
- **Which service calls which, as observed.**  That is slice 2's flow
  agent.  A pattern states what its author declares.
- **One merged drawing of several patterns that share parts.**  The
  drawings stack until a page shows that they read as fragments.
- **A pattern declared in an annotation or an image label.**  Both are
  possible sources for an application's author, after the resource
  works.
- **Views that need no new knowledge.**  A drawing of an operator's
  kinds with the split between what a person writes and what the
  operator writes, which `managedFields` gives, and a drawing of a
  pod's containers in start order with the audience of each token.
  The conversation found both, and each can be a small plan.

## Stages

1. The catalog's first patterns, each with its roles, its drawing, and
   its readings of loss, on the fixture, bound by a `find` block in
   kind knowledge.  The fixture gets a kind whose objects own a `Job`
   with stages.
2. The standalone resource, with the `selector` and
   `namespaceSelector`, and the `Matched` conditions on `/resources`.
3. The backlog and the rate, through the `MetricsSource`.
4. Knowledge for `library-operator` in `examples/liken/`: the walk
   `Job` as a pipeline, and the trickplay worker `Job` as a work queue
   that the pipeline's `close` container fills through the bus.  A
   test in `library-operator` checks the knowledge against the pod
   that the operator builds.
5. Check the design against a real cluster, and stop.

## Open questions

- The first catalog.  The candidates are a work queue, a pipeline, a
  primary with replicas, publish and subscribe, leader election, and a
  gateway in front of a workload.  Whether `primary` becomes the
  primary-with-replicas pattern or stays a field beside it.
- The name of the standalone resource, and its scope.  A
  cluster-scoped resource with a `namespaceSelector` gives one match
  for each namespace that it selects, and a chart can ship it once.  A
  namespaced resource, like `Intent`, needs no selector for the common
  case.
- How one drawing joins two patterns when work leaves one and enters
  the other, as the walk `Job`'s `close` container fills the queue of
  the trickplay workers.
- How a phase of `library-operator` maps to the `fact` label of its
  metrics, since a phase can serve more than one fact, and how often
  the operator writes those metrics during a run.  Which phase
  `contributors` waits for is not checked either.
- Whether a role's medium, such as a stream's name, is a fixed string,
  a field path, or a template.
