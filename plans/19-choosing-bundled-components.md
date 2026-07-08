# Choosing the bundled components

Milestone 19 — Folded into milestone 17

The static k3s config disables traefik, servicelb, and metrics-server
on principle: anything beyond the control plane should be a declared,
visible workload. Some deployments reasonably want k3s's bundled
versions instead, and that choice should be theirs to make rather than
hardcoded in the image.

This capability merged into milestone 17's opt-in feature vocabulary:
the bundled components are three slugs in the Cluster's spec.features,
alongside the vendored payloads, and opting in removes one from the
disable list init renders into the k3s boot drop-in. The design, and
the reasoning for putting the declaration on the Cluster (the disable
list is cluster-wide in effect, as milestone 16 showed), live in
[17-network-storage-clients.md](17-network-storage-clients.md). The
number stays so the survey that produced milestones 17 through 21
reads as it happened.
