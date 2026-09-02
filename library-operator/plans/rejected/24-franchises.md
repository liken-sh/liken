Superseded on 2026-09-02 by [plan 31](../31-franchises.md), which puts a franchise on the volume as a library kind instead of a cluster-owner resource, so a rebuilt catalog keeps it. The text below is the plan as it stood.

# Franchises

Plan 24. A stub for a later agent to shape. It gives a whole franchise,
the films and series of one universe in one order, a home of its own on
the screen.

## The problem

A set is what a movie sidecar names, one collection per film, and plan
22 draws it as a strip on the film's page. A franchise is bigger than a
set and crosses kinds: the films of one universe in release order or in
story order, and the series that belong beside them. No sidecar carries
it. The order is a person's opinion, and a person may hold two orders
for the same universe. So a franchise cannot be derived from the
volume, and the scanner cannot write it.

## The shape

- A franchise is a resource the cluster owner writes, in the namespace
  the libraries live in: a name, an order, art, and an ordered list of
  members named by provider id, so a member resolves to a row in any
  library of the namespace and survives a rename on the volume.
- The catalog holds a franchises table the operator writes from the
  resource, so a screen reads a franchise the way it reads a set, and a
  member the catalog does not hold yet is a gap in the order and not an
  error.
- The franchise page is built from plan 22's primitives: a header over
  the franchise's art, then the strip, grown to a wall in the chosen
  order, with a film and a series side by side.
- A franchise reaches the screen through the home page and through a
  film's page, where a strip under the set strip names the franchise
  the film belongs to.

## What is not decided

The resource's name and whether it is this operator's or a resource of
its own. Where a franchise's art comes from, because the volume holds
none. Whether one franchise can hold two orders, and how a person
switches between them. Whether a franchise can span namespaces, which
the catalog's one-cluster-per-namespace rule says no to today.
