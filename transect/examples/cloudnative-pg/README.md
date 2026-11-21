# CloudNativePG

`KindKnowledge` resources for CloudNativePG's `Cluster` and the kinds that its operator reconciles.
A `Cluster` fills the pattern `primary-with-replicas` of `transect`'s catalog, so its page draws the store: the `Service` for writes over the primary pod, the `Service` that selects the replicas over a stack of the replica pods, and what the pattern expects of them. When a machine loss takes the primary, the loss page says that writes stop until a replica is promoted. The `PodDisruptionBudget` that the operator creates to allow no pod disruptions is drawn as expected.
