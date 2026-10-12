# 22, Prometheus metrics

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the series are not designed
yet.

## The problem

[Root plan 65](../../plans/completed/65-prometheus-metrics.md) says
every long-running process that a `liken` operator ships serves
Prometheus metrics, under one contract for the port, the names, and the
labels. `observatory-operator` serves none today. Conditions say what
is true now, and nothing keeps the history: how long activation took,
how often a device dropped its connection, how the guiding error moved
through a night. Nobody watches the cluster at dawn, so a condition
that waits on a person also needs a way to reach that person.

## Decisions

- **Conditions come first.** Each state that a person or a controller
  acts on is a condition on the operator's resources, as the
  repository's rule for conditions and `Event`s requires.
- **The operator exports its own metrics under plan 65's contract.**
  Plan 65 set aside kube-state-metrics on the CRDs, because it names
  series in its own vocabulary and adds a second reader between the
  fact and the graph.
- **Alerts reach a person through their own Prometheus and
  Alertmanager.** The operator exports the series, and the manual
  suggests the alerts, such as a telescope that waits for someone to
  set it up or to cover it ([plan 21](21-a-person-in-the-procedure.md)).
  The operator sends no notification itself.

## Still open

The series for each resource, the readings worth keeping, and the
alerts the manual suggests.
