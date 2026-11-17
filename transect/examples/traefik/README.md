# Traefik

An `IngressController` for Traefik, with the queries that read each backend's requests, p95 latency, and 5xx rate, and kind knowledge for its `IngressRoute` and `Middleware`.
With it, an `Ingress`'s entry box names the workload that serves it and shows its traffic, Ways in gives each `Ingress` host a row per port Traefik serves it on, with TLS on the port that serves it, and an application behind a forward-auth `Middleware` authenticates through what the middleware asks.
