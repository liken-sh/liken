# Traefik

An `IngressController` for Traefik, with the queries that read each backend's request rate, p95 latency, and 5xx rate, and `KindKnowledge` resources for its `IngressRoute` and `Middleware`.
With them, the `Ingress` box on a workload's page names the workload that serves it and shows its traffic. The "Ways in" page at `/access` gives each `Ingress` host one row for each port that Traefik serves it on, with TLS on the ports that use it. An application behind a forward-auth `Middleware` is shown as authenticating through the service that the middleware calls.
