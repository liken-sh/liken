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

## The redirect host

Each operator's API group, such as `bluetooth.liken.sh`, and each CSI
driver's name, such as `git.liken.sh`, is also the address of its
manual, because a cluster shows those names. The manuals are sections
of the one site, so a small machine answers each name with a 301 to
`https://liken.sh/<name>/`, with the same path and query.
`redirects.tf` declares the machine, and its comments explain each
choice.

The machine is a Linode Nanode in `us-east` that runs Flatcar
Container Linux. Its only state comes from one Ignition config, which
Terraform renders from `redirects.bu` and the `Caddyfile` and passes
as the instance's user data. The config starts one container, the
official Caddy image, pinned by digest. Caddy gets and renews a Let's
Encrypt certificate for each name in `redirect_names` over HTTP-01 or
TLS-ALPN-01, so the host holds no DNS token. A firewall passes only
TCP ports 80 and 443.

A change to the Flatcar image, the Butane file, the `Caddyfile`, or a
variable replaces the instance. Nothing on the host changes by hand.
A new instance asks Let's Encrypt for a new certificate for each
name in `redirect_names`, nine by default. In any seven days, Let's
Encrypt issues at most 5 certificates for one name and at most 50
for all of liken.sh, so replace the host at most a few times a week.

Flatcar updates itself on the stable channel and reboots five
minutes after each update. The pinned release below is only the
release that a new instance boots first.

### Download the image before a plan

Terraform uploads the Flatcar image from `flatcar/`, which is not in
history. Download it once in each checkout, and again after the pin
moves:

    make flatcar

The target checks the download against the SHA-512 in the `Makefile`
before it writes `flatcar/version`, the file that tells Terraform
which image to upload.

### Move the Flatcar release

Read the new version from
`https://stable.release.flatcar-linux.net/amd64-usr/current/version.txt`.
Then check the signature on that release's digests with Flatcar's
image signing key, whose ID is `E25D9AED0593B34A`
(`https://www.flatcar.org/security/image-signing-key/`):

    base=https://stable.release.flatcar-linux.net/amd64-usr/<version>
    curl -LO $base/flatcar_production_akamai_image.bin.gz.DIGESTS.asc
    gpg --verify flatcar_production_akamai_image.bin.gz.DIGESTS.asc

Put the version and the SHA-512 line from that file into
`FLATCAR_VERSION` and `FLATCAR_SHA512` in the `Makefile`. Then run
`make flatcar` and `terraform apply`. The new image replaces the old
one and the instance.

### Move the Caddy release

Set `caddy_image` in `redirects.tf` to the new tag and the digest of
its multi-platform index. Docker Hub lists the digest beside each
tag of `library/caddy`, and this command prints it:

    docker buildx imagetools inspect docker.io/library/caddy:<tag>

### Add a name

Add the name to `redirect_names` in `redirects.tf`. The wildcard
records send every subdomain without a record of its own to the host,
but a record of its own always wins over the wildcard. So a name
redirects only when no other record in `terraform.tf` claims it. The
names in `extension_operators` each have a CNAME to GitHub Pages,
and each of them reaches the host only after its CNAME is removed.

### No SSH

The host has no SSH: sshd is masked, the core user has no keys, and
the firewall drops port 22. In an emergency, open the instance's
Lish console in Linode's Cloud Manager and reboot the instance. Lish
shows the serial console. When the GRUB menu appears, press `e` on
the default entry, add `flatcar.autologin` to the kernel command
line, and boot. The console then logs in as core without a password.
The better repair is almost always a new instance: change what is
wrong in this directory, and apply.
