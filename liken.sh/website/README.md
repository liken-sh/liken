# The website

The site is one static page (`index.html`), served by the liken.sh
cluster as ordinary Kubernetes: nginx behind Traefik, with the page
stored in a ConfigMap. `manifests/` holds everything the site needs,
and the comments in each file explain the shape. This README adds
the deploy story: how a change here reaches liken.sh, and the few
operational facts that a future deployer would otherwise have to
rediscover the hard way.

## How a change ships

Two paths, one destination:

* **CI, on every push.** A push to main that touches this directory
  runs `.github/workflows/website.yaml`. This workflow regenerates
  the ConfigMap from `index.html` and rolls the deployment. Editing
  the page and pushing it is the whole publishing workflow.
* **`make website`, from a workstation.** This is the full deploy:
  both the manifests and the content, using the admin credential. Use
  this path for manifest changes, since CI's credential deliberately
  cannot touch them, and to set up the site from nothing.

Neither path touches the OS. The resources live in the cluster's own
datastore on the data disk, so they survive reboots and release
rolls. Changing the page rebuilds no image and reboots no machine.

Content updates do not strictly need the restart that the targets
perform. The page mounts as a whole-directory ConfigMap volume, and
kubelet refreshes this volume in place within about a minute of the
ConfigMap changing. The restart exists so a deploy has a definite
end: when `rollout status` returns, the cluster is serving the new
page, and CI can check that it shipped what it intended.

## Order matters once

Setting up the site from nothing has one sequencing rule: **DNS
before TLS**. Traefik proves control of liken.sh to Let's Encrypt
with a TLS-ALPN-01 challenge — Let's Encrypt dials the name back on
port 443 to check this proof. Because of this, the apex records in
`terraform.tf` must resolve to the node before the first
`make website`. If they do not, the first issuance attempts fail
against a name that points nowhere.

Applying `traefik-tls.yaml` for the first time makes k3s's helm
controller re-render Traefik. This is the one expensive moment on a
1 GB node: convergence is where memory headroom runs out. A freshly
booted machine has the most headroom, because its caches are empty
and k3s is still small. Deploy soon after a boot, and watch it land
with `./kubectl -n kube-system get jobs -w`. Re-applying the file
unchanged does nothing, so routine deploys never pay this cost again.

## The resolvers, and flipping between them

`traefik-tls.yaml` defines two ACME resolvers, `letsencrypt`
(production) and `staging`. The ingress annotation in `website.yaml`
picks one. Staging is for rehearsal: it uses the same protocol,
untrusted roots, and generous rate limits. Watch for one trap when
you switch from staging to production: both resolvers share
`acme.json`, and Traefik will not discard a served certificate only
because the annotation changed. Clear the outgoing resolver's
`Certificates` entry in `acme.json` (on Traefik's `/data` volume) and
restart Traefik. Traefik will then issue a fresh certificate from the
new resolver.

## The CI credential

CI authenticates as the user `website-deployer`. terraform mints this
user's client certificate offline from the cluster's client CA in
`identity/`, the same way `make kubeconfig` mints the admin
credential, and delivers it to the repository's secret store as
`WEBSITE_KUBECONFIG`. No token lives on the cluster.
`manifests/deployer.yaml` defines the whole of what this name is
allowed to do. To rotate the credential, run
`terraform apply -replace=tls_private_key.website_deployer`. To
revoke it, delete the RoleBinding; the certificate still proves a
name, but that name means nothing afterward.

## Starting over

If the machine is ever refounded, meaning the instance and the data
disk are both torn down, the site comes back through the same two
steps that created it. First, run `terraform apply` for the DNS
records and the CI credential; both come from local state, and
neither needs a cluster. Second, run `make website`
once the name resolves. Everything else here is state that the
cluster carries for itself.
