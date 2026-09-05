# 37, Prometheus metrics

Proposed. No metrics or hardware drill are implemented by this plan.

## The problem

An operator pod can be healthy while scans fail or enrichment stops making
progress. Some incomplete metadata needs a provider retry; some needs a
person to choose an identity. The metrics should distinguish those cases.

[`report.go`](../report.go) already defines counts for gaps, unresolved
identities, choices awaiting a person, and conflicting writers. It also
reports the last walk and worker runs. [`runs.go`](../runs.go) records
worker start, finish, and failure. These facts provide a useful starting
point without adding a catalog query to every scrape.

## The design

Expose library progress and work that needs attention through Prometheus.

| Signal | Meaning and use |
|---|---|
| Outstanding enrichment | Gauges per library and configured fact for remaining work. Show whether eligible enrichment is progressing. |
| Identity and writer issues | Separate counts for unidentified folders, unresolved titles, choices awaiting a person, and writer conflicts. Distinguish automatic work from manual intervention. |
| Latest worker run | Start and finish timestamps, duration of the last completed run, and its failure state, by library and fixed worker category. Explain a failed or unusually long scan or enrichment run. |
| Last successful worker completion | A timestamp distinct from the latest attempt or finish. Detect repeated failure without letting each failed retry appear to restore health. |
| Observation health | Catalog/report source availability and freshness. Distinguish a quiet library from an operator reporting stale counts. |

Expose only configured facts in the outstanding-work set. Distinguish gaps
that can run now from work blocked by configuration, provider refusal, or a
requested refresh. `Gaps` alone does not describe all of that eligibility;
reuse the scheduling decisions and add report fields where needed.

An unchanged gap count does not prove a worker is stuck. New arrivals can
replace completed work, and a requested refresh can create work with no
missing fact. Use run state and completion evidence with gap counts.
Choices awaiting a person and conflicting writers should prompt review,
not repeated automatic retries disguised as a liveness alert.

### Successful work and fresh reports

`LastWalk` describes a walk, not a durable record of every worker's last
success. Do not derive success solely from that field, a recent attempt,
or an empty failure string on an unfinished run.

Persist the last successful completion where worker outcomes are recorded
if the latest run would otherwise overwrite it. Count completion only after
the catalog confirms the worker's result under the existing echo contract.
Document that worker success does not imply that every title was identified
or that every fact was filled.

Reports are retained MQTT snapshots. Receiving one after reconnect does
not prove that the reporter is running or that its catalog is current.
Use producer freshness and catalog availability to validate the counts.
If the current protocol cannot establish freshness during quiet periods,
add an explicit heartbeat with a documented interval. A metric scrape must
never trigger a catalog query or a worker run.

### Idle libraries and failures

A library with no eligible work can remain unchanged indefinitely. Alert
on overdue work only when the configured scan schedule, a requested refresh,
or eligible enrichment establishes that work is expected. A completed scan
of unchanged files is still successful work.

Missing initial observations are unknown. Do not export unknown counts as
zero or report an unobserved run as successful. Expose source validity and
last successful observation timestamps. An unavailable catalog or bus has a
separate alert from a failed worker. Prometheus's `up` covers an unreachable
exporter.

### Collection and labels

The operator exports its existing reports and scheduling facts through an
in-memory snapshot. Namespace, stable library name, and fixed worker or fact
categories bound the labels. Never label by title, file path, provider URL,
job name, run ID, or error text. Remove series when a library is deleted or
a fact is no longer configured.

Start with latest-run gauges and persisted success timestamps. The current
report contains the latest run per worker, so it cannot reconstruct exact
historical outcome counters or duration histograms after an observation gap.
Any later cumulative metrics need an explicit event or persistence contract.

Provide a configurable, disableable `/metrics` listener with bounded HTTP
timeouts and internal scrape access. Put `PodMonitor` or `ServiceMonitor`
resources in an opt-in deployment overlay so base manifests require no
monitoring CRDs. Scanning, enrichment, and browsing must work without
Prometheus. Final names, listener settings, and alert windows belong to the
implementation design.

## Considered and set aside

An external custom-resource collector could expose existing `Library`
status fields. Direct instrumentation also exposes scheduling eligibility
and observation validity without adding a status write for every scrape.
Metrics and resource status must use the same reports.

Catalog size dashboards, per-title enrichment series, provider request
telemetry, and browser rendering metrics are outside the first set.
Playback startup and failures belong to the media operator. Peripheral
battery belongs to the Bluetooth operator.

## Proof

Write failing tests before implementation. Use real report and run fixtures,
controlled time, and a metric registry. Cover an idle library, a successful
unchanged scan, an unfinished run, repeated failures after success, eligible
gaps, disabled facts, requested refresh, unresolved identities, and writer
conflicts. Failure must not advance the last-success timestamp.

Replay a retained report after reconnect and interrupt the catalog reporter.
Verify that neither stale counts nor a missing first report appears healthy.
Delete a library and disable a fact to check series cleanup. Restart the
operator and confirm that persisted success timestamps survive. Repeated
scrapes must make no catalog queries and start no work.

On a hardware test cluster with Prometheus, scan a small fixture library,
add a title that needs identification, and exercise a provider refusal.
Confirm that progress, manual attention, and failed work remain distinct.
Leave the library unchanged after completion and verify that it causes no
stalled-work alert. Apply base manifests without monitoring CRDs. Record
the release, reporting interval, alert windows, and observed recovery times
when the drill runs.

## References

Prometheus documents [instrumentation](https://prometheus.io/docs/practices/instrumentation/)
and [metric naming](https://prometheus.io/docs/practices/naming/).
Use Unix timestamps for successful completion and freshness. Keep series
bounded by libraries and their configured worker and fact categories.
