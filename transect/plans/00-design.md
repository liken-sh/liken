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
of structure.  The cluster's objects show them, and `transect` can
find one as soon as the object is applied.  Not flowing is a fault of
behavior.  The objects can be correct while it
happens, so `transect` needs a signal, such as a metric or a count in
the service that receives the work, and it needs knowledge of what is
expected to flow.

Settled on 2026-11-20.

## Peers

`transect` compares a thing with its peers without being told what to
expect.  Agents build by copying a thing that works and changing it, so
the things that a new one was copied from are what to compare it with.
When every workload of a kind but one is built from the same template,
that one is worth a look, and no person wrote a rule to say so.

A difference can be deliberate.  A person or an agent marks it as
intended, and that mark is a claim like any other.

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

A mark that a difference is intended is attached to that difference.
When the difference changes, such as when the account gains another
grant, the mark no longer covers it, and `transect` reports the
difference again.

So the reader can always tell when to stop trusting a claim.  This
matters most for claims that agents write.  An agent's account of its
own work is most reliable at the moment that it writes it, and the rule
shows how far the system has moved since.

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

- Who may state what, and whose claim holds when two disagree.
- Where the expectations of a pattern come from: `transect`'s catalog,
  a thing's peers, or a convention that the cluster's owner sets for
  the whole cluster.
- What counts as the cluster's surroundings, such as a storage server,
  a forge, an identity provider, and the devices on a bus.
- Which ideas of the proof of concept carry over.  The candidates are
  places that keep their position from one visit to the next, a mark on
  each fact that states its source, a runtime that calls no language
  model, and the elevations from the cluster down to a container.
- What a pattern's catalog holds first.  The cases will show which
  patterns are real.
