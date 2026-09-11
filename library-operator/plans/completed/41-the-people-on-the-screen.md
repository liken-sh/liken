# 41, The people on the screen

[Plan 14](14-watch-state-and-people.md) built the record: a
`Person`, a `Watch`, and a progress store that every namespace writes
from the bus. Nothing on a screen reads it yet. This plan puts the record on the screen: the browser asks who is
watching, sends the answer with every play request, and draws
continue watching and history from its own copy of the store.

## The problem

A `Play` the browser requests today names no people and no aliases,
so the store records it against the `Player` alone. The store's rows
reach no screen, because the screen pod runs one Corrosion agent, the
catalog's. And the home screen has no row for what a person was in
the middle of.

## The design

### The screen pod joins the progress cluster

The screen pod gains a second Corrosion agent beside the catalog
agent, on the progress configuration in the same image, bootstrapping
to the `progress` `Service` on port 8788 and answering on 8081. Its
file is on the screen's local-path claim beside the catalog's, so the
browser starts with progress already on disk. The pod carries the
progress member label, so the namespace's progress `EndpointSlice`
names it.

The second agent's resident memory on a 1 GB screen is the number
this plan measures first. If the two agents together do not fit, the
fallback is one agent and one file for both, which plan 14 set aside.

### The browser asks who is watching

When the browser has no answer to who is watching, it draws the
`Person` list and asks. The answer stands until three hours pass with
no press, and every play request carries it as `people`. A press on
the circles of the room's strip asks again with the room already
chosen.

The browser reads the `Person` list from a file, because a screen
pod holds no API credential. The operator writes one `ConfigMap` per
screen namespace from the `Person` objects, cut to the name and the
display name the browser draws, owned by the namespace's `Catalog`
where one stands. The pod projects it, and the browser reads the
file again each time the picker opens, so a `Person` added later is
offered within the kubelet's sync period and no pod restarts. The
bus was weighed for this and set aside: a list that changes a few
times a year is not worth a retained topic that a broker restart
takes away until the message returns.

### The play request carries the work

The browser fills `aliases`, `season`, and `episode` on every play
request from the catalog row it plays, and `watch` when the audience
and the item match a `Watch` it knows. A `Watch` it does not know, it
asks the operator to create over the bus, the way it asks for a
`Play`.

### Continue watching and history

The home screen gains a continue-watching row: every audience the
current people are in, latest row per work, joined to the catalog by
alias in SQL across the two local files. A history page reads the
same rows backward. Both draw only from the local files, and the
Corrosion update stream wakes the redraw the way the catalog's does.

## What is not decided

- Default people per screen, and where that default lives. A living
  room asks every time on purpose; a bedroom might not want to.
- Retention of history rows.

## What was decided in the build

- The answer lapses after three hours with no press.
- Finished is a position at or past ninety percent of the duration.
- A finished episode continues at the next episode in the series;
  a finished movie leaves the continue row.
