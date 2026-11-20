# 00: Design

This document is the product design of `transect`: who it is for, the
questions it answers, and the positions that every later decision must
agree with.  It states no schema, no screen, and no storage.  The
maintainer and an agent settled each position in conversation, starting
on 2026-11-20, and each one has the date it was settled.  "Open
positions" lists what is not settled.

The code in this directory is a proof of concept, built before this
framing.  Its design is in
[completed/00-proof-of-concept-design.md](completed/00-proof-of-concept-design.md).
Where the two documents differ, this one holds for new work, and that
one describes the code.

## Who it is for

`transect` is for a person who manages a Kubernetes cluster that
changes faster than that person can keep up with, because agents build
and tune things in it all day.

This reader built or directed much of the cluster, so they do not need
it explained from the start.  Their knowledge of it is out of date.
Their work is supervision: agents make a thing, and the person checks
that it is sound and that it is what they meant.

Agents will read `transect` too, as a source of knowledge about the
cluster before they change it.  That comes after the human reader.

Settled on 2026-11-20.

## The questions

The reader comes back to a system with these questions, the most
important first:

- **How does this work now?**  Its parts, what each one does, and how
  work or data moves between them.
- **What is out of place?**  What nothing owns, what nothing explains,
  and what does not fit the way the rest of the cluster is built.
- **Is it working, and if not, where?**

"What changed since I last looked" is a question the reader has, and it
waits until these are answered well.

"What breaks if this is lost" is not a goal of its own.  A reader who
knows how a system works and where it does not work can answer it.  The
proof of concept spent much of its design on this question, and the
maintainer judged that it drew the design away from the questions
above.

Settled on 2026-11-20.

## Living documentation

`transect` is documentation of a running cluster and of the things
around it that the cluster depends on.  What it shows comes from what
is registered and running, so it is true at the moment that the reader
looks.  A page that a person wrote last month describes last month's
cluster, and `transect` has no such page.

The cluster does not record everything.  It does not record why a thing
exists, or that a pool of workers reads one stream of a broker.  People
and agents add those facts, and "Claims" below says how an added fact
stays true.

Settled on 2026-11-20.

## Simple primitives

`transect` gives the reader a small set of simple primitives, and the
reader does the reasoning.  A primitive is good when a person or an
agent can combine it with the others to reach a conclusion that
`transect` never computed.  A reader who sees how a system works and
where it does not work can tell what its loss would cost, and a reader
who sees a thing beside its peers can tell which one drifted.

So a new idea is judged first by whether a primitive lets the reader
reach the conclusion.  `transect` computes a conclusion itself only
when a stated expectation makes the check exact.

The primitives are these:

- **Parts.**  The things that exist: objects, containers, and the
  things around the cluster.
- **Relations.**  How parts connect, read from the cluster: one runs
  on another, stores on it, owns it, or reaches it.
- **Roles.**  What a part does within a named pattern.
- **Claims.**  Facts that people and agents add, each attached to a
  part or a relation.
- **Expectations.**  What a pattern or a convention says is true of a
  part, shown as met or unmet.
- **Likeness.**  Which parts are of the same sort, so that `transect`
  can show them side by side.
- **Signals.**  What happens while the cluster runs: a condition or an
  `Event` from the Kubernetes API, a metric from Prometheus, a trace
  from an OpenTelemetry store, a line from a log store.

Parts and relations say what the cluster is, and signals say what it
does.  A signal reaches the reader through `transect`, attached to the
part or the relation that it describes, so that a person or an agent
can reason about the behavior of a system in the place where they read
its structure.

The list was settled on 2026-11-20.  What makes parts "of the same
sort" is open.

## Signals

`transect` correlates signals.  Each source describes the same system
in its own terms: Prometheus has a series with labels, a trace store
has spans with the names of services, and the Kubernetes API has an
`Event` on an object.  `transect` holds which part or relation each
signal is about, so a metric, a span, a log line, and a condition that
came from different sources are present together on the part that they
describe, where they are of most use to a person or an agent who is
learning how the system behaves.

`transect` does nothing else with a signal:

- **It does not judge a signal.**  An alert has the judgement of
  Alertmanager, and a condition has the judgement of the operator that
  wrote it.  `transect` passes each one on with its source.  It sets no
  threshold of its own.
- **It does not keep a signal.**  The sources hold the history, and
  `transect` shows what they say now.

A stated expectation can use a signal.  "Work that goes in comes out"
is checked with a metric, and that check is exact because somebody
stated the expectation.

Settled on 2026-11-20.

## Patterns

A pattern is a named set of expectations.  To call a group of workloads
a work queue is to expect that it has producers, a queue, and workers,
that the workers can reach the queue, and that work which goes in comes
out.

A reader who knows what a work queue is
understands a new one as soon as they see the name, because they bring
the model with them.  `transect` then shows which parts of the cluster
have which role, and where this system differs from the model.

This idea answers each of the reader's questions:

- **How does this work now?**  The name gives the model, and the
  cluster gives the parts.
- **What is out of place?**  An expectation that the structure does not
  meet: a work queue with no workers, or workers that cannot reach the
  queue.
- **Is it working?**  An expectation that the behavior does not meet:
  work goes in and does not come out.

A pattern is in `transect`'s catalog only when its name lets `transect`
expect something that it could not expect without the name.  A name
that adds no expectation is a label.

Settled on 2026-11-20.

## What is out of place

A thing is out of place in one of these ways:

- **Excess.**  It is granted or declared, and nothing uses it.  An
  account holds a credential that none of its workloads ask for.
- **Broken.**  It names something that does not exist or that it cannot
  reach.
- **Unexplained.**  Nothing states why it exists or who owns it.
- **Different from its peers.**  It does not match the things beside
  it that were built the same way.
- **Not flowing.**  Work or data that the system is expected to move
  does not arrive.

Excess, broken, unexplained, and different from its peers are faults
of structure, and the cluster's objects show them.  Not flowing is a
fault of behavior.  The objects can be correct while it happens, so
`transect` needs a signal, such as a metric or a count in the service
that receives the work, and it needs knowledge of what is expected to
flow.

Settled on 2026-11-20.

## Peers

`transect` shows a thing beside its peers, the things that were built
the same way, so that a person or an agent who reads the page can
notice the one that differs.  Agents build by copying a thing that
works and changing it, so the things that a new one was copied from are
what to compare it with.

`transect` does not search for such differences itself.  To find a
difference that matters with no stated expectation is a harder problem,
and it waits.  The maintainer corrected an earlier form of this
position, which had `transect` compare peers without being asked.

Settled on 2026-11-20.

## Where expectations come from

An expectation that `transect` checks is one that somebody stated, and
`transect` shows its source with it:

- **A pattern.**  `transect`'s catalog states what a pattern expects,
  and a claim states that a group of parts is one.
- **The owner's conventions.**  The owner of the cluster states a rule
  for the whole cluster, such as that every namespace has a policy that
  refuses traffic by default, or that every workload with replicas
  spreads them across machines.

A cluster's owner often checks conventions in the repository that holds
the cluster's manifests, before a commit.  That check reads the
manifests of that repository.  It does not read what runs, and it does
not read what an agent applied by another way.  A convention that
`transect` holds is checked against the running cluster, so it covers
both.

Settled on 2026-11-20.

## Claims

A claim is a fact that a person or an agent adds because the cluster
does not record it.  Every claim is attached to something that
`transect` can observe.

- **A claim that the cluster can contradict is checked against the
  cluster, and it is checked again as the cluster changes.**  "These
  workers read this queue" is such a claim.  When it stops being true,
  `transect` shows it as wrong.
- **A claim that the cluster cannot contradict shows its age against
  the thing it describes.**  "This exists to keep the orders" cannot be
  proved false.  `transect` shows who wrote it, when, and what has
  changed about the workload since.  A reason that was written before
  the workload doubled its parts reads differently from one written
  yesterday.

A person or an agent can mark an unmet expectation as intended, such
as a namespace that has no default policy on purpose.  The mark is
attached to the state that it was written about.  When that state
changes, the mark no longer covers it, and `transect` reports the
expectation again.

So the reader can always tell when to stop trusting a claim.  This
matters most for claims that agents write.  An agent's account of its
own work is most reliable at the moment that it writes it, and the rule
shows how far the system has moved since.

Settled on 2026-11-20.

## No language model at runtime

`transect` calls no language model while it runs.  Every sentence that
it shows is a template that it fills from the cluster and its signals,
or text that a person or an agent stored as a claim.  So the same
cluster and the same claims give the same page, and a reader can trace
each sentence to its source.

A language model does its work when knowledge is written.  An agent
writes a claim when it builds a thing, and "Claims" above says how that
claim is checked or shows its age.

This is a constraint, and the proof of concept kept it.  The maintainer
confirmed it for new work on 2026-11-20.

## Disagreement

`transect` shows every claim and every expectation with its author,
and that includes the ones that conflict.  It does not choose between
them, and it has no rule of precedence.  It compares what it observes
in the cluster with each one, and shows where the cluster meets it and
where the cluster does not.

A disagreement is a fact that the reader needs.  When the author of an
operator states one thing about a system and an agent states another,
a rule that picks one hides the other.  Each claim already shows
whether the cluster confirms it or how old it is, so the reader has
what they need to weigh one against the other.

Settled on 2026-11-20.

## Who writes the knowledge

The people who know a fact write it: the author of an operator, the
developer of an application, the owner of the cluster, and the agents
that build things in it.  An agent knows why it built a thing at the
moment it builds it, and that is the cheapest moment to record the
reason.

A person and an agent read the same facts.  A view is how a person
reads them, and a query is how an agent reads them.

Settled on 2026-11-20.

## How it stays legible

A cluster holds more state than a tired person can read.  `transect`
compresses it in these ways:

- **Name the model.**  A pattern's name tells the reader which model to
  bring before they read a part.
- **Stack what is alike.**  The instances of one role are one stack
  with its count, and the reader can open it.  An instance that differs
  from the rest stands apart from the stack.
- **Healthy is the plain state.**  A part that is healthy and takes
  part is drawn plainly.  A part that deviates is marked.  A part that
  is inactive, disabled, disconnected, or not relevant to the question
  is dimmed.
- **One question for each view.**
- **Put the reading that matters on the part.**  A queue shows how much
  work waits in it.
- **Open one level at a time.**

These are principles, and the visual design is not started.

Settled on 2026-11-20.

## The cases

The design is tested against real systems of different shapes.  For
each one, the first step is to write in prose what the reader should
understand, as answers to the questions above.

- **A system of several services that work as one, which agents built
  under a person's direction.**  It runs autonomous agents: one service
  starts a turn when an event arrives, and others give the turn its
  credentials, its browser, and a store for its transcript.  A primer
  that was written by hand explains it, so each part of the primer can
  be compared with what `transect` could show.
- **The walk `Job` of `library-operator`.**  Its pod has many
  containers that start together, and the order in which they hand work
  to each other is in no field of the pod.
- **A web application with workers and a database.**  This is the
  common shape, and the baseline for what fits a pattern.
- **A database with replicas, under an operator that another project
  ships.**  Neither the cluster's owner nor an agent wrote the
  operator, so the knowledge comes from outside.

Settled on 2026-11-20.

## The order of the design

The work goes in this order, and a later step does not start before the
earlier ones are settled: the reader, the questions, the cases, the
ways to compress, what a pattern is, the facts that the cases need and
the API does not hold, and last, where knowledge is stored and how it
reaches `transect`.

A schema and a storage form were both designed early on 2026-11-19 and
deleted on 2026-11-20, because they came before the questions they
served.

## Open positions

- Who may write a claim.  That depends on where knowledge is stored,
  which the design settles last.
- What counts as the cluster's surroundings, such as a storage server,
  a forge, an identity provider, and the devices on a bus.
- Which ideas of the proof of concept carry over.  The candidates are
  places that keep their position from one visit to the next, a mark on
  each fact that states its source, and the elevations from the cluster
  down to a container.
- What makes parts of the same sort, for likeness.
- What a pattern's catalog holds first.  The cases will show which
  patterns are real.
