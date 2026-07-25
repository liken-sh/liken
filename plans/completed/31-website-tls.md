# TLS for the website

Milestone 31 — Completed. liken.sh answers over HTTPS with a
certificate that Traefik gets from Let's Encrypt.

The milestone landed on 2026-07-12. Before it, liken.sh served its
landing page over plain HTTP. A public site must answer over HTTPS,
and the release channel that the site holds (milestone 26) makes
HTTPS necessary: a digest-verified download also needs a transport
that nobody can change, and browsers refuse more and more content that
arrives over plain HTTP. Let's Encrypt is the issuer, because it is
free, automatic, and made for unattended renewal.

The cluster has less than half a gigabyte of memory free, so the
choice of what performs the ACME exchange is important. The
Kubernetes-native answer is cert-manager: three controllers, the
manager, a webhook, and a CA injector, that watch Certificate
resources and keep Secrets renewed. That answer fits a fleet that
issues many certificates. It does not fit a one-node nanode that needs
one certificate, because three always-on pods use memory to automate
what one component does in its own process.

Traefik holds the certificate instead. Traefik already runs as the
cluster's declared ingress, and it has ACME support built in. The
arrangement needs one HelmChartConfig overlay and one ingress
annotation, both in the deployment's terraform, beside the website
they serve. This answers the first open question: this is deployment
configuration, not vocabulary that the OS must grow before a second
deployment needs it.

The overlay makes these requests, and the deployment gave these
results:

* **TLS-ALPN-01, not HTTP-01.** The challenge occurs in the TLS
  handshake on port 443, so it cannot collide with HTTP routing or
  with the redirect. The interaction that the plan expected does not
  exist. This answers the second open question.

* **Two resolvers, production and staging**, which are the same except
  for the CA that each one calls. They share acme.json, because the
  store uses the resolver name as its key, so each resolver keeps its
  own account and certificates. The staging issuance ran first, from
  end to end, before production used a real attempt. To move the site
  between the two resolvers, change one word in the ingress
  annotation. Traefik does not request a replacement certificate while
  a certificate for the host is in its store, whichever resolver owns
  it. The change takes effect only after you clear the outgoing
  resolver's `Certificates` entry in acme.json and restart Traefik, so
  that the incoming resolver finds no certificate.

* **A 128Mi volume for acme.json**, on the local-path provisioner,
  with fsGroup set to the chart's non-root UID. The certificate stayed
  through a node reboot during the milestone. One file per replica
  works with one replica, but it does not work at a larger scale.

* **The redirect is at the entrypoint.** Middleware was not necessary,
  which answers the third open question. A router that terminates TLS
  stops matching plaintext requests, so port 80 returns 404 without
  the redirect. The chart puts this setting under
  `ports.web.http.redirections`, and Helm's `with` skips a missing key
  and reports nothing. A value at `ports.web.redirections` renders
  nothing and reports nothing. If an overlay value has no effect,
  render the chart locally (`helm template` with the same values) and
  search the output for the expected argument.

The milestone also measured the node's memory envelope. The k3s server
process is the largest resident program on the node. It uses about
375Mi with a lean feature set, and 550Mi or more when the traefik
feature adds the helm controller and Traefik's CRDs. Pod accounting
does not show this memory, because k3s runs outside every pod cgroup.
On a 1GB machine, about 100Mi is free at idle, and a convergence event
then fails hard. One such event is a client that requests the API
server's full OpenAPI document, which terraform's
`kubernetes_manifest` does on every plan.

During these events, the datastore misses its IO deadlines, apiserver
handlers time out, and k3s's own embedded controllers lose their
leader leases and stop the whole process. The lab reproduced this: a
1GB guest with four vCPUs crash-looped for twenty minutes as it tried
to converge the traefik feature from an empty state, then converged in
three minutes with 1.5GB. The steady state fits a 1GB node, but
convergence to that state needs more memory headroom. A node that
booted recently has that headroom, because its caches are empty and
k3s is still small. A renewal is cheap. A re-render is not.
