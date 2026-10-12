# 22, Prometheus metrics

Stub. Proposed on 2026-10-12. Not designed.

[Root plan 65](../../plans/completed/65-prometheus-metrics.md) says
every long-running process that a `liken` operator ships serves
Prometheus metrics, under one contract for the port, the names, and
the labels. `observatory-operator` serves none today. Conditions say
what is true now, and metrics keep the history: how long activation
took, how often a device dropped its connection, how the guiding RMS
moved through a night.

Metrics are also how a person hears about a condition when no one is
watching the cluster. A telescope that waits for someone to set it up,
or to cover it at dawn, sets a condition ([plan
21](21-a-person-in-the-procedure.md)), and an alert on the matching
metric reaches that person's phone through their own Alertmanager.
Plan 65 set aside kube-state-metrics on the CRDs, so the operator
exports these series itself.

To design: the series for each resource, the readings worth keeping,
and the alerts the manual suggests.
