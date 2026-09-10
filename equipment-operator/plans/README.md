# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It also states how the work was proved, and a proof runs on
hardware.

The README states what the operator is. These documents state why it
is built the way it is, and what it still owes an answer to.
`completed/` holds the plans that are built and drilled.

## The design

* [00, The equipment-operator design](00-design.md). What equipment
  is, what a `Receiver` holds, how the level rides the media bus, and
  what the Service front is for.
* [01, Prometheus metrics](01-prometheus-metrics.md). In progress. The
  operator serves Prometheus metrics on port 9200 under liken's shared
  contract: the receiver connected and claimed, power, volume, the
  selected input, and commands by status.

## Completed

* Plan 01, the `Receiver` with the `denon` protocol, the session, and
  the level path. Proved on a living room on 2026-09-07: the Denon's
  status followed its own remote, a `Play` selected the input, and
  the room's remote stepped the receiver under a declared ceiling.
  Releases 2026.09.07-001 to -003. Three things that day changed the
  design: the Denon's `MVMAX` line is not a limit, a session must
  adopt the receiver's position before it applies any level, and a
  press is a direction and not a level.
