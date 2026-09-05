# 08, Prometheus metrics

Proposed. No metrics or hardware drill are implemented by this plan.

## The problem

An operator pod can be healthy while a claimed endpoint cannot produce
sound. A Bluetooth speaker can lose its link, or PipeWire can lose the
node for a connected output. Kubernetes pod health does not describe
either failure.

The operator already derives physical connection and PipeWire readiness
in [`sinkstatus.go`](../sinkstatus.go). It also reads the claim that holds
each endpoint. [`reconcile.go`](../reconcile.go) detects failed PipeWire
graph reads. These observations can support metrics without another
hardware reader.

## The design

Expose a small Prometheus metric set for endpoint availability and failed
control operations. Include capture endpoints under the same rules.

| Signal | Meaning and use |
|---|---|
| Endpoint connected and ready | Separate gauges for the existing `Connected` and `Ready` facts. Distinguish a missing physical connection from a missing PipeWire node. |
| Endpoint claimed | A gauge derived from the existing claim state. Combine it with availability to find endpoints that a workload needs but cannot use. |
| Control attempts and failures | Counters by a fixed operation category. Show repeated failures to apply requested endpoint settings, with attempts as the denominator. |
| Observation health | Successful-observation timestamps and current collection validity for the hardware and PipeWire sources. Distinguish an unavailable endpoint from an operator that cannot observe it. |

A claim indicates demand for the endpoint, but does not prove that audio
is streaming. Describe alerts as claimed-endpoint availability. Do not
claim to detect audible silence, audio underruns, or capture quality.

Reuse the same facts that derive resource status and device taints. A
scrape reads an in-memory snapshot and does not query PipeWire, BlueZ,
ALSA, or the Kubernetes API. Metrics add no hardware polling loop.

Count control operations where an attempt completes. An unchanged
reconcile pass adds no attempt. A failed graph read affects observation
health; it does not invent a failed volume write for every endpoint.

### Unknown state and recovery

A missing observation is unknown, not a disconnected endpoint. Publish
validity with the last successful observation timestamp. Availability
alerts require valid observations; observation loss has a separate alert.
An exporter that stops answering is detected through Prometheus's `up`.

Initial collection establishes state without counting a transition.
Process counters reset on restart. Remove device series when inventory
confirms departure, and document an absence check for expected endpoints
so removal does not silently resolve a claimed-device outage.

Use stable endpoint identity and direction as labels. Prometheus target
labels identify the operator instance and node. Do not label by claim UID,
pod UID, error text, track, or media URI.

### Optional collection

Provide a configurable, disableable `/metrics` listener with bounded HTTP
timeouts. Keep it internal to the cluster and document scrape access.
The operator must work with no Prometheus installation.

Keep `PodMonitor` or `ServiceMonitor` resources in an opt-in deployment
overlay. The base manifests must apply without those CRDs. Monitoring
failure must not block reconciliation, claim preparation, or daemon
supervision. Final names, listener settings, and alert windows belong to
the implementation design.

## Considered and set aside

Exporting resource status through an external custom-resource collector
could provide the availability gauges. Direct instrumentation also records
control attempts between scrapes and can report failed observations before
resource status changes. Keep one metric definition per fact.

Volume histories, codec inventories, generic reconciliation counts, and
PipeWire performance telemetry are outside the first set. Add them only
when a concrete diagnostic or alert needs them.

The Bluetooth operator owns peripheral battery and radio-link metrics.
This operator owns whether an audio endpoint is usable through PipeWire.
It must not export a second copy of peripheral battery data.

## Proof

Write failing tests before implementation. Use endpoint facts and a real
metric registry to test connected-without-node, disconnected, unclaimed,
unknown, recovered, and deleted endpoints. Repeated scrapes must leave
counters unchanged and must make no hardware calls.

On a hardware test cluster with Prometheus, claim an output and interrupt
its connection. Separately interrupt PipeWire observation. Confirm that
the two failures produce different metrics and that recovery clears them.
An unplugged, unclaimed output must not trigger the demand-based alert.
Repeat with a capture endpoint when suitable hardware is available.

Restart the operator, check counter resets and initial unknown state, and
remove a device to check series cleanup and expected-device absence.
Apply the base deployment without monitoring CRDs and confirm playback
still works. Record the release, scrape interval, alert delays, and measured
recovery times when the drill runs.

## References

Prometheus documents [instrumentation](https://prometheus.io/docs/practices/instrumentation/)
and [metric naming](https://prometheus.io/docs/practices/naming/).
Use event timestamps rather than continuously updated elapsed-time gauges,
and keep labels bounded by the managed endpoint inventory.
