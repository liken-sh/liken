# The liken.sh domain

This directory is the project's public presence: the liken.sh DNS
zone, the release channel at `https://releases.liken.sh`, and the
records that point liken.sh at the website's host, and the host that
redirects the old manual names. `terraform.tf` and `redirects.tf`
declare all of it, and their comments explain each choice. The
website's content and deploy belong to the docs domain
(`liken/docs/README.md`); what the site needs from here is only its DNS.

## The release channel

The channel is a Linode Object Storage bucket, served over HTTPS at
its own name. The channel is deliberately not a machine: machines
upgrade themselves from the channel, so the channel must outlive any
machine that it feeds. If a cluster served its own updates, a
failure could leave nothing to rescue it.

The layout is exactly what liken's fetcher expects, and it is easy
to browse by hand:

    https://releases.liken.sh/channel.yaml
    https://releases.liken.sh/<version>/release.yaml
    https://releases.liken.sh/<version>/<artifact>

`channel.yaml` is the channel's one mutable object. liken's releases
are linear, so the root document only records the newest version,
and clusters poll it to learn when a newer version exists. This
document is advisory only. *Adopting* a release still means a
Cluster edit that names the version and pins the release document's
digest, so trust travels through the Cluster, never through the
channel. Beyond that one pointer, nothing lists the contents of the
bucket. The publishing workflow's run summary prints the digest for
each release.

Two Linode details are worth recording here, so nobody has to
rediscover them. First, the bucket is *named* `releases.liken.sh`,
because that is how Linode's custom-domain TLS finds a bucket: the
name CNAMEs to the bucket's own hostname. Second, Linode has no ACME
service of its own. Because of this, a scheduled workflow
(`.github/workflows/releases-cert.yaml`) mints a fresh Let's Encrypt
certificate every month, by DNS-01 against the zone declared here,
and uploads it to the bucket.

## The website's names

GitHub Pages serves liken.sh. An apex name cannot be a CNAME, and
Linode's object storage serves custom domains only on subdomains, so
the website and the channel answer from different hosts: the apex
carries GitHub's published Pages addresses as plain A and AAAA
records, and www CNAMEs to the organization's Pages hostname.
GitHub issues and renews the site's certificate, so only the
channel's name needs the certificate workflow above.

## The redirects

Each operator's API group, such as `bluetooth.liken.sh`, and each CSI
driver's name, such as `git.liken.sh`, is also the address of its
manual, because a cluster shows those names. The manuals are sections
of the one site, so each name answers with a 301 to
`https://liken.sh/<name>/`, with the same path and query.

Neither Linode DNS nor GitHub Pages serves a redirect, so a workload
in a Kubernetes cluster answers. `redirects/` is that workload as a
kustomize base: Caddy, its `Service`, and an `Ingress` that lists the
names. The cluster applies the base from this repository, for example
with a Flux `GitRepository` on this repository and a `Kustomization`
with the path `./liken.sh/redirects`. The cluster's ingress controller
ends TLS and passes plain HTTP to Caddy, so the cluster supplies the
certificate for the `redirects-tls` `Secret`, and Caddy holds no
state.

`redirects.tf` points the wildcard record at the cluster's address,
`redirects_ipv4`. A name with a record of its own never matches the
wildcard, so the apex, `www`, `releases`, `log`, and the verification
records keep their answers. A name that the `Ingress` does not list
gets a 404 over plain HTTP.

### Add a name

Add the name to the `Ingress` in `redirects/redirects.yaml`, in both
`tls.hosts` and `rules`. The name must have no record of its own in
`terraform.tf`, so that the wildcard sends it to the cluster.

### Check a name

    dig +short display.liken.sh
    curl -sI http://display.liken.sh/docs/
    curl -sI https://display.liken.sh/docs/

`dig` gives the cluster's address, and each `curl` answers with a 301
to `https://liken.sh/display/docs/`. `curl -v` shows a certificate
from Let's Encrypt.
