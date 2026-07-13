# The liken.sh domain: the project's public presence, as
# infrastructure. This one file declares everything the name answers
# for: the DNS zone, and the release channel — the object storage
# bucket that holds published releases, the credential that lets this
# repo's CI upload them, and the token that keeps the channel's TLS
# certificate fresh.
#
# The channel lives in object storage rather than on a liken machine
# for a reason worth stating plainly: machines upgrade themselves
# *from* the channel, so the channel has to outlive any machine it
# feeds. A cluster that served its own update channel could never be
# rescued by an update. When a liken cluster serves the project's
# website again, its resources will join this file — and that cluster
# will install and upgrade from the channel declared here.
#
# Terraform fits here for the same reason the Machine and Cluster
# documents fit the OS: the desired state is declared in files, and a
# reconciler (here, `terraform apply` run by a person) converges the
# world to match. Credentials stay out of these files entirely: the
# linode provider reads LINODE_TOKEN and the github provider reads
# GITHUB_TOKEN from the environment, both exported by the repo's
# .envrc.

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
  }

  # Terraform's state maps these declarations onto real resource IDs,
  # and it records secrets verbatim (the upload key below), so it can
  # never be committed. It lives in its own private bucket on Linode
  # Object Storage, spoken to over the S3 protocol. That bucket is
  # the one piece of bootstrap this file cannot create for itself —
  # the state has to exist somewhere before the first apply — so it
  # is made once by hand, with an access key scoped to it alone; the
  # key's halves travel in .envrc.private as the AWS_* variables the
  # S3 backend conventionally reads. The skip_* lines tell the
  # backend not to expect the parts of AWS that Linode's S3
  # compatibility doesn't imitate.
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

# ---------------------------------------------------------------------------
# DNS: the liken.sh zone.
#
# Linode is the authoritative nameserver; the registrar's one job is
# to delegate here, by pointing the domain's NS records at
# ns1.linode.com through ns5.linode.com. That delegation is the one
# manual step DNS ever needs — everything inside the zone is declared
# below.

resource "linode_domain" "liken_sh" {
  domain    = "liken.sh"
  type      = "master"
  soa_email = "c@guid.foo"
  ttl_sec   = 300
}

# The release channel's name. Linode serves a bucket over HTTPS on a
# custom domain only when the bucket is *named* that domain (see the
# bucket below) and the name CNAMEs to the bucket's own hostname: the
# TLS SNI and Host header of a request are how their edge finds both
# the bucket and the certificate to answer with.

resource "linode_domain_record" "releases" {
  domain_id   = linode_domain.liken_sh.id
  name        = "releases"
  record_type = "CNAME"
  target      = "releases.liken.sh.us-east-1.linodeobjects.com"
}

# ---------------------------------------------------------------------------
# Release storage: the bucket the published channel lives in.
#
# A release is a directory of digest-named artifacts and a document
# (the releases domain explains the shape), and object storage is a
# natural home for exactly that: immutable blobs, addressed by path,
# no server of ours required to hold them. Machines fetch
# https://releases.liken.sh/<version>/release.yaml and the artifacts
# beside it, and the channel's root carries one mutable object,
# channel.yaml, naming the newest published version — the advisory
# pointer clusters poll. Nothing ever lists the bucket: adopting a
# release still means a Cluster naming the exact version and pinning
# the release document's digest, so trust travels in the Cluster
# document, never the channel.
#
# Three Linode particulars shape this resource. The label is the
# fully-qualified domain name, because that is how their custom-domain
# TLS finds a bucket. The endpoint type is pinned to E0 — us-east's
# type, and one of the two generations (E0/E1) that accept an uploaded
# certificate at all. And the bucket's own ACL stays private while CI
# uploads each object public-read: known paths download anonymously,
# but the root refuses to enumerate what exists.

resource "linode_object_storage_bucket" "releases" {
  region        = "us-east"
  label         = "releases.liken.sh"
  endpoint_type = "E0"
  acl           = "private"

  # Published releases are immutable and this bucket is the only copy
  # of them; no plan that deletes it should ever run without a person
  # first editing this stanza with intent.
  lifecycle {
    prevent_destroy = true
  }
}

# ---------------------------------------------------------------------------
# The upload credential: how CI publishes a release.
#
# The key is scoped to the releases bucket alone — a leaked CI secret
# should spend as little as possible — and Terraform hands both
# halves straight to the GitHub repository as Actions secrets, so the
# secret never rests anywhere but Linode, the state bucket, and
# GitHub's secret store. Rotation is one command:
# `terraform apply -replace=linode_object_storage_key.github_releases`
# mints a new key and re-delivers it in the same run.

resource "linode_object_storage_key" "github_releases" {
  label = "github-releases-upload"

  bucket_access {
    bucket_name = linode_object_storage_bucket.releases.label
    region      = linode_object_storage_bucket.releases.region
    permissions = "read_write"
  }
}

# The bucket name and endpoint are not secrets, so the workflow that
# uploads releases states them in plain sight; only the key rides in
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
# the owner uploads; there is no ACME on their side, so renewal is a
# recurring act. A scheduled workflow proves control of
# releases.liken.sh with a DNS-01 challenge against the zone above,
# then uploads the fresh certificate to the bucket — both steps need
# a Linode token scoped to Domains and Object Storage read/write.
# Terraform cannot mint personal tokens, so this one is created by
# hand in the Cloud Manager and travels in .envrc.private as
# TF_VAR_releases_cert_token; Terraform's job is delivering it to the
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
