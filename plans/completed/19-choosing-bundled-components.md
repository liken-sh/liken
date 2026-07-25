# Choosing the bundled components

Milestone 19 — Completed. Milestone 17 absorbed this milestone: a
cluster opts into k3s's bundled components through spec.features.

The static k3s configuration disables traefik, servicelb, and
metrics-server, as a rule: anything beyond the control plane must be a
declared, visible workload. Some deployments want k3s's bundled
versions instead. That choice belongs to the deployment, and must not
be fixed in the image.

Milestone 17 absorbed this capability into its opt-in feature
vocabulary. The bundled components are three slugs in the Cluster's
spec.features, beside the vendored payloads. An opt-in to one of them
removes it from the disable list that init renders into the k3s boot
drop-in. [17-network-storage-clients.md](17-network-storage-clients.md)
has the design, and the reason to put the declaration on the Cluster:
the disable list has an effect across the whole cluster, as milestone
16 showed. This milestone keeps its number so that the survey which
produced milestones 17 through 21 reads in order.
