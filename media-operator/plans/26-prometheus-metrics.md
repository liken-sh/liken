# 26, Prometheus metrics

Proposed. No metrics or hardware drill are implemented by this plan.

## The problem

A healthy operator pod does not show whether playback starts reliably or
whether a running playback pod has stopped reporting. Reconciliation
counts do not answer either question.

[`report.go`](../report.go) receives playback reports and online/offline
availability. [`status.go`](../status.go) derives `Play` phase from pod
state and playback values from reports. A `Running` pod does not prove
that playback produced a frame. The report cache does not currently record
receipt times, so freshness needs new instrumentation.

## The design

Expose playback reliability through a bounded set of per-`Player` metrics.

| Signal | Meaning and use |
|---|---|
| Playback outcomes | Counters for observed starts, start failures, runtime failures, and normal or requested endings. Fixed categories show failure rates without exporting error strings. |
| Startup duration | A histogram from `Play` creation to the first fresh playback report for that attempt. Measures startup delay, including scheduling, but does not claim to measure first visible frame. |
| Pending startup | A count of attempts awaiting their first fresh report and the oldest creation timestamp. Includes attempts that never contribute to the successful-start histogram. |
| Expected reporting and report freshness | Active-attempt count and the oldest last-report timestamp among those attempts. Detects lost reports while playback is expected to remain online. |
| Observation health | Bus connection and resource-observation validity. Distinguishes a playback failure from an operator that cannot observe playback. |

The implementation must define an attempt around the playback pod's
identity, including replacement pods for an existing `Play`. Use that
identity internally for deduplication, never as a metric label. Specify
which `Play` creation timestamp applies when a replacement resumes an
existing run; report replacement startup separately from initial startup
if the two durations would otherwise be mixed.

### Fresh reports and outcomes

The first report must belong to the current attempt. Retained MQTT state
received after a subscription or reconnect does not prove that its producer
is alive. Add attempt identity and producer freshness to the report contract
if the existing wire cannot establish them. Receipt time alone is
insufficient. This change must remain compatible across a rolling upgrade.

Count outcomes at observed lifecycle transitions, once per attempt in the
running process. Repeated reconciliation and replayed retained messages
must not create starts or failures. On operator restart, initialize existing
attempts without replaying historical outcomes. Counters are operational
observations, not an exact durable playback ledger.

An `Ended` report is not by itself evidence of a crash. Normal completion,
requested stop, deletion, and deliberate pod replacement must be separated
from unexpected failure. Base failure categories on structured lifecycle
facts, not matching arbitrary error-message strings.

### Idle, paused, and unknown

A paused film still has a reporting process. Check report freshness during
pause, but do not alert because its position stopped advancing. An idle
`Player`, an ended attempt, and an intentionally removed playback pod have
no reporting obligation.

Do not treat operator-to-broker loss as one playback failure per `Player`.
Expose source validity and suppress dependent playback conclusions until
observations become valid. Prometheus's `up` covers an unreachable exporter.
Define an initial observation grace period after restart and reconnect.

### Collection and labels

Scrapes read in-memory state and make no MQTT or Kubernetes API calls.
Use namespace and stable `Player` name as labels, plus fixed outcome
categories. Aggregate concurrent attempts explicitly; do not silently select
one report when several attempts contribute to a player's state.

Never label metrics by `Play` name, pod UID, media URI, title, file path,
position, or viewer identity. Remove a deleted player's series. Process
counters reset on restart, and startup durations are recorded only when
both endpoints of the measurement are established for the same attempt.

Provide a configurable, disableable `/metrics` listener with bounded HTTP
timeouts and internal scrape access. Keep `PodMonitor` or `ServiceMonitor`
resources in an opt-in deployment overlay so base manifests require no
monitoring CRDs. Playback and reconciliation must work without Prometheus.
Final names, histogram buckets, listener settings, and alert windows belong
to the implementation design.

## Considered and set aside

An external custom-resource collector could export current `Play` phases.
It would miss short-lived outcomes between scrapes and cannot derive fresh
producer reports from pod phase. Instrument the lifecycle and report path
while retaining resource status as the source for current phase semantics.

Per-title viewing histories, position graphs, dropped-frame metrics, and
playhead-stall detection are outside the first set. Frame quality needs
player-side measurements. Hardware availability and peripheral battery
metrics belong to their respective hardware operators.

## Proof

Write failing tests before implementation. Use a real metric registry,
controlled time, lifecycle fixtures, and the repository's bus test setup.
Cover successful startup, permanent pending, start failure, runtime failure,
pause, requested stop, natural ending, pod replacement, and player deletion.

Replay retained messages and reconnect during a run. Confirm that stale
reports neither satisfy startup nor refresh a live-report timestamp. Verify
that restarts do not recount old outcomes, and that aggregation preserves
the oldest outstanding attempt. Repeated scrapes change no counters.

On hardware with Prometheus, start playback, pause it, end it, and inject a
playback failure. Separately interrupt reports and the operator's broker
connection. Confirm distinct results and no failure alerts for normal pause
or idle. Apply the base deployment without monitoring CRDs. Record the
release, startup boundaries, scrape interval, alert delays, and recovery
times when the drill runs.

## References

Prometheus documents [instrumentation](https://prometheus.io/docs/practices/instrumentation/)
and [metric naming](https://prometheus.io/docs/practices/naming/).
Use event timestamps for freshness and keep labels bounded by the managed
`Player` inventory.
