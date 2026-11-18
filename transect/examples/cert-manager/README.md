# cert-manager

`KindKnowledge` resources for cert-manager's `Certificate`, `ClusterIssuer`, and `Issuer`.
With them, the `Ingress` box on a workload's page shows which issuer provides its TLS certificate ("TLS from"). `transect` finds the issuer through the `Certificate` that writes the `Ingress`'s TLS `Secret`, or through the `Ingress`'s own annotations.
