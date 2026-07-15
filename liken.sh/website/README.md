# The website

One static page (`index.html`), served by the liken.sh cluster as
ordinary Kubernetes: nginx behind Traefik, the page in a ConfigMap.
`manifests/` holds everything the site needs; the comments in each
file explain the shape. What this README adds is the deploy story —
how a change here reaches liken.sh, and the few operational facts a
future deployer would otherwise rediscover the hard way.

## How a change ships

Two paths, one destination:

* **CI, on every push.** A push to main that touches this directory
  runs `.github/workflows/website.yaml`, which regenerates the
  ConfigMap from `index.html` and rolls the deployment. Editing the
  page and pushing is the whole publishing workflow.
* **`make website`, from a workstation.** The full deploy: manifests
  and content both, with the admin credential. This is the path for
  manifest changes — CI's credential deliberately can't touch them —
  and for standing the site up from nothing.

Neither path touches the OS. The resources live in the cluster's own
datastore on the data disk, so they survive reboots and release rolls,
and no image is rebuilt and no machine reboots to change the page.

Content updates don't even strictly need the restart the targets
perform: the page mounts as a whole-directory ConfigMap volume, which
kubelet refreshes in place within about a minute of the ConfigMap
changing. The restart is there so a deploy has a definite end — when
`rollout status` returns, the new page is what's being served, and CI
can check it saw what it shipped.

## Order matters once

Standing the site up from nothing has one sequencing rule: **DNS
before TLS**. Traefik proves control of liken.sh to Let's Encrypt
with a TLS-ALPN-01 challenge — Let's Encrypt dials the name back on
port 443 — so the apex records in `terraform.tf` must resolve to the
node before the first `make website`, or the first issuance attempts
burn against a name that points nowhere.

Applying `traefik-tls.yaml` for the first time makes k3s's helm
controller re-render Traefik, and that is the one expensive moment on
a 1 GB node: convergence is where memory headroom runs out, and a
freshly booted machine — caches empty, k3s still small — is where the
headroom lives. Deploy soon after a boot, and watch it land with
`./kubectl -n kube-system get jobs -w`. Re-applying the file
unchanged is a no-op, so routine deploys never pay this again.

## The resolvers, and flipping between them

`traefik-tls.yaml` defines two ACME resolvers, `letsencrypt`
(production) and `staging`; the ingress annotation in `website.yaml`
picks one. Staging is for rehearsal — same protocol, untrusted roots,
generous rate limits. One trap when flipping staging → production:
both resolvers share `acme.json`, and Traefik won't discard a served
certificate just because the annotation changed. Clear the outgoing
resolver's `Certificates` entry in `acme.json` (on Traefik's `/data`
volume) and restart Traefik, and it will issue fresh from the new
resolver.

## The CI credential

CI authenticates as the user `website-deployer`, with a client
certificate terraform mints offline from the cluster's client CA in
`identity/` — the same way `make kubeconfig` mints the admin
credential — and delivers to the repository's secret store as
`WEBSITE_KUBECONFIG`. No token lives on the cluster;
`manifests/deployer.yaml` is the whole of what the name is allowed
to do. Rotation is
`terraform apply -replace=tls_private_key.website_deployer`;
revocation is deleting the RoleBinding, which leaves the certificate
proving a name that means nothing.

## Starting over

If the machine is ever refounded — instance torn down, data disk and
all — the site comes back with the two acts that created it:
`terraform apply` for the DNS records and the CI credential (both
minted from local state, no cluster required), then `make website`
once the name resolves. Everything else here is state the cluster
carries for itself.
