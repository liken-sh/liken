# The liken.sh domain

This directory is the project's public presence: the liken.sh DNS
zone, the release channel at `https://releases.liken.sh`, and the
records that point liken.sh at the website's host. `terraform.tf`
declares all of it, and its comments explain each choice. The
website's content and deploy belong to the docs domain
(`docs/README.md`); what the site needs from here is only its DNS.

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
