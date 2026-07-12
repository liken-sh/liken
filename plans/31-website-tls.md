# TLS for the website

Milestone 31 — Landed 2026-07-12

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

The frugal candidate won: Traefik — already running as the cluster's
declared ingress — has ACME support built in, so it holds the
certificate itself. The whole arrangement is one HelmChartConfig
overlay and one ingress annotation, both in the deployment's
terraform next to the website they serve (the first open question
answered: this is deployment configuration, not vocabulary the OS
should grow until a second deployment wants it).

What the overlay asks for, and what the deployment taught:

* **TLS-ALPN-01, not HTTP-01.** The challenge happens inside the TLS
  handshake on port 443, so it cannot collide with HTTP routing or
  the redirect — the interaction the plan worried about simply never
  exists. That dissolved the second open question.

* **Two resolvers, production and staging**, identical but for the
  CA they call. They share acme.json — the store is keyed by
  resolver name, so each keeps its own account and certificates —
  and the staging issuance ran first, end to end, before production
  spent a real attempt. Moving the site between them is a one-word
  ingress-annotation edit, with one catch: Traefik will not request
  a replacement while any certificate for the host still sits in its
  store, whichever resolver owns it. The flip only takes effect
  after clearing the outgoing resolver's `Certificates` in acme.json
  and restarting Traefik, so the incoming resolver sees a genuine
  miss.

* **A 128Mi volume for acme.json**, on the local-path provisioner,
  with fsGroup matched to the chart's non-root UID. It proved itself
  the hard way: the certificate survived a mid-milestone node
  reboot. Per-replica file, one replica, fine here, never at scale.

* **The redirect at the entrypoint** (the third open question:
  middleware was never needed). A router that terminates TLS stops
  matching plaintext, so without it port 80 would 404. One gotcha
  worth recording: the chart nests this under `ports.web.http.
  redirections`, and Helm's `with` skips a missing key silently — a
  value placed at `ports.web.redirections` renders nothing and
  reports nothing. When an overlay value seems ignored, render the
  chart locally (`helm template` against the same values) and grep
  the args; that turned an invisible failure into a one-line fix.

The milestone's real lesson was about memory, not certificates. The
k3s server process is the node's dominant resident — roughly 375Mi
with a lean feature set, 550Mi and up once the traefik feature pulls
in the helm controller and Traefik's CRDs — and none of it appears
in pod accounting, because k3s runs outside every pod cgroup. On a
1GB machine that leaves ~100Mi free at idle, and convergence events
(a helm re-render, a client that requests the apiserver's full
OpenAPI document — terraform's `kubernetes_manifest` does exactly
that on every plan) do not fail politely: the datastore misses its
IO deadlines, apiserver handlers time out, and k3s's own embedded
controllers lose their leader leases and exit the whole process.
The lab reproduced it on demand: a 1GB guest with four vCPUs
crash-looped for twenty minutes trying to converge the traefik
feature from scratch, then converged in three at 1.5GB. The
steady state fits a 1GB node; converging toward it wants headroom,
and a freshly booted node — caches empty, k3s still small — is
where that headroom lives. Renewals are cheap; re-renders are not.
