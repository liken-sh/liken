# cert-manager

Kind knowledge for cert-manager's `Certificate`, `ClusterIssuer`, and `Issuer`.
With it, an `Ingress`'s entry box shows "TLS from" the issuer of its certificate, found through the `Certificate` that writes its TLS `Secret` or through the `Ingress`'s own annotations.
