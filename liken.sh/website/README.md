# The website

The site is served by the liken.sh cluster as ordinary Kubernetes:
nginx behind Traefik, with the whole site baked into a container
image. The content lives in the docs domain at the repository root
(`docs/README.md` explains it); this directory holds the manifests
that serve it, and the comments in each file explain the shape. This
README adds the deploy story: how a change reaches liken.sh, and the
few operational facts that a future deployer would otherwise have to
rediscover the hard way.

## How a change ships

Two paths, two halves of the site:

* **Content ships through CI.** A push to main that touches the docs
  domain runs `.github/workflows/docs.yaml`. The workflow builds the
  site, pushes it to ghcr.io as `ghcr.io/liken-sh/website`, and
  patches the Deployment to the new tag. Editing a page and pushing
  it is the whole publishing workflow.
* **Manifests ship with `make website`, from a workstation.** CI's
  credential deliberately cannot touch them. Use this path for
  changes under `manifests/`, and to set up the site from nothing.

Neither path touches the OS. The resources live in the cluster's own
datastore on the data disk, so they survive reboots and release
rolls. Publishing a page reboots no machine.

The cluster pulls the image anonymously, so the `website` package on
ghcr.io must be public. A workstation apply resets the Deployment's
image to `latest`, which still serves the current site; the next
docs push pins it back to a commit's own tag.

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
