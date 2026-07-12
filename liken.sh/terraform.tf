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
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.0"
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

# The cluster itself is a provider too: the website below is ordinary
# Kubernetes resources, and Terraform applies them with the operator
# credential `make kubeconfig` mints. The API server's certificate
# names the node's cluster-segment address, so reaching it over the
# public internet means naming the server we expect — the same
# --server/--tls-server-name pair every kubectl in this deployment
# uses.
provider "kubernetes" {
  config_path     = "identity/kubeconfig"
  host            = "https://${tolist(linode_instance.node.ipv4)[0]}:6443"
  tls_server_name = "10.10.0.1"
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
# The OS reaches this machine as a disk image. The Makefile beside
# this file runs a real liken install in a local QEMU guest and the
# resulting disk is declared below as a Linode custom image, hashed
# so that Terraform notices a rebuild; shipping a new OS is
# `make && terraform apply` with the instance powered off, and a
# power-on to boot the result. The machine's storage splits by
# lifetime to make that stamp safe: the system disk is erased by
# every ship, and everything durable lives on a data disk the ship
# never touches (machines/node-1.yaml declares which roles live
# where).

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

# The OS, as a custom image. `make` in this directory produces
# image/disk.img.gz — a complete installed system disk — and this
# resource uploads it, so the file must exist before a plan. Linode
# accepts raw disk images, gzipped, up to 6 GB uncompressed; the
# Makefile explains how that cap shaped the disk layout.
#
# Replacing this image is how an OS ships: a new image forces the
# system disk below to be recreated from it, which erases that disk.
# Shipping is therefore an explicit act, never a side effect:
# ignore_changes keeps a routine apply from noticing a rebuilt local
# image (builds aren't byte-reproducible, so every rebuild would
# otherwise read as a ship), and
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

  # Linode's hosts boot guests BIOS-style only — no UEFI — while a
  # liken machine normally boots through the kernel's EFI stub, from
  # boot entries the installer writes into firmware variables. So
  # this machine's disk carries its own bootloader: the Makefile
  # installs GRUB into the image's MBR and a small BIOS boot
  # partition, with its config on the system slot mirroring what the
  # EFI boot entry would have said. Direct disk boot means the host
  # simply executes what the MBR carries. (Linode's GRUB 2 setting
  # is a dead end for this disk: their loader reads its config from
  # the disk treated as one whole-disk filesystem, as Linode's own
  # images are laid out, and never looks inside a partition table.)
  #
  # One wart rides along: Linode's image deploys zero whatever boot
  # code the MBR carries, so after the system disk is stamped from a
  # new image, the first 440 bytes must be put back from the local
  # disk.img over a rescue boot before the machine can boot. The
  # machine runs normally under BIOS; what it loses is the firmware
  # half of blue-green upgrades (BootNext/BootOrder), so release
  # upgrades on this machine wait on a BIOS-boot milestone. The
  # config deliberately says nothing about power: `booted` here is
  # not a description but an instruction (false shuts a running
  # machine down to match), so power stays an explicit act through
  # the API, never a side effect of an apply.
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
# The firewall: the node's public face is the website, nothing else.
#
# The cluster API on 6443 authenticates every caller with client
# certificates, so the firewall is defense in depth rather than the
# lock on the door — but depth is cheap and exposure isn't: a port
# the edge drops can't be scanned, fuzzed, or hit by the next
# pre-auth vulnerability, and a 1 GB node shouldn't spend its one
# CPU entertaining the internet's background noise. Packets the
# firewall drops never reach the machine at all.
#
# The operator's address arrives as a variable so it never lands in
# this public repository: .envrc.private exports TF_VAR_operator_cidr
# (it lives on in the Terraform state, which is already private, like
# every other secret here). The VLAN is untouched — Cloud Firewalls
# filter the public interface, and cluster traffic rides the private
# segment.

variable "operator_cidr" {
  description = "The address allowed to reach the cluster API and rescue-mode ssh, as a CIDR"
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
    ipv4     = [var.operator_cidr]
  }

  # Rescue mode's sshd, for shipping: the machine itself runs no ssh
  # server, so outside a rescue boot this rule matches nothing.
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
# The website: one static page, served by the cluster it describes.
#
# The page rides in a ConfigMap and nginx serves it — the smallest
# arrangement that is still ordinary Kubernetes, so replacing it with
# a real site later is editing these resources, not inventing new
# machinery. Traffic arrives through Traefik, the cluster's declared
# ingress (cluster.yaml opts into it), which the service load
# balancer binds to the node's own addresses: the ports liken.sh
# resolves to are the ports Traefik answers.

resource "kubernetes_config_map_v1" "website" {
  metadata {
    name = "website"
  }
  data = {
    "index.html" = file("${path.module}/website/index.html")
  }
}

resource "kubernetes_deployment_v1" "website" {
  metadata {
    name = "website"
  }
  spec {
    replicas = 1
    selector {
      match_labels = { app = "website" }
    }
    template {
      metadata {
        labels = { app = "website" }
        annotations = {
          # Roll the deployment when the page changes: the pod spec
          # itself is what Kubernetes watches, so the content's hash
          # rides in it.
          "liken.sh/content" = sha1(file("${path.module}/website/index.html"))
        }
      }
      spec {
        container {
          name  = "nginx"
          image = "nginx:1.29-alpine"
          port {
            container_port = 80
          }
          volume_mount {
            name       = "page"
            mount_path = "/usr/share/nginx/html"
          }
        }
        volume {
          name = "page"
          config_map {
            name = kubernetes_config_map_v1.website.metadata[0].name
          }
        }
      }
    }
  }
}

resource "kubernetes_service_v1" "website" {
  metadata {
    name = "website"
  }
  spec {
    selector = { app = "website" }
    port {
      port        = 80
      target_port = 80
    }
  }
}

resource "kubernetes_ingress_v1" "website" {
  metadata {
    name = "website"
  }
  spec {
    ingress_class_name = "traefik"
    dynamic "rule" {
      for_each = ["liken.sh", "www.liken.sh"]
      content {
        host = rule.value
        http {
          path {
            path = "/"
            backend {
              service {
                name = kubernetes_service_v1.website.metadata[0].name
                port {
                  number = 80
                }
              }
            }
          }
        }
      }
    }
  }
}

# ---------------------------------------------------------------------------

# The identifiers the shipping commands need (README.md), so they
# never have to be written down anywhere else.

output "node_id" {
  description = "The Linode instance ID, for linode-cli power commands"
  value       = linode_instance.node.id
}

output "boot_config_id" {
  description = "The boot configuration ID, for linode-cli boot commands"
  value       = linode_instance_config.boot.id
}

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
