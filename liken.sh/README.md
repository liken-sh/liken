# liken.sh

This directory has the Terraform for the project's public names and
its release channel. When you run `terraform apply` here, you get the
liken.sh DNS zone and the bucket that serves
`https://releases.liken.sh`. You also get the credentials that CI uses
to publish to the channel and to renew its certificate, and the public
settings of each repository of the liken-sh organization.

The Terraform manages the records of the zone, the channel's bucket,
and the keys and tokens that it stores in the GitHub repository as
Actions secrets. It also manages each repository's description,
homepage, topics, Pages site, and merge settings. It does not build
the website or the releases. The website is built and deployed as
`liken/docs/README.md` describes, and `liken/releases/` builds and
publishes each release. No CI job applies this Terraform. The
providers read `LINODE_TOKEN` and `GITHUB_TOKEN` from the environment,
so no credential is in these files.

The OS's publish job uploads each release to the bucket with the key
that this Terraform creates. Each `liken` machine and the `liken`
toolkit download releases from the channel. Names such as
`display.liken.sh` are the operators' API groups and DRA or CSI
driver names. They have no records in the zone.

## The parts

- **The zone.** Linode is the authoritative nameserver for liken.sh.
  The zone has the records for the website, the channel, the devlog at
  `log.liken.sh`, and GitHub's verification of the organization.
- **The release channel.** The channel is a Linode Object Storage
  bucket named `releases.liken.sh`. It has that name because Linode's
  TLS for a custom domain finds the bucket by its name. The channel is
  not on one of the cluster's machines, because the machines upgrade
  from it, and it must stay available when any of them is gone. Linode
  serves the bucket over HTTPS with a certificate that you upload. So
  each month, `.github/workflows/releases-cert.yaml` gets a Let's
  Encrypt certificate with a DNS-01 challenge on this zone, and
  uploads the certificate to the bucket.
- **The website's names.** GitHub Pages serves liken.sh. The apex of
  a zone cannot be a CNAME. So the apex has A and AAAA records with
  the Pages addresses that GitHub publishes, and `www` is a CNAME to
  the organization's Pages hostname. This Terraform makes no
  certificate for the site, because GitHub Pages issues it.
- **The repositories.** `repositories.tf` has one entry for each
  repository of the organization. When an entry names a repository
  that does not exist, Terraform creates it. `prevent_destroy` stops
  Terraform from deleting a repository.

The channel has this layout:

    https://releases.liken.sh/channel.yaml
    https://releases.liken.sh/<version>/release.yaml
    https://releases.liken.sh/<version>/<artifact>

`channel.yaml` records the newest version. `cluster-operator` reads it
and reports the newest release in the `Cluster`'s status. A machine
installs a release only when the `Cluster`'s catalog lists its version
and the digest of its `release.yaml`. So the trust is in the
`Cluster`, not in the channel. `liken index` renders index pages that
list each release.
