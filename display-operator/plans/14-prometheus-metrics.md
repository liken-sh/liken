# 14, Prometheus metrics

Proposed. No metrics or hardware drill are implemented by this plan.

## The problem

An operator pod can be healthy while a claimed display is disconnected,
the compositor is unavailable, or a panel refuses a requested setting.
These failures need different responses. A brightness graph alone does
not identify any of them.

[`displays.go`](../displays.go) defines connection status and the kernel
and Weston modes. [`controls.go`](../controls.go) checks panel control
readbacks. The compositor watch described in
[plan 12](completed/12-the-compositor-reports-its-outputs.md) provides
observations that a process-health probe cannot replace.

## The design

Expose display availability and failed control operations through
Prometheus, using the observations the operator already collects.

| Signal | Meaning and use |
|---|---|
| Output connected and compositor available | Separate gauges for the connector and the compositor. Distinguish cable or panel loss from compositor failure. |
| Output claimed | A gauge for whether any relevant allocation uses the output, including shared draw allocations. Allows alerts to account for demand. |
| Control attempts and failures | Counters by a fixed operation category for mode, brightness, and power requests. Show repeated failure to apply a requested setting. |
| Mode agreement | A gauge comparing known kernel and Weston modes. Identifies a persistent difference that can explain a mis-sized canvas. |
| Observation health | Source validity and last successful observation timestamps. Distinguish a failed read from a known unavailable output. |

Count operations where they complete. A retried hardware operation is
another attempt; re-exporting an unchanged status is not. Keep unsupported
controls and deferred operations distinct from failed writes. In particular,
a panel that intentionally stops answering after power-off must follow the
existing control semantics rather than manufacture a readback failure.

A mode comparison is unknown while either mode is unavailable. Allow a
settling interval after hotplug and requested mode changes before alerting
on disagreement. Do not encode resolution or refresh strings as labels.

### Demand and intentional darkness

A standing draw claim can remain allocated while the screen is intentionally
dark. Claimed does not imply that the panel must currently emit a picture.
Any availability rule must respect the effective power declaration and
`spec.override`, including intentional sleep and its wake transition.

The display operator reports whether it applied the requested state. The
media operator owns when a `Player` should sleep or wake. Do not copy that
idle policy into the metric collector.

### Collection and recovery

Scrapes read in-memory observations and allocations. They perform no DRM,
Wayland, DDC/CI, or Kubernetes API calls and add no hardware polling loop.
If an observation source fails, invalidate its current readings and retain
its last successful observation timestamp. An unreachable exporter is
separately detected through Prometheus's `up`.

Use stable output identity as a label and target labels for the node and
operator instance. Do not label by claim UID, monitor serial, mode string,
or error text. Shared claims contribute to one output's demand rather than
creating a series per allocation.

Initial collection establishes state. Process counters reset on restart.
Remove series when inventory confirms removal, and document an absence
check for expected outputs so a departed display does not appear repaired.

Provide a configurable, disableable `/metrics` listener with bounded HTTP
timeouts and documented internal scrape access. Keep `PodMonitor` or
`ServiceMonitor` resources in an opt-in deployment overlay. The base
manifests require no monitoring CRDs, and scraping must not affect claim
preparation, control writes, or compositor supervision.

Final metric names, listener settings, and alert windows belong to the
implementation design.

## Considered and set aside

An external custom-resource collector could export `Display` conditions
and mode agreement. Direct instrumentation also records control operations
between scrapes and reports source validity. Both status and metrics must
use the same observations and comparisons.

Brightness histories, mode-change counts, EDID inventories, and GPU
performance telemetry are outside the first set. Metrics do not prove that
a viewer received a frame or that a panel's backlight works.

## Proof

Write failing tests before implementation. Use observed display facts and
a metric registry to cover unknown modes, agreement, disagreement, shared
claims, deferred controls, unsupported controls, and failed writes. Repeated
scrapes must make no hardware calls and leave counters unchanged.

On hardware with Prometheus, interrupt a claimed display connection and
separately interrupt the compositor. Confirm that the metrics distinguish
them. Exercise a supported mode change and a refused control operation.
Check that a temporary mode transition does not trigger the sustained
mismatch alert.

Let the media layer darken and wake a screen while its draw claim remains
allocated. Confirm that intentional darkness causes no availability alert.
Restart the operator, remove an output, and check unknown state, counter
resets, series cleanup, and expected-output absence. Apply the base
deployment without monitoring CRDs. Record the release, panel capabilities,
scrape interval, alert delays, and measured recovery times in the drill.

## References

Prometheus documents [instrumentation](https://prometheus.io/docs/practices/instrumentation/)
and [metric naming](https://prometheus.io/docs/practices/naming/).
Export successful-observation timestamps as Unix seconds and keep series
bounded by the managed output inventory.
