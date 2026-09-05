# 07, Prometheus metrics

Proposed. No metrics or hardware drill are implemented by this plan.

## The problem

A controller can need charging while every operator pod is healthy. A
speaker can reconnect repeatedly without a pod restart. These are useful
things to observe over time, but sleeping controllers also disconnect as
part of normal operation.

[`radio.go`](../radio.go) reads BlueZ link and battery properties.
[`peripheral.go`](../peripheral.go) derives peripheral status, including
the `Connected` condition. [Plan 06](06-peripherals-and-the-input-relay.md)
describes the peripheral inventory and the kernel battery source. Metrics
must reuse the resulting observations and their unknown-state rules.

## The design

Expose peripheral battery and connection metrics through Prometheus.

| Signal | Meaning and use |
|---|---|
| Battery level | A gauge for the last known percentage from the existing battery observation. Supports a low-battery notification. |
| Peripheral connected | A gauge for the observed radio link. Supports diagnosis of an unavailable speaker or remote. |
| Observed disconnects | A counter for known connected-to-disconnected transitions. Shows frequent link loss without counting every reconciliation. |
| Observation health | Current source validity and last successful observation timestamps. Prevent stale peripheral status from appearing to be a current reading. |

Battery level is absent when neither source reports a level. Zero means
an observed empty battery. Preserve the existing source selection; metrics
must not add a competing BlueZ or kernel battery reader. Document that a
successful read of a cached battery value does not prove a new measurement
from the peripheral.

Use stable peripheral identity as the label. Target labels identify the
operator instance and node. Keep any source or reason labels to fixed
categories. Do not include aliases, pairing request IDs, claim UIDs, or
raw error messages. Peripheral names can encode hardware addresses, so the
endpoint remains internal to the cluster.

### Sleeping devices and demand

Disconnected is an observation, not an alert on its own. A bonded
controller can sleep while a standing remote pod holds its claim. A claim
alone therefore does not mean that its radio link must remain connected.

Keep the disconnect counter descriptive. Low-battery rules require a known
level and usable observation health. Persistent-connection alerts require
an explicit consumer expectation, such as an audio endpoint needed for
playback. Do not infer that expectation from device class or bond state.

Count only transitions observed while the source is valid. The first
snapshot, source loss, and reconnecting the operator to BlueZ do not count
as peripheral disconnects. The counter measures observed transitions; it
cannot recover radio events missed during an outage.

### Collection and recovery

Scrapes read an in-memory snapshot. They perform no D-Bus, sysfs, or API
calls and do not change scan frequency. Unknown source state must remain
distinguishable from a known disconnected peripheral.

Initialize validity before publishing device readings, and remove series
when a bond is removed. Process counters reset on restart. Prometheus's
`up` detects an unreachable exporter; source validity detects a reachable
exporter that cannot read the radio.

Provide a configurable, disableable `/metrics` listener with bounded HTTP
timeouts. Document internal scrape access. Put `PodMonitor` or
`ServiceMonitor` resources in an opt-in deployment overlay so the base
manifests require no monitoring CRDs. Pairing, bond restoration, and input
relay operation must not depend on successful scraping.

Final metric names, listener settings, and alert windows belong to the
implementation design.

## Considered and set aside

An external custom-resource collector could export battery and connection
state from `Peripheral` objects. Direct instrumentation can also count
observed disconnects between scrapes and report loss of its observation
source. It must derive gauges from the same facts as resource status.

Pairing-window duration, discovery counts, and inventory dashboards are
outside the first set. They need a concrete operational question before
adding more series. Audio readiness belongs to the audio operator.

## Proof

Write failing tests before implementation. Use real snapshots and a metric
registry to cover known zero battery, absent battery, each existing source,
source loss, reconnection, bond deletion, and initial collection. Repeated
snapshots and repeated scrapes must not increment disconnect counters.

On hardware with Prometheus, observe a battery-reporting controller and a
device with no battery report. Let a controller sleep and wake while its
remote pod remains running. Confirm that normal sleep causes no alert.
Exercise a low-battery rule with a fixture if hardware cannot reproduce it
without a long discharge; record which result was fixture-tested.

Interrupt the radio observation separately from a peripheral connection.
Confirm that the metrics distinguish those failures. Restart the operator,
remove a bond, and check initialization, counter resets, and series cleanup.
Apply the base deployment without monitoring CRDs. Record the release,
device capabilities, scrape interval, and measured transitions in the drill.

## References

Prometheus documents [instrumentation](https://prometheus.io/docs/practices/instrumentation/)
and [metric naming](https://prometheus.io/docs/practices/naming/).
Export successful-observation timestamps as Unix seconds. Keep series
bounded by the bonded peripheral inventory.
