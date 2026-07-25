# The liken.sh website

Milestone 25 — Completed. The liken.sh domain serves one static web page
from the project's own cluster.

The domain already serves code, because every CRD lives under
liken.sh/v1alpha1, and it already serves machines, because
releases.liken.sh feeds the fleet. This milestone makes it serve people.
The page says what liken is and where to start reading. The project's
own cluster serves the page, on the same nanode machine that upgrades
itself from the channel.

## The page

The site is one hand-written HTML file, with no generator and no
framework. The site has one page of content, and a build step would put
machinery in front of almost nothing. `liken.sh/website/index.html` is
the whole site.

The text is short and practical. The page states what liken does. It
states that the kernel and k3s come from their own upstream releases,
and that this repository contributes the assembly. It states one proof:
a liken cluster built from the repository that the page points at serves
the page. The word `liken` is in code face everywhere it appears,
because it names the code.

## How the site deploys

One requirement shaped this design. A person must be able to publish a
page without a rebuild of an image and without a reboot of a machine.
The OS ships through the release channel on its own schedule. The
website is a workload, and a workload reaches a liken cluster through
the Kubernetes API.

The site is a set of ordinary Kubernetes resources
(`liken.sh/website/manifests/`), applied directly to the cluster. nginx
serves a ConfigMap behind Traefik, in a `website` namespace of its own.
Resources applied through the API stay in the cluster's datastore, on
the data disk, and a reboot or a release roll does not touch that disk.
A deploy does not need the OS, and the OS does not disturb a deploy. The
auto-deploy manifests directory is not used. init resets that directory
to the image's seeds on every boot, so a file put there needs an image
rebuild to survive. That is the coupling this milestone removes.

Terraform stops at Linode's edge, at the DNS records and the firewall,
and never speaks to Kubernetes. Terraform's kubernetes provider fetches
the API server's full OpenAPI document on every plan, and milestone 31
measured that as memory pressure on the 1 GB node. kubectl applies the
same resources without that cost.

A content deploy is the fastest loop. The page mounts as a
whole-directory ConfigMap volume, so the kubelet refreshes it in place
about one minute after the ConfigMap changes. `make website-content`
regenerates the ConfigMap from the HTML file and also restarts the
deployment, so a deploy has a definite end that a person can verify.

## Publishing from CI

A push to main that changes the site deploys it.
`.github/workflows/website.yaml` runs `make website-content` and
compares the served page byte for byte against the commit. This needs a
cluster credential in GitHub's secret store, and it needs port 6443 open
to the internet, because GitHub's runners have no addresses that a
firewall rule can pin. The cost of this trade is known. The credential's
user is bound to the website's namespace alone, so a leak or a
compromised runner can change the page and nothing more, and the API
authenticates every request before it answers. Manifest changes stay
outside CI. A person deploys them from a workstation, with `make
website` and the admin credential.

The credential is a client certificate for the user `website-deployer`.
Terraform mints it offline from the cluster's client CA, the same way it
computes the admin kubeconfig from identity/. It reaches the repository
as `WEBSITE_KUBECONFIG`, like every other CI secret. The only part of it
on the cluster is the RoleBinding that gives the name its narrow
permissions, so one deletion revokes it.

## TLS

TLS does not change from milestone 31: Traefik's built-in ACME, two
resolvers, and acme.json on a small persistent volume. It is now a plain
manifest (`website/manifests/traefik-tls.yaml`) instead of a
terraform-applied object, for the OpenAPI reason above. The operational
notes are in `liken.sh/website/README.md`, next to the thing they
describe: DNS before the first issuance, the one-time helm re-render,
and the change from staging to production.

## What comes next

Milestone 26's release pages and milestone 27's documentation build on
this page and this deploy path. They add more files to the same
ConfigMap and nginx arrangement, until the site becomes too large for
it. One page, and a few pages after it, are not too large for it.
