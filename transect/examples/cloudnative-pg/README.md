# CloudNativePG

`KindKnowledge` resources for CloudNativePG's `Cluster` and the kinds that its operator reconciles.
With them, the page of a `Cluster` names the primary pod and the `Service`s. When a machine loss takes the primary, the loss page says that writes stop until a replica is promoted. The `PodDisruptionBudget` that the operator creates to allow no pod disruptions is drawn as expected.
