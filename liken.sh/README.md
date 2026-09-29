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
TLS-ALPN-01, so the host holds no DNS token. Plain HTTP gets the same
301, so a name redirects before it has its certificate. A request for
any other name gets a 404. A firewall passes TCP ports 80 and 443 and
ICMP, and drops everything else.

### What replaces the host

A change to `flatcar.mk`, `redirects.bu`, the `Caddyfile`, or a
variable replaces the instance. Nothing on the host changes by hand.
The certificates are on the instance's disk, so a new instance asks
Let's Encrypt for a new certificate for each name in
`redirect_names`, nine by default. In any seven days, Let's Encrypt
issues at most 5 certificates for one name and at most 50 for all of
liken.sh, so replace the host at most a few times a week. A reboot
keeps the certificates.

The Caddyfile and the text of the Caddy unit reach the user data byte
for byte, so an edit there replaces the host, even an edit to
whitespace. Their explanations are in YAML comments in `redirects.bu`
and in `redirects.tf`, which do not reach the user data.

### The Flatcar release

`flatcar.mk` pins Flatcar 4459.2.4, and the pin sets only the release
that a new host boots first. Flatcar's automatic updates stay on:
about 10 minutes after the first boot, the host checks for an update
and installs the current stable release. It reboots five minutes
after the install, and it follows the stable channel from then on. So a pin that is months old does not
make an old host.

The pin is 4459.2.4 because it is the newest stable release with
Flatcar's first disk layout. Its image is 4,756,340,736 bytes
uncompressed, and Linode refuses an image larger than 6144 MiB. Later
releases use a larger layout, and their images are about 8 GB, so a
new pin must still have the first layout. An update does not change
the partitions, and Flatcar plans to keep updating the first layout
until at least 2030 (flatcar/Flatcar#1917).

A plan needs no download: `redirects.tf` reads the version and the
MD5 digest from `flatcar.mk`. Only an apply that creates the Linode
image uploads the file, so run this before such an apply, in each
checkout:

    make flatcar

The target downloads the image into `flatcar/`, which is not in
history. It checks the image against the SHA-512 digest in
`flatcar.mk`, and it checks that the image is small enough for
Linode.

To move the pin, check the signature on the new release's digests
with Flatcar's image signing key, whose ID is `E25D9AED0593B34A`
(`https://www.flatcar.org/security/image-signing-key/`):

    base=https://stable.release.flatcar-linux.net/amd64-usr/<version>
    curl -LO $base/flatcar_production_akamai_image.bin.gz.DIGESTS.asc
    gpg --verify flatcar_production_akamai_image.bin.gz.DIGESTS.asc

Put the version and the MD5 and SHA-512 lines from that file into
`flatcar.mk`. Then run `make flatcar` and `terraform apply`. The new
image replaces the old one and the instance.

### Move the Caddy release

Set `caddy_image` in `redirects.tf` to the new tag and the digest of
its multi-platform index. The image comes from ECR Public's copy of
Docker's official images, and this command prints the digest, which
is the same as Docker Hub's:

    docker buildx imagetools inspect public.ecr.aws/docker/library/caddy:<tag>

### Add a name

Add the name to `redirect_names` in `redirects.tf`. The wildcard
records send every subdomain without a record of its own to the host,
but a record of its own always wins over the wildcard. So a name
redirects only when no other record in `terraform.tf` claims it.

### Move the operator names to the host

The nine names in `extension_operators` in `terraform.tf` each have a
CNAME to GitHub Pages, which serves the archived repositories'
manuals. While a CNAME exists, requests for the name go to Pages, and
Let's Encrypt cannot reach the host to check the name. Caddy retries
a failed certificate with a longer wait each time, up to six hours
between attempts, and it stops after 30 days until it restarts. A
reboot restarts it, and so does each Flatcar update. So move the
names in this order:

1. Run `make flatcar` and `terraform apply`. The apply creates the
   host and the wildcard records.
2. Read the host's ID and addresses with
   `terraform output redirect_host`, and check that the host
   redirects plain HTTP:

       curl -sI -H 'Host: display.liken.sh' http://<ipv4>/docs/

   The answer is a 301 to `https://liken.sh/display/docs/`.
3. Check that each target section exists, for example
   `curl -sI https://liken.sh/display/` answers 200.
4. Remove the names from `extension_operators`, and apply.
5. Wait for the old CNAMEs to expire, five minutes, until
   `dig +short display.liken.sh` gives the host's address.
6. Reboot the instance in Linode's Cloud Manager, or with
   `linode-cli linodes reboot <id>`, so Caddy asks for the
   certificates at once. Skip this step when the host is less than
   an hour old: Caddy's early retries are minutes apart.
7. Check each name over HTTPS:

       curl -sI https://display.liken.sh/docs/

   The answer is a 301 to `https://liken.sh/display/docs/`, and
   `curl -v` shows a certificate from Let's Encrypt.

### No SSH

The host has no SSH: sshd is masked, the core user has no keys, and
the firewall drops port 22. In an emergency, open the instance's
Lish console in Linode's Cloud Manager and reboot the instance. Lish
shows the serial console. When the GRUB menu appears, press `e` on
the default entry, add `flatcar.autologin` to the kernel command
line, and boot. The console then logs in as core without a password.
The better repair is almost always a new instance: change what is
wrong in this directory, and apply.
