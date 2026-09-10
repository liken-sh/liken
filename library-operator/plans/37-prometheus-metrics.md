# Prometheus metrics

Plan 37. Proposed.

The contract for every metric here, the three layers, the names, the
port table, and the monitoring component, is [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/65-prometheus-metrics.md).
This plan states only what this operator adds.

## The problem

A scan that stopped finishing, a provider that answers 429, and a
browser that spins while covered are all things that happened and were
found late. The catalog's Corrosion already serves Prometheus metrics;
nothing else in this repository does.

## The design

Layers 1 and 2 under the `library_` prefix, the operator on port 9230.
The media browser serves on 9231 under `library_browser_`, with the
`metrics` facade and the Prometheus exporter, and has no layer 2.
Corrosion keeps its own `corro_*` names; the component scrapes its port
and this repository owns nothing there.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| library-operator | `library_scan_duration_seconds{library}` | histogram | a scan that got slow |
| library-operator | `library_scan_last_success_timestamp_seconds{library}` | gauge | a scan that stopped finishing |
| library-operator | `library_items{library, kind}` | gauge | catalog size over time |
| media-browser | `library_browser_frame_seconds` | histogram | the covered-spin problem as a graph |
| media-browser | `library_browser_art_cache_bytes` | gauge | the RSS problem as a graph |
| catalog | upstream `corro_*` | upstream | sync and change health |

The `library` label is the Library resource's name, never a title or a
path.

Three rows from the first draft of this plan wait on a process that
can serve them. `library_provider_requests_total{provider, status}` and
`library_identify_total{provider, outcome}` happen inside the enrich
Job's init containers, which exit when their work is done, so no
listener there would be scraped; the catalog's `attempts` table holds
the latest result per item, not an event log, so the operator cannot
count them either. `library_progress_writes_total{source}` happens in
the progress store pod and the Jellyfin sync, which the operator sees
only as a collapsed mark on the bus. Each needs its own counter in the
process that does the work, and a listener or a push path for it.

The monitoring component at `deploy/monitoring/` holds a PodMonitor for
the operator, one for the catalog pod naming Corrosion's port, and one
for the browser.

## Proof

Failing tests first. On liken-1, run a scan and read its duration bar;
open the browser under a film and read the frame histogram flatten.