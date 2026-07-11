# The liken.sh domain: the project's public presence, as
# infrastructure. This one file declares everything the name needs to
# answer for real people and real fleets: the DNS zone, the Linode
# machine that will run liken and serve the website, the object
# storage bucket that holds published releases, and the credential
# that lets this repo's CI upload them.
#
# The intended flow, once every piece is standing: CI builds a
# release (the releases domain), uploads the bundle to the bucket
# with the key granted below, and the webserver running *in the
# liken cluster on this machine* serves the website and the release
# channel at liken.sh. The machine's own Cluster document points
# spec.releases.source at that same channel, so the node that serves
# liken's releases also upgrades itself from them — the project
# eating its own dogfood as literally as possible.
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

# The apex points at the machine below from day one. Nothing answers
# on it yet — the record exists so that the moment the node first
# serves, the name already resolves, and so the zone never needs a
# second round of surgery.

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
# The machine: one Linode that will be the liken.sh cluster.
#
# A single node is a legitimate liken cluster (one leader, sqlite
# datastore), and it is all the website and release channel need to
# start. Growing past one node is an ordinary Cluster edit later.
#
# The honest caveat: this instance does not boot liken yet. Linode's
# KVM guests boot BIOS-style only — no UEFI — and liken today boots
# through the kernel's EFI stub, loading the OS archive and the
# deployment layer as two initrd= parameters from a boot entry that
# systemd-boot or the firmware reads. Getting liken onto this machine
# means teaching the install media a legacy path: an MBR-stage
# bootloader on the slot disk that loads vmlinuz with both archives,
# under Linode's "direct disk" boot setting, which simply executes
# the disk's boot sector. That is a drill for its own milestone, so
# the machine is provisioned powered off: the disk, the address, and
# the DNS above are real, and the first boot happens when the OS is
# ready for it.

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
  # state. The watchdog restarts a machine that powers off
  # unexpectedly; while this instance is deliberately offline, that
  # would fight us.
  backups_enabled  = false
  watchdog_enabled = false

  lifecycle {
    prevent_destroy = true
  }
}

# The whole disk, raw and unformatted: liken's installer owns the
# partition table (an ESP and the two system slots), so Linode is
# given no filesystem to manage and no distro to be helpful about.
# 25600 MB is the entire nanode allotment.

resource "linode_instance_disk" "system" {
  label      = "liken"
  linode_id  = linode_instance.node.id
  size       = 25600
  filesystem = "raw"
}

resource "linode_instance_config" "boot" {
  label     = "liken"
  linode_id = linode_instance.node.id

  # Linode's hosts boot guests BIOS-style only — no UEFI — while a
  # liken machine normally boots through the kernel's EFI stub, from
  # boot entries the installer writes into firmware variables. So
  # this machine's disk carries its own bootloader: the Makefile
  # installs GRUB into the image's MBR and a small BIOS boot
  # partition, with a grub.cfg on the system slot mirroring what the
  # EFI boot entry would have said. Direct disk boot means the host
  # simply executes what the MBR carries. (Linode's GRUB 2 setting
  # was the road not taken: the host's GRUB expects the whole disk
  # to be one filesystem, as Linode's own images are, and never finds
  # a config inside a partitioned disk.) The machine boots and runs
  # normally under BIOS; what it loses is the firmware half of
  # blue-green upgrades (BootNext/BootOrder), so release upgrades on
  # this machine wait on a BIOS-boot milestone. Powered off
  # (booted = false) until the disk carries an installed system.
  kernel      = "linode/direct-disk"
  root_device = "/dev/sda"
  booted      = false

  device {
    device_name = "sda"
    disk_id     = linode_instance_disk.system.id
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
# Release storage: the bucket the published channel lives in.
#
# A release is a directory of digest-named artifacts and a document
# (the releases domain explains the shape), and object storage is a
# natural home for exactly that: immutable blobs, addressed by path,
# no server of ours required to hold them. The webserver in the
# cluster serves the public channel from here; its read credentials
# are part of deploying the site, not part of this file.

resource "linode_object_storage_bucket" "releases" {
  region = "us-east"
  label  = "liken-releases"

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

output "node_ipv4" {
  description = "The liken.sh node's public IPv4 address"
  value       = tolist(linode_instance.node.ipv4)[0]
}

output "node_ipv6" {
  description = "The liken.sh node's public IPv6 address"
  value       = split("/", linode_instance.node.ipv6)[0]
}

output "releases_bucket" {
  description = "S3 URL of the release storage bucket"
  value       = "https://${linode_object_storage_bucket.releases.region}-1.linodeobjects.com/${linode_object_storage_bucket.releases.label}"
}
