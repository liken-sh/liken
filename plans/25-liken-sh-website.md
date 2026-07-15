# A website on liken.sh

Milestone 25 — Landed 2026-07-15

The domain already answers for code (every CRD lives under
liken.sh/v1alpha1) and for machines (releases.liken.sh feeds the
fleet); this milestone makes it answer for people: what liken is, and
where to start reading. One static page, served by the project's own
cluster — the same nanode that upgrades itself from the channel.

## The page

One hand-written HTML file, no generator, no framework: the site has
exactly one page's worth of things to say, and a build step would be
machinery in front of nothing. `liken.sh/website/index.html` is the
whole site.

The voice is pragmatic and brief. The page says what liken does, is
honest that the kernel and k3s come from their upstream releases —
the assembly is what this repository contributes — and lets the one
proof point land plainly: the page is served by a liken cluster built
from the repository it points at. The word `liken` renders in code
face everywhere it appears, because it names the code.

## The deploy story

The requirement that shaped everything: publishing a page must never
mean rebuilding an image or rebooting a machine. The OS ships through
the release channel on its own rhythm; the website is a workload, and
workloads reach a liken cluster through the Kubernetes API.

So the site is ordinary Kubernetes resources
(`liken.sh/website/manifests/`), applied straight to the cluster:
nginx serving a ConfigMap behind Traefik, in a `website` namespace of
their own. Resources applied through the API live in the cluster's
datastore on the data disk, which reboots and release rolls never
touch — deploys don't need the OS, and the OS doesn't disturb
deploys. The auto-deploy manifests directory was the alternative and
is deliberately not used: init resets it to the image's own seeds
every boot, so anything placed there would need an image rebuild to
survive — exactly the coupling this milestone exists to avoid.

Terraform stops at Linode's edge (the DNS records; the firewall) and
never speaks Kubernetes: its kubernetes provider fetches the API
server's entire OpenAPI document on every plan, which milestone 31
measured as real memory pressure on the 1 GB node. kubectl applies
the same resources without the ceremony.

Content deploys are the tightest loop. The page mounts as a
whole-directory ConfigMap volume, so kubelet refreshes it in place
within about a minute of the ConfigMap changing; `make
website-content` regenerates the ConfigMap from the HTML file and
restarts the deployment anyway, because a deploy should have a
definite end a deployer can verify against.

## Publishing from CI

A push to main that touches the site deploys it
(`.github/workflows/website.yaml` runs `make website-content` and
byte-compares the served page against the commit). That put a cluster
credential in GitHub's secret store and opened 6443 to the world —
GitHub's runners have no pinnable addresses — which is a trade taken
with eyes open: the credential's user is bound to the website's
namespace alone, so the worst a leak or a compromised runner spends
is the page itself, and the API authenticates everyone before
answering anything. Manifest changes stay outside CI's reach; those
deploy from a workstation with `make website` and the admin
credential.

The credential is a client certificate for the user
`website-deployer`, minted by terraform offline from the cluster's
client CA — the same way the admin kubeconfig is computed from
identity/ — and delivered to the repository as `WEBSITE_KUBECONFIG`
like every other CI secret. Nothing about it lives on the cluster
except the RoleBinding that gives the name its narrow meaning, which
also makes revocation one deletion away.

## TLS

Unchanged from milestone 31 — Traefik's built-in ACME, two resolvers,
acme.json on a small persistent volume — but delivered now as a plain
manifest (`website/manifests/traefik-tls.yaml`) instead of a
terraform-applied object, for the OpenAPI reason above. The
operational caveats (DNS before the first issuance, the one-time helm
re-render, the staging-to-production flip) live in
`liken.sh/website/README.md`, next to the thing they describe.

## What waits

Milestone 26's release pages and milestone 27's documentation
arrangement build on this page and this deploy path: more files in
the same ConfigMap-and-nginx arrangement until the site outgrows it,
which one page and a handful more do not.
