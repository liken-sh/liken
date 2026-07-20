# Choosing the bundled components

Milestone 19 — Folded into milestone 17

The static k3s configuration disables traefik, servicelb, and
metrics-server, as a rule: anything beyond the control plane should be
a declared, visible workload. Some deployments reasonably want k3s's
bundled versions instead. That choice should belong to the deployment,
not be fixed in the image.

This capability merged into milestone 17's opt-in feature vocabulary.
The bundled components are three slugs in the Cluster's
spec.features, alongside the vendored payloads. Opting into one of
them removes it from the disable list that init renders into the k3s
boot drop-in. The design, and the reasoning for placing the
declaration on the Cluster (the disable list has an effect across the
whole cluster, as milestone 16 showed), are in
[17-network-storage-clients.md](17-network-storage-clients.md). This
milestone keeps its number so that the survey which produced
milestones 17 through 21 reads in the order it happened.
