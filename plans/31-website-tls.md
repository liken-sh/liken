# TLS for the website

Milestone 31 — Landed 2026-07-12

liken.sh serves its landing page over plain HTTP. A public site in
2026 owes its readers HTTPS, and the release channel that this site
will grow (milestone 26) makes HTTPS non-negotiable: digest-verified
downloads still deserve a transport that nobody can tamper with, and
browsers increasingly refuse to fetch anything serious without it.
Let's Encrypt is the obvious issuer: free, automated, and designed for
exactly this kind of unattended renewal.

The question is what should perform the ACME exchange, on a cluster
with less than half a gigabyte of memory to spare. The
Kubernetes-native answer is cert-manager: a set of controllers (three,
in fact: the manager, a webhook, and a CA injector) that watch
Certificate resources and keep Secrets renewed. This is the right
answer for a fleet that issues many certificates, and probably the
wrong answer for a one-node nanode that needs exactly one certificate:
three always-on pods spending memory to automate what one component
could do in-process.

The frugal candidate won. Traefik, already running as the cluster's
declared ingress, has ACME support built in, so it can hold the
certificate itself. The whole arrangement needs one HelmChartConfig
overlay and one ingress annotation, both placed in the deployment's
terraform next to the website they serve. This answers the first open
question: this is deployment configuration, not vocabulary that the
OS should grow until a second deployment wants it.

The overlay makes three requests, and the deployment taught three
lessons:

* **TLS-ALPN-01, not HTTP-01.** The challenge happens inside the TLS
  handshake on port 443, so it cannot collide with HTTP routing or the
  redirect. The interaction that the plan worried about simply does
  not exist. This resolved the second open question.

* **Two resolvers, production and staging**, identical except for the
  CA each one calls. They share acme.json, because the store is keyed
  by resolver name, so each resolver keeps its own account and
  certificates. The staging issuance ran first, end to end, before
  production spent a real attempt. Moving the site between the two
  resolvers is a one-word ingress-annotation edit, with one catch:
  Traefik will not request a replacement certificate while any
  certificate for the host still sits in its store, no matter which
  resolver owns it. The flip only takes effect after clearing the
  outgoing resolver's `Certificates` entry in acme.json and restarting
  Traefik, so the incoming resolver sees a genuine miss.

* **A 128Mi volume for acme.json**, on the local-path provisioner,
  with fsGroup matched to the chart's non-root UID. This choice proved
  itself the hard way: the certificate survived a mid-milestone node
  reboot. A per-replica file works fine here, with one replica, but it
  would not work at scale.

* **The redirect sits at the entrypoint** (the third open question:
  middleware was never needed). A router that terminates TLS stops
  matching plaintext requests, so without the redirect, port 80 would
  return 404. One detail is worth recording: the chart nests this
  setting under `ports.web.http.redirections`, and Helm's `with`
  silently skips a missing key. A value placed at
  `ports.web.redirections` instead renders nothing and reports
  nothing. When an overlay value seems to be ignored, render the chart
  locally (`helm template` against the same values) and search the
  output for the expected argument. That step turned an invisible
  failure into a one-line fix.

The milestone's real lesson concerned memory, not certificates. The
k3s server process is the node's largest resident program, using
roughly 375Mi with a lean feature set, and 550Mi or more once the
traefik feature pulls in the helm controller and Traefik's CRDs. None
of this appears in pod accounting, because k3s runs outside every pod
cgroup. On a 1GB machine, this leaves about 100Mi free at idle, and
convergence events do not fail gently. (One such event: a client
requesting the API server's full OpenAPI document; terraform's
`kubernetes_manifest` does exactly that on every plan.) During these
events, the datastore misses its IO deadlines, apiserver handlers time
out, and k3s's own embedded controllers lose their leader leases and
exit the whole process. The lab reproduced this on demand: a 1GB guest
with four vCPUs crash-looped for twenty minutes trying to converge the
traefik feature from scratch, then converged in three minutes at
1.5GB. The steady state fits a 1GB node, but converging toward that
state needs extra memory headroom, and a freshly booted node, with
empty caches and k3s still small, is where that headroom exists.
Renewals are cheap. Re-renders are not.
