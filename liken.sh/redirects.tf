# The redirect host: the machine that keeps the old manual addresses
# working. Each operator's API group, such as bluetooth.liken.sh, and
# each CSI driver's name, such as git.liken.sh, is also the address of
# its manual, because a cluster shows those names and a person opens
# them. The manuals are sections of the one site at liken.sh, so the
# host answers each name with a 301 to that section, with the same
# path and query:
#
#     https://display.liken.sh/docs/guides/install/
#       -> https://liken.sh/display/docs/guides/install/
#
# Neither Linode DNS nor Linode Object Storage serves a redirect, so a
# machine must answer. It is the smallest Linode, a Nanode, and it
# runs Flatcar Container Linux, an immutable OS that runs containers.
# Flatcar's /usr is read-only and updates as a whole, and the host's
# only other state comes from one Ignition config on its first boot.
# The config starts one container, Caddy, from the official image.
# Caddy gets a certificate for each name from Let's Encrypt over
# HTTP-01 or TLS-ALPN-01, so the host needs no DNS token and holds no
# secret that this repository gave it.
#
# Every input that shapes the host is below or beside this file: the
# Flatcar image that the Makefile downloads, redirects.bu, the
# Caddyfile, and the two variables. A change to any of them replaces
# the instance, and nothing on the host changes by hand. The host has
# no SSH. README.md gives the way in for an emergency.

variable "redirect_names" {
  description = "The subdomains of liken.sh that the redirect host answers over HTTPS, each with a 301 to liken.sh/<name>/"
  type        = list(string)
  default = [
    "audio",
    "bluetooth",
    "display",
    "equipment",
    "git",
    "library",
    "media",
    "people",
    "per-node",
  ]

  # The Caddyfile finds the name as the third label from the right of
  # the host, so each name must be one DNS label.
  validation {
    condition     = alltrue([for name in var.redirect_names : can(regex("^[a-z0-9]([a-z0-9-]*[a-z0-9])?$", name))])
    error_message = "Each name must be one DNS label: lowercase letters, digits, and inner hyphens."
  }
}

variable "caddy_image" {
  description = "The official Caddy image that the redirect host runs, with its tag and its digest"
  type        = string
  default     = "docker.io/library/caddy:2.11.4@sha256:0c994536bddb66445885237f1a5dcc1916bccea922661c76b4e9fc24061f9b52"

  # Docker pulls by the digest when a reference has one, so the tag
  # is only for the reader. Without a digest, a new push to the tag
  # would change the host with no change here.
  validation {
    condition     = can(regex("@sha256:[0-9a-f]{64}$", var.caddy_image))
    error_message = "The image reference must end in @sha256:<digest>."
  }
}

# The Flatcar image, uploaded as a private Linode image. `make flatcar`
# downloads it and checks its digest, then writes the version to
# flatcar/version, so the Makefile holds the only copy of the pin.
# Without the download, the plan fails here and names the missing file.
#
# The file hash is the image's identity for Terraform: a different
# file replaces the image, and the instance below with it. Linode
# offers the metadata service, which carries the Ignition config, only
# to an image that is marked for cloud-init, so the upload sets
# cloud_init. Its region is the one the release bucket uses, and
# us-east offers the metadata service.

locals {
  flatcar_version = trimspace(file("${path.module}/flatcar/version"))
  flatcar_image   = "${path.module}/flatcar/${local.flatcar_version}/flatcar_production_akamai_image.bin.gz"
}

resource "linode_image" "flatcar" {
  label       = "flatcar-${replace(local.flatcar_version, ".", "-")}"
  description = "Flatcar Container Linux ${local.flatcar_version}, stable, for the liken.sh redirect host"
  region      = "us-east"
  cloud_init  = true

  file_path = local.flatcar_image
  file_hash = filemd5(local.flatcar_image)
}

# The Ignition config, from the Butane file and the Caddyfile. The ct
# provider transpiles Butane inside the plan, so no rendered JSON is
# ever in history to drift from its source, and strict mode makes a
# Butane warning an error.

data "ct_config" "redirects" {
  content = templatefile("${path.module}/redirects.bu", {
    caddy_image = var.caddy_image
    caddyfile = templatefile("${path.module}/Caddyfile", {
      domain = linode_domain.liken_sh.domain
      names  = var.redirect_names
    })
  })
  strict       = true
  pretty_print = false
}

# The firewall passes only the two ports that Caddy answers. Port 80
# carries the HTTP-01 challenge and the redirect to HTTPS, and 443
# carries the redirects and the TLS-ALPN-01 challenge. Every other
# inbound packet is dropped, SSH included. Outbound traffic passes,
# because Caddy must reach Let's Encrypt, Docker must pull the image,
# and Flatcar must reach its update server. Linode's firewall is
# stateful, so the replies to those connections come back in.

resource "linode_firewall" "redirects" {
  label           = "liken-sh-redirects"
  inbound_policy  = "DROP"
  outbound_policy = "ACCEPT"

  inbound {
    label    = "http-and-https"
    action   = "ACCEPT"
    protocol = "TCP"
    ports    = "80,443"
    ipv4     = ["0.0.0.0/0"]
    ipv6     = ["::/0"]
  }
}

# The instance, its disk, and its boot configuration, in the shape of
# Flatcar's guide for Linode. The instance starts with no disk, so it
# does not boot until the configuration below exists. The firewall
# attaches when the instance is created, so the host never runs
# without it.
#
# The user data is the Ignition config, and the provider replaces the
# instance when it changes. The image does not reach the instance
# directly, so replace_triggered_by replaces the instance when the
# image changes. Each replacement is a new host with an empty Caddy
# data directory, which asks Let's Encrypt for one new certificate
# for each name. In any seven days, Let's Encrypt issues at most 5
# certificates for one name and at most 50 for all of liken.sh, so a
# sixth replacement in a week leaves the names without HTTPS until
# the limit refills.
#
# interface_generation is explicit, because otherwise the account
# setting interfaces_for_new_linodes sets it. The configuration
# profile below needs the legacy kind, which gives the instance its
# public interface with no interface resources.

resource "linode_instance" "redirects" {
  label                = "liken-sh-redirects"
  region               = "us-east"
  type                 = "g6-nanode-1"
  firewall_id          = linode_firewall.redirects.id
  interface_generation = "legacy_config"

  metadata {
    user_data = base64encode(data.ct_config.redirects.rendered)
  }

  lifecycle {
    replace_triggered_by = [linode_image.flatcar]
  }
}

# The API requires a root password for a disk made from an image.
# Flatcar does not use it: the Linode helper that sets the password
# does not support Flatcar, so the root account keeps no password.
# The value exists only to satisfy the API, and nobody reads it.

resource "random_password" "redirects_root" {
  length = 32
}

resource "linode_instance_disk" "redirects" {
  label     = "flatcar"
  linode_id = linode_instance.redirects.id
  size      = linode_instance.redirects.specs[0].disk
  image     = linode_image.flatcar.id
  root_pass = random_password.redirects_root.result
}

# Flatcar boots from its own GRUB, so the kernel is direct-disk, and
# every Linode helper is off. The helpers edit files on the disk at
# boot, and Flatcar's configuration comes from Ignition alone.

resource "linode_instance_config" "redirects" {
  label       = "flatcar"
  linode_id   = linode_instance.redirects.id
  kernel      = "linode/direct-disk"
  root_device = "/dev/sda"
  booted      = true

  device {
    device_name = "sda"
    disk_id     = linode_instance_disk.redirects.id
  }

  helpers {
    devtmpfs_automount = false
    distro             = false
    modules_dep        = false
    network            = false
    updatedb_disabled  = true
  }
}

# The wildcard records send every subdomain of liken.sh to the host.
# In DNS, a name with a record of its own never matches a wildcard, so
# the apex, www, releases, log, and the verification records keep
# their answers. So does each name in extension_operators in
# terraform.tf: while its CNAME to GitHub Pages exists, requests for
# that name go to the archived repository's Pages site, and the host
# cannot get its certificate. A name redirects once its CNAME leaves
# that set. A name with no record and no entry in redirect_names
# reaches the host too. Caddy sends every plain HTTP request to HTTPS,
# and the TLS handshake for that name fails, because Caddy has no
# certificate for it.

# The instance has one IPv4 address, its public one, because it has no
# private interface. one() fails the plan if the set ever holds a
# second address.

resource "linode_domain_record" "redirects_a" {
  domain_id   = linode_domain.liken_sh.id
  name        = "*"
  record_type = "A"
  target      = one(linode_instance.redirects.ipv4)
}

resource "linode_domain_record" "redirects_aaaa" {
  domain_id   = linode_domain.liken_sh.id
  name        = "*"
  record_type = "AAAA"
  target      = split("/", linode_instance.redirects.ipv6)[0]
}
