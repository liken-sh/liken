# TLS for the website

Milestone 31 — Not started

liken.sh serves its landing page over plain HTTP. A public site in
2026 owes its readers HTTPS, and the release channel this site will
grow (milestone 26) makes it non-negotiable: digest-verified
downloads still deserve a transport nobody can tamper with, and
browsers increasingly refuse to fetch anything serious without it.
Let's Encrypt is the obvious issuer — free, automated, and designed
for exactly this kind of unattended renewal.

The question is what does the ACME dance, on a cluster with less
than half a gigabyte of memory to spare. The Kubernetes-native
answer is cert-manager: a controller (three, actually — the manager,
a webhook, and a CA injector) that watches Certificate resources and
keeps Secrets renewed. It is the right answer for a fleet that
issues many certificates, and probably the wrong one for a one-node
nanode that needs exactly one: three always-on pods spending memory
to automate what one component could do in-process.

The frugal candidate: Traefik — already running as the cluster's
declared ingress — has ACME support built in. A certificate resolver
in its static configuration (k3s exposes this as a HelmChartConfig
overlay) lets Traefik answer the HTTP-01 challenge itself and renew
in-process, costing zero additional pods. The catch is storage:
Traefik keeps its certificates in a JSON file, so it needs a small
persistent volume (the local-path provisioner is already there), and
that file is per-replica — fine at one replica, a real problem only
if Traefik ever scales out, which on this cluster it won't.

Open questions, deliberately unanswered here: whether the
HelmChartConfig overlay belongs in the deployment's terraform (where
the website lives today) or grows into liken's feature vocabulary as
traefik configuration; how the HTTP-01 challenge interacts with the
registrar's NS delegation, which must land first (Let's Encrypt has
to resolve liken.sh to this node before it will issue anything); and
whether the redirect story (HTTP to HTTPS, www to apex) lives in
Traefik middleware or stays out of the way until the site has more
than one page.
