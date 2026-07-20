# A website on liken.sh

Milestone 25 — Landed 2026-07-15

The domain already serves code (every CRD lives under
liken.sh/v1alpha1) and machines (releases.liken.sh feeds the fleet).
This milestone makes it serve people too: what liken is, and where to
start reading. It adds one static page, served by the project's own
cluster, the same nanode machine that upgrades itself from the
channel.

## The page

The site is one hand-written HTML file, with no generator and no
framework. The site has exactly one page's worth of things to say, and
a build step would be machinery placed in front of nothing.
`liken.sh/website/index.html` is the whole site.

The voice is practical and brief. The page states what liken does,
states plainly that the kernel and k3s come from their own upstream
releases (the assembly is what this repository contributes), and
states the one proof point plainly: the page is served by a liken
cluster built from the repository it points at. The word `liken`
renders in code face everywhere it appears, because it names the
code.

## The deploy story

One requirement shaped everything here: publishing a page must never
require rebuilding an image or rebooting a machine. The OS ships
through the release channel on its own schedule. The website is a
workload, and workloads reach a liken cluster through the Kubernetes
API.

So the site consists of ordinary Kubernetes resources
(`liken.sh/website/manifests/`), applied straight to the cluster:
nginx serves a ConfigMap behind Traefik, inside a `website` namespace
of its own. Resources applied through the API live in the cluster's
datastore, on the data disk, which reboots and release rolls never
touch. Deploys do not need the OS, and the OS does not disturb
deploys. The alternative, the auto-deploy manifests directory, is
deliberately not used: init resets that directory to the image's own
seeds on every boot, so anything placed there would need an image
rebuild to survive. That is exactly the coupling this milestone exists
to avoid.

Terraform stops at Linode's edge (the DNS records, the firewall) and
never speaks to Kubernetes. Terraform's kubernetes provider fetches
the API server's entire OpenAPI document on every plan, and milestone
31 measured this as real memory pressure on the 1 GB node. kubectl
applies the same resources without that overhead.

Content deploys form the tightest loop. The page mounts as a
whole-directory ConfigMap volume, so the kubelet refreshes it in place
within about a minute of the ConfigMap changing. `make
website-content` regenerates the ConfigMap from the HTML file and
restarts the deployment anyway, because a deploy should have a
definite end that a deployer can verify against.

## Publishing from CI

A push to main that touches the site deploys it automatically.
(`.github/workflows/website.yaml` runs `make website-content` and
compares the served page byte for byte against the commit.) This
required putting a cluster credential in GitHub's secret store and
opening port 6443 to the world, because GitHub's runners have no
addresses that can be pinned in a firewall rule. This trade was taken
with full awareness of its cost: the credential's user is bound to the
website's namespace alone, so the worst that a leak or a compromised
runner can spend is the page itself, and the API authenticates
everyone before it answers anything. Manifest changes stay outside
CI's reach; those deploy from a workstation, using `make website` and
the admin credential.

The credential is a client certificate for the user
`website-deployer`, minted by terraform offline from the cluster's
client CA, the same way the admin kubeconfig is computed from
identity/. It is delivered to the repository as `WEBSITE_KUBECONFIG`,
like every other CI secret. Nothing about it lives on the cluster
except the RoleBinding that gives the name its narrow meaning, which
also makes revocation a single deletion.

## TLS

TLS is unchanged from milestone 31: Traefik's built-in ACME, two
resolvers, and acme.json on a small persistent volume. It is now
delivered as a plain manifest (`website/manifests/traefik-tls.yaml`)
instead of a terraform-applied object, for the OpenAPI reason given
above. The operational notes (DNS before the first issuance, the
one-time helm re-render, the staging-to-production flip) live in
`liken.sh/website/README.md`, next to the thing they describe.

## What waits

Milestone 26's release pages and milestone 27's documentation
arrangement build on this page and this deploy path. They add more
files to the same ConfigMap-and-nginx arrangement, until the site
outgrows it. One page and a handful more do not outgrow it.
