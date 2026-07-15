# The liken.sh domain: the project's public presence, as
# infrastructure. This one file declares what Linode has to know for
# the name to answer: the DNS zone, the machine that runs the cluster
# and serves the website, the firewall in front of it, and the
# release channel — the object storage bucket that holds published
# releases, the credential that lets this repo's CI upload them, and
# the token that keeps the channel's TLS certificate fresh.
#
# The channel lives in object storage rather than on a liken machine
# for a reason worth stating plainly: machines upgrade themselves
# *from* the channel, so the channel has to outlive any machine it
# feeds. A cluster that served its own update channel could never be
# rescued by an update. The website has no such constraint, so the
# cluster serves it — but the site's Kubernetes resources deliberately
# do not live in this file. Terraform's kubernetes provider fetches
# the API server's entire OpenAPI document on every plan, which is
# real memory pressure on a 1 GB node (plans/31 tells that story), so
# Terraform stops at Linode's edge and the website deploys with
# kubectl instead (website/README.md).
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
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
    # The aws provider is here for exactly one resource: the release
    # bucket's website configuration. That setting exists only in the
    # S3 protocol (PutBucketWebsite) — Linode's own API doesn't carry
    # it and their provider doesn't model it — and the aws provider is
    # the maintained Terraform client for that protocol, pointed at
    # Linode's endpoint below.
    aws = {
      source  = "hashicorp/aws"
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

# The aws provider, aimed at Linode's S3 endpoint rather than AWS.
# It authenticates with the same scoped upload key CI publishes with,
# read straight from that key's resource, so no new credential
# exists. The skip_* lines tell it not to expect the parts of AWS
# that Linode's S3 compatibility doesn't imitate, and path-style
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
# bucket below) and the name CNAMEs to one of the bucket's own
# hostnames: the TLS SNI and Host header of a request are how their
# edge finds both the bucket and the certificate to answer with.
#
# A bucket answers under two hostname classes, and the CNAME's target
# picks which one a custom domain gets. The plain S3 class treats
# GET / as a listing request, which this bucket's ACL refuses; the
# website class serves the bucket's index document there instead (the
# website configuration below names it). Everything else is the same
# either way — objects, digests, and the uploaded certificate, which
# Linode presents on both classes — so the channel points at the
# website class and https://releases.liken.sh/ answers with a page
# instead of a 403.

resource "linode_domain_record" "releases" {
  domain_id   = linode_domain.liken_sh.id
  name        = "releases"
  record_type = "CNAME"
  target      = "releases.liken.sh.website-us-east-1.linodeobjects.com"
}

# The website's names: the apex points at the node, and www rides
# along as a CNAME. Traefik on the cluster answers for both
# (website/), and it proves control of them to Let's Encrypt by
# answering a challenge dialed back to the name — so these records
# have to resolve before the site's first deploy can earn its
# certificate.

resource "linode_domain_record" "apex_a" {
  domain_id   = linode_domain.liken_sh.id
  name        = ""
  record_type = "A"
  target      = tolist(linode_instance.node.ipv4)[0]
}

resource "linode_domain_record" "apex_aaaa" {
  domain_id   = linode_domain.liken_sh.id
  name        = ""
  record_type = "AAAA"
  target      = split("/", linode_instance.node.ipv6)[0]
}

resource "linode_domain_record" "www" {
  domain_id   = linode_domain.liken_sh.id
  name        = "www"
  record_type = "CNAME"
  target      = "liken.sh"
}

# ---------------------------------------------------------------------------
# The machine: one nanode, carrying the cluster.

resource "linode_instance" "node" {
  label  = "liken-node-1"
  region = "us-east"

  # The smallest Linode: 1 GB of memory. liken runs from a read-only
  # system image on disk, so the OS itself needs little RAM and a
  # single small machine can carry the cluster and its website.
  type = "g6-nanode-1"

  # Backups snapshot disks, and a liken machine's disks are the wrong
  # thing to snapshot: the slots are reproducible from published
  # releases, and everything that matters lives in the cluster's own
  # state.
  backups_enabled = false

  # The watchdog (Lassie) boots the instance whenever it finds it
  # powered off unexpectedly, and on this host that is load-bearing:
  # Linode treats a guest-initiated reboot as a power-off, so every
  # reboot a liken machine performs on itself — proving a new
  # release, rolling back from one, applying a spec that needs a
  # fresh boot — ends with the instance off and the watchdog is what
  # turns it back on. Without it, the machine's first self-reboot is
  # its last.
  watchdog_enabled = true

  lifecycle {
    prevent_destroy = true
  }
}

# The OS, as a custom image. `make RELEASE=<version>` in this
# directory downloads that published release from the channel,
# verifies it, and really installs it onto image/disk.img.gz — a
# complete installed system disk — and this resource uploads the
# result, so the file must exist before a plan. Linode accepts raw
# disk images, gzipped, up to 6 GB uncompressed; the Makefile
# explains how that cap shaped the disk layout.
#
# Replacing this image is how an OS ships from the outside — which
# founding a machine needs exactly once. After that the machine
# upgrades itself from the channel, and shipping a disk again is the
# recovery path, not the routine. A new image forces the system disk
# below to be recreated from it, which erases that disk. Shipping is
# therefore an explicit act, never a side effect: ignore_changes
# keeps a routine apply from noticing a rebuilt local image (builds
# aren't byte-reproducible, so every rebuild would otherwise read as
# a ship), and
#
#   terraform apply -replace=linode_image.system
#
# is the command that means "reinstall the node's OS from the image
# I just built" — run with the instance powered off, then power on.

resource "linode_image" "system" {
  label       = "liken-node-1-system"
  description = "The liken.sh node's installed system disk"
  region      = "us-east"

  file_path = "${path.module}/image/disk.img.gz"
  file_hash = filemd5("${path.module}/image/disk.img.gz")

  lifecycle {
    ignore_changes = [file_hash]
  }
}

# The system disk, stamped from the image above. Slightly larger than
# the 3 GiB image because Linode's advertised sizes don't land on
# exact byte counts; liken grows the partition table to the disk's
# real end on first boot. Replacing a disk needs the instance
# powered off, so a ship is: power off, apply, power on.

resource "linode_instance_disk" "system" {
  label     = "liken-system"
  linode_id = linode_instance.node.id
  size      = 3200
  image     = linode_image.system.id

  # The API insists on a login credential whenever a disk comes from
  # an image, so that Linode can write it into the disk's filesystem.
  # A raw image has no filesystem Linode can read, so nothing is ever
  # written anywhere and no such login exists; this value satisfies
  # the API's schema and does nothing else.
  root_pass = "inert!on a raw image 0"
}

# The data disk: raw, blank, and never shipped to. liken claims it on
# the machine's first boot, writing role names into its GPT, and
# every ship after that replaces the OS around it — the cluster's
# database, images, and volumes survive. 22400 MB is the rest of the
# nanode's allotment.

resource "linode_instance_disk" "data" {
  label      = "liken-data"
  linode_id  = linode_instance.node.id
  size       = 22400
  filesystem = "raw"

  lifecycle {
    prevent_destroy = true
  }
}

resource "linode_instance_config" "boot" {
  label     = "liken"
  linode_id = linode_instance.node.id

  # Linode's hosts boot guests BIOS-style only — no UEFI — so this
  # machine's disk carries its own bootloader, laid down by the
  # installer: GRUB's first stage in the MBR, its core image in a
  # BIOS boot partition, and its config and environment block on the
  # boot home. Direct disk boot means the host simply executes what
  # the MBR carries, and upgrades actuate through GRUB's environment
  # block the way UEFI machines actuate through firmware variables.
  # (Linode's GRUB 2 setting is a dead end for this disk: their
  # loader reads its config from the disk treated as one whole-disk
  # filesystem, as Linode's own images are laid out, and never looks
  # inside a partition table.)
  #
  # The MBR's 440 boot-code bytes deserve a sentence of history:
  # Linode's image deploys used to zero them, which once meant a
  # rescue-boot restoration after every ship. Today's deploys are
  # byte-faithful — verified by hashing a deployed disk end to end —
  # and the machine guards those bytes itself anyway, re-deriving
  # them from its proven slot's artifacts on every boot and before
  # every reboot. The config deliberately says nothing about power:
  # `booted` here is not a description but an instruction (false
  # shuts a running machine down to match), so power stays an
  # explicit act through the API, never a side effect of an apply.
  kernel      = "linode/direct-disk"
  root_device = "/dev/sda"

  device {
    device_name = "sda"
    disk_id     = linode_instance_disk.system.id
  }

  device {
    device_name = "sdb"
    disk_id     = linode_instance_disk.data.id
  }

  # Two interfaces, and the second is the reason this cluster can
  # grow: eth0 is the public internet, and eth1 joins a private VLAN
  # named "liken" — Linode's free layer-2 segment within a region.
  # The VLAN is the cluster segment (the Cluster document's nodeCIDR
  # lives here), so adding a node later is another instance with the
  # same VLAN label and the next address, exactly how the lab's
  # machines share their cluster network. The ipam_address is
  # Linode-side bookkeeping only; the machine configures its own
  # addresses from its Machine manifest.
  interface {
    purpose = "public"
  }

  interface {
    purpose      = "vlan"
    label        = "liken"
    ipam_address = "10.10.0.1/24"
  }

  # Every helper here assumes Linode installed the distro and may
  # reach into its filesystem at boot to adjust it. liken is not that
  # distro, and nothing on the outside gets to edit a slot.
  helpers {
    devtmpfs_automount = false
    distro             = false
    modules_dep        = false
    network            = false
    updatedb_disabled  = true
  }
}

# ---------------------------------------------------------------------------
# The firewall: the node answers the world on the website's ports and
# the cluster API, and drops everything else at the edge.
#
# The API on 6443 answers publicly because CI deploys the website
# from GitHub-hosted runners, whose addresses can't usefully be
# pinned. That is a real trade: an open port can be scanned, fuzzed,
# and hit by the next pre-auth vulnerability, and a 1 GB node would
# rather not spend its one CPU entertaining the internet's background
# noise. What bounds the exposure is what a caller can actually do:
# the API authenticates everyone with client certificates or tokens,
# unauthenticated requests stop at TLS, and the one credential that
# leaves the operator's hands — the certificate CI deploys with — is
# scoped to the website's namespace alone
# (website/manifests/deployer.yaml), so the worst a leak spends is
# the page itself.
#
# The operator's address arrives as a variable so it never lands in
# this public repository: .envrc.private exports TF_VAR_operator_cidr
# (it lives on in the Terraform state, which is already private, like
# every other secret here). The VLAN is untouched — Cloud Firewalls
# filter the public interface, and cluster traffic rides the private
# segment.

variable "operator_cidr" {
  description = "The address allowed to reach rescue-mode ssh, as a CIDR"
  type        = string
  sensitive   = true
}

resource "linode_firewall" "node" {
  label           = "liken-node-1"
  inbound_policy  = "DROP"
  outbound_policy = "ACCEPT"
  linodes         = [linode_instance.node.id]

  inbound {
    label    = "website-http"
    action   = "ACCEPT"
    protocol = "TCP"
    ports    = "80"
    ipv4     = ["0.0.0.0/0"]
    ipv6     = ["::/0"]
  }

  inbound {
    label    = "website-https"
    action   = "ACCEPT"
    protocol = "TCP"
    ports    = "443"
    ipv4     = ["0.0.0.0/0"]
    ipv6     = ["::/0"]
  }

  # Ping stays answerable: it costs nothing and it is the first
  # question anyone asks a machine that seems down.
  inbound {
    label    = "ping"
    action   = "ACCEPT"
    protocol = "ICMP"
    ipv4     = ["0.0.0.0/0"]
    ipv6     = ["::/0"]
  }

  inbound {
    label    = "kubernetes-api"
    action   = "ACCEPT"
    protocol = "TCP"
    ports    = "6443"
    ipv4     = ["0.0.0.0/0"]
    ipv6     = ["::/0"]
  }

  # Rescue mode's sshd, the break-glass path: the machine itself runs
  # no ssh server, so outside a rescue boot this rule matches nothing.
  inbound {
    label    = "rescue-ssh"
    action   = "ACCEPT"
    protocol = "TCP"
    ports    = "22"
    ipv4     = [var.operator_cidr]
  }
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

# The channel's front page. With a website configuration on the
# bucket, the website hostname class (which the CNAME above targets)
# serves index.html when a request asks for / — the one thing the
# plain S3 class cannot do — and the same page answers for any path
# that doesn't exist. The page itself is releases/index.html in this
# repository; the release workflow keeps the object fresh.

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
# The website deploy credential: how CI updates the page.
#
# CI authenticates to the cluster the same way the operator does — a
# client certificate signed by the cluster's client CA — just as a
# lesser user. The cluster's identity is minted offline (`make
# identity`) and its CA material lives in identity/, so Terraform can
# mint this credential offline too: a key and a certificate naming
# the user website-deployer, whose entire authority is the RBAC in
# website/manifests/deployer.yaml — content updates in the website's
# namespace, nothing else. Kubernetes reads the certificate's common
# name as the username, so the signature is the authentication and
# the RoleBinding is the authorization. Nothing here talks to the
# cluster: what a plan needs is the identity files on disk, the same
# bootstrap the system image above asks for, and the credential can
# be minted and delivered while the machine is down, mid-refound, or
# not yet founded at all.
#
# The certificate lives a year, and any plan inside the last month of
# that proposes a fresh one, so renewal is whichever `terraform
# apply` comes next. Rotation on demand is
#
#   terraform apply -replace=tls_private_key.website_deployer
#
# and revocation doesn't involve the certificate at all (issued
# certificates can't be recalled): delete the RoleBinding on the
# cluster and the name the certificate proves stops meaning anything.

resource "tls_private_key" "website_deployer" {
  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "tls_cert_request" "website_deployer" {
  private_key_pem = tls_private_key.website_deployer.private_key_pem

  subject {
    common_name = "website-deployer"
  }
}

resource "tls_locally_signed_cert" "website_deployer" {
  cert_request_pem   = tls_cert_request.website_deployer.cert_request_pem
  ca_private_key_pem = file("${path.module}/identity/tls/client-ca.key")
  ca_cert_pem        = file("${path.module}/identity/tls/client-ca.crt")

  allowed_uses = ["client_auth", "digital_signature"]

  validity_period_hours = 24 * 365
  early_renewal_hours   = 24 * 30
}

# The kubeconfig CI receives, assembled from parts Terraform already
# holds: the certificate above, the server CA that lets the client
# trust what it dials, and the same public-address/tls-server-name
# pair the ./kubectl wrapper uses (the API server's certificate names
# the VLAN address; the tls-server-name is what lets one certificate
# serve both the VLAN and the world).

locals {
  website_deployer_kubeconfig = yamlencode({
    apiVersion = "v1"
    kind       = "Config"
    clusters = [{
      name = "liken-sh"
      cluster = {
        server                       = "https://${tolist(linode_instance.node.ipv4)[0]}:6443"
        "tls-server-name"            = "10.10.0.1"
        "certificate-authority-data" = base64encode(file("${path.module}/identity/tls/server-ca.crt"))
      }
    }]
    users = [{
      name = "website-deployer"
      user = {
        "client-certificate-data" = base64encode(tls_locally_signed_cert.website_deployer.cert_pem)
        "client-key-data"         = base64encode(tls_private_key.website_deployer.private_key_pem)
      }
    }]
    contexts = [{
      name = "website"
      context = {
        cluster = "liken-sh"
        user    = "website-deployer"
      }
    }]
    "current-context" = "website"
  })
}

resource "github_actions_secret" "website_kubeconfig" {
  repository  = "liken"
  secret_name = "WEBSITE_KUBECONFIG"
  value       = local.website_deployer_kubeconfig
}

# ---------------------------------------------------------------------------

output "releases_channel" {
  description = "The public base URL of the release channel"
  value       = "https://releases.liken.sh"
}
