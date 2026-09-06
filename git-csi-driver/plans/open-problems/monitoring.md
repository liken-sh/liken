# Monitoring

## The problem

The node plugin serves `git_csi_volume_abnormal` and the other gauges on
`--metrics`, and the drills read them with `curl`. Nothing in `deploy/`
tells a cluster's Prometheus to scrape them, and no alert rule ships.
A volume that cannot reach its forge reads `1` on a port nobody reads.

## What is known

- The DaemonSet already declares the metrics `containerPort`, so a
  `PodMonitor` is enough where the Prometheus operator runs.
- The base is for the cluster owner to take through their own GitOps.
  A `PodMonitor` in the base would fail to apply on a cluster without
  the operator's CRDs, so it belongs in a second kustomize component
  the owner adds.
- One rule covers the driver's whole promise: `git_csi_volume_abnormal
  == 1` for longer than the volume's `pull`, or the class's
  `push.maxLatency`, is a volume that is stale or cannot push.

## What would settle it

A `deploy/monitoring/` component with a `PodMonitor` and one
`PrometheusRule`, and a drill in a cluster with Prometheus that sees the
alert fire when the forge is stopped and clear when it returns.
