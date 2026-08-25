# The liken.sh domain: the project's public presence, as
# infrastructure. This one file declares everything Linode needs for
# the name to answer: the DNS zone, the website's records, and the
# release channel. The release channel is the object storage bucket
# that holds published releases, the credential that lets this
# repo's CI upload them, and the token that keeps the channel's TLS
# certificate fresh.
#
# The channel lives in object storage, rather than on a liken
# machine, for a reason worth stating plainly: machines upgrade
# themselves from the channel, so the channel must outlive any
# machine it feeds. A cluster that served its own update channel
# could never be rescued by an update. The website is the same shape
# of thing, a tree of files built ahead of time, and GitHub Pages
# serves it at liken.sh; the DNS records below are everything the
# site needs from this file (docs/README.md tells the deploy story).
#
# Terraform fits here for the same reason that the Machine and
# Cluster documents fit the OS: the desired state is declared in
# files, and a reconciler (here, `terraform apply` run by a person)
# converges the world to match. Credentials stay out of these files
# entirely: the linode provider reads LINODE_TOKEN, and the github
# provider reads GITHUB_TOKEN, both from the environment, both
# exported by the repo's .envrc.

terraform {
  required_providers {
    linode = {
      source  = "linode/linode"
      version = "~> 3.0"
    }
    github = {
      source  = "integrations/github"
      version = "~> 6.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
    # The aws provider is here for exactly one resource: the release
    # bucket's website configuration. That setting exists only in the
    # S3 protocol (PutBucketWebsite). Linode's own API does not carry
    # it, and their provider does not model it, so this file points
    # the aws provider, the maintained Terraform client for that
    # protocol, at Linode's endpoint below instead.
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }

  # Terraform's state maps these declarations onto real resource IDs,
  # and it records secrets verbatim (the upload key below), so this
  # state can never be committed. It lives in its own private bucket
  # on Linode Object Storage, reached over the S3 protocol. That
  # bucket is the one piece of bootstrap that this file cannot create
  # for itself, because the state must exist somewhere before the
  # first apply. For this reason, someone creates it once by hand,
  # with an access key scoped to it alone. The key's two halves
  # travel in .envrc.private as the AWS_* variables that the S3
  # backend conventionally reads. The skip_* lines tell the backend
  # not to expect the parts of AWS that Linode's S3 compatibility
  # does not imitate.
  backend "s3" {
    bucket = "liken-sh-terraform-state"
    key    = "liken.tfstate"
    region = "us-east-1"

    endpoints = {
      s3 = "https://us-east-1.linodeobjects.com"
    }

    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
  }
}

provider "linode" {}

provider "github" {
  owner = "liken-sh"
}

# The aws provider, aimed at Linode's S3 endpoint rather than AWS.
# It authenticates with the same scoped upload key that CI publishes
# with, read straight from that key's resource, so no new credential
# exists. The skip_* lines tell it not to expect the parts of AWS
# that Linode's S3 compatibility does not imitate. Path-style
# addressing matters because the bucket's dotted name (the FQDN, for
# custom-domain TLS) breaks the wildcard certificate under
# virtual-host addressing.
provider "aws" {
  alias  = "linode_object_storage"
  region = "us-east-1"

  access_key = linode_object_storage_key.github_releases.access_key
  secret_key = linode_object_storage_key.github_releases.secret_key

  skip_credentials_validation = true
  skip_region_validation      = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true

  endpoints {
    s3 = "https://us-east-1.linodeobjects.com"
  }
}

# ---------------------------------------------------------------------------
# DNS: the liken.sh zone.
#
# Linode is the authoritative nameserver. The registrar's one job is
# to delegate here, by pointing the domain's NS records at
# ns1.linode.com through ns5.linode.com. That delegation is the one
# manual step that DNS ever needs. This file declares everything else
# inside the zone below.

resource "linode_domain" "liken_sh" {
  domain    = "liken.sh"
  type      = "master"
  soa_email = "c@guid.foo"
  ttl_sec   = 300
}

# The release channel's name. Linode serves a bucket over HTTPS on a
# custom domain only when the bucket is named after that domain (see
# the bucket below), and the name CNAMEs to one of the bucket's own
# hostnames. The TLS SNI and Host header of a request are how their
# edge finds both the bucket and the certificate to answer with.
#
# A bucket answers under two hostname classes, and the CNAME's target
# picks which class a custom domain gets. The plain S3 class treats
# GET / as a listing request, which this bucket's ACL refuses. The
# website class serves the bucket's index document there instead (the
# website configuration below names it). Everything else is the same
# either way: objects, digests, and the uploaded certificate, which
# Linode presents on both classes. For this reason, the channel
# points at the website class, and https://releases.liken.sh/
# answers with a page instead of a 403.

resource "linode_domain_record" "releases" {
  domain_id   = linode_domain.liken_sh.id
  name        = "releases"
  record_type = "CNAME"
  target      = "releases.liken.sh.website-us-east-1.linodeobjects.com"
}

# The website's names. GitHub Pages serves liken.sh, and the DNS
# specification forbids a CNAME at a zone's apex, so the apex
# carries GitHub's published anycast addresses for Pages as plain A
# and AAAA records. These addresses are a documented contract
# (docs.github.com, "configuring a custom domain"), which is what
# makes pinning them safe; an address pool that a provider resolves
# behind a traffic manager would not be. www CNAMEs to the
# organization's Pages hostname, and GitHub redirects it to the
# apex. GitHub also issues and renews the site's TLS certificate, so
# liken.sh needs no certificate machinery in this repository; only
# the channel's name below does.

resource "linode_domain_record" "apex_a" {
  for_each = toset([
    "185.199.108.153",
    "185.199.109.153",
    "185.199.110.153",
    "185.199.111.153",
  ])

  domain_id   = linode_domain.liken_sh.id
  name        = ""
  record_type = "A"
  target      = each.value
}

resource "linode_domain_record" "apex_aaaa" {
  for_each = toset([
    "2606:50c0:8000::153",
    "2606:50c0:8001::153",
    "2606:50c0:8002::153",
    "2606:50c0:8003::153",
  ])

  domain_id   = linode_domain.liken_sh.id
  name        = ""
  record_type = "AAAA"
  target      = each.value
}

resource "linode_domain_record" "www" {
  domain_id   = linode_domain.liken_sh.id
  name        = "www"
  record_type = "CNAME"
  target      = "liken-sh.github.io"
}

# The extension operators' manuals. Each operator repository
# publishes its own Pages site under a liken.sh subdomain. The
# hardware operators (bluetooth-operator, display-operator,
# audio-operator) each publish devices, and their hostnames are also
# their device class names, so the name a claim selects on is also
# the address where a reader learns what it selects. media-operator
# composes those devices into playback, and its manual answers at
# media.liken.sh the same way. A subdomain can CNAME where the apex
# cannot, so each name points at the organization's Pages hostname,
# and GitHub routes the request to the repository that claims the
# name as its custom domain. The Pages verification record below
# covers these names too, because it locks liken.sh's immediate
# subdomains to this organization.

resource "linode_domain_record" "extension_operators" {
  for_each = toset([
    "bluetooth",
    "display",
    "audio",
    "media",
  ])

  domain_id   = linode_domain.liken_sh.id
  name        = each.value
  record_type = "CNAME"
  target      = "liken-sh.github.io"
}

# The three hardware records lived under the name hardware_operators
# before media joined the set. This block maps their state onto the
# wider name, so an apply renames them instead of recreating them.
moved {
  from = linode_domain_record.hardware_operators
  to   = linode_domain_record.extension_operators
}

# GitHub's proof that the liken-sh organization owns these names.
# GitHub issues a code for each name, and reads it back from a TXT
# record at a label derived from the organization's login. When the
# records answer, GitHub marks the organization's liken.sh link with
# a "Verified" badge. The badge tells a visitor that the website on
# the profile and the account that publishes the code answer to the
# same owner. GitHub verifies each name separately, so the apex and
# the release channel each carry a record. The codes are not
# secrets: they prove control of this zone, and control of this zone
# is exactly what this file declares.

resource "linode_domain_record" "github_org_verification" {
  domain_id   = linode_domain.liken_sh.id
  name        = "_gh-liken-sh-o"
  record_type = "TXT"
  target      = "6c1f53b738"
}

resource "linode_domain_record" "github_org_verification_releases" {
  domain_id   = linode_domain.liken_sh.id
  name        = "_gh-liken-sh-o.releases"
  record_type = "TXT"
  target      = "fa73a639f2"
}

# GitHub Pages' own domain verification, a separate feature from the
# profile badge above. This record makes liken.sh a verified Pages
# domain for the organization, which locks the name and its
# immediate subdomains to this account: no other GitHub account can
# publish a Pages site under them. The code is not a secret, for the
# same reason the badge codes are not.

resource "linode_domain_record" "github_pages_verification" {
  domain_id   = linode_domain.liken_sh.id
  name        = "_github-pages-challenge-liken-sh"
  record_type = "TXT"
  target      = "182c9382c6c411f394267ede17a401"
}

# ---------------------------------------------------------------------------
# Release storage: the bucket that the published channel lives in.
#
# A release is a directory of digest-named artifacts and a document
# (the releases domain explains the shape), and object storage is a
# natural home for exactly that: immutable blobs, addressed by path,
# with no server of ours required to hold them. Machines fetch
# https://releases.liken.sh/<version>/release.yaml and the artifacts
# beside it. The channel's root carries one mutable object,
# channel.yaml, which names the newest published version: the
# advisory pointer that clusters poll. Nothing ever lists the
# bucket. Adopting a release still means a Cluster names the exact
# version and pins the release document's digest, so trust travels
# in the Cluster document, never in the channel.
#
# Three Linode particulars shape this resource. The label is the
# fully qualified domain name, because that is how their
# custom-domain TLS finds a bucket. The endpoint type is pinned to
# E0, us-east's type, and one of the two generations (E0/E1) that
# accept an uploaded certificate at all. And the bucket's own ACL
# stays private while CI uploads each object as public-read: known
# paths download anonymously, but the root refuses to list what
# exists.

resource "linode_object_storage_bucket" "releases" {
  region        = "us-east"
  label         = "releases.liken.sh"
  endpoint_type = "E0"
  acl           = "private"

  # Published releases are immutable, and this bucket is the only
  # copy of them. No plan that deletes it should ever run without a
  # person first editing this stanza on purpose.
  lifecycle {
    prevent_destroy = true
  }
}

# The channel's index pages. With a website configuration on the
# bucket, the website hostname class (the class that the CNAME above
# targets) serves index.html when a request asks for a prefix, the one
# thing the plain S3 class cannot do. This applies to every prefix,
# not only the root, so a page inside a release's directory makes
# https://releases.liken.sh/<version>/ answer. `liken index` renders
# the pages from the channel's own documents and the release workflow
# uploads them (releases/index.go).
#
# The error document is the front page, so a mistyped path lands on
# the list of every release. A dedicated 404 page would not read as
# one anyway: this bucket's ACL is private, so a request for a key
# that does not exist is answered 403 rather than 404, because
# reporting the difference would tell an anonymous reader which keys
# exist.

resource "aws_s3_bucket_website_configuration" "releases" {
  provider = aws.linode_object_storage
  bucket   = linode_object_storage_bucket.releases.label

  index_document {
    suffix = "index.html"
  }

  error_document {
    key = "index.html"
  }
}

# ---------------------------------------------------------------------------
# The upload credential: how CI publishes a release.
#
# The key is scoped to the releases bucket alone, because a leaked CI
# secret should spend as little as possible. Terraform hands both
# halves straight to the GitHub repository as Actions secrets, so the
# secret never rests anywhere except Linode, the state bucket, and
# GitHub's secret store. Rotation is one command:
# `terraform apply -replace=linode_object_storage_key.github_releases`
# mints a new key and delivers it again in the same run.

resource "linode_object_storage_key" "github_releases" {
  label = "github-releases-upload"

  bucket_access {
    bucket_name = linode_object_storage_bucket.releases.label
    region      = linode_object_storage_bucket.releases.region
    permissions = "read_write"
  }
}

# The bucket name and endpoint are not secrets, so the workflow that
# uploads releases states them in plain sight. Only the key goes in
# the secret store.

resource "github_actions_secret" "releases_access_key" {
  repository  = "liken"
  secret_name = "RELEASES_ACCESS_KEY"
  value       = linode_object_storage_key.github_releases.access_key
}

resource "github_actions_secret" "releases_secret_key" {
  repository  = "liken"
  secret_name = "RELEASES_SECRET_KEY"
  value       = linode_object_storage_key.github_releases.secret_key
}

# ---------------------------------------------------------------------------
# The certificate credential: how CI keeps HTTPS on the channel.
#
# Linode terminates TLS for a custom-domain bucket with a certificate
# that the owner uploads. There is no ACME on their side, so renewal
# is a recurring act. A scheduled workflow proves control of
# releases.liken.sh with a DNS-01 challenge against the zone above,
# then uploads the fresh certificate to the bucket. Both steps need
# a Linode token scoped to Domains and Object Storage read/write.
# Terraform cannot mint personal tokens, so someone creates this one
# by hand in the Cloud Manager, and it travels in .envrc.private as
# TF_VAR_releases_cert_token. Terraform's job is to deliver it to the
# repository's secret store alongside the upload key.

variable "releases_cert_token" {
  description = "A Linode token scoped to Domains and Object Storage read/write, for the certificate renewal workflow"
  type        = string
  sensitive   = true
}

resource "github_actions_secret" "releases_cert_token" {
  repository  = "liken"
  secret_name = "RELEASES_CERT_TOKEN"
  value       = var.releases_cert_token
}

# ---------------------------------------------------------------------------

output "releases_channel" {
  description = "The public base URL of the release channel"
  value       = "https://releases.liken.sh"
}
