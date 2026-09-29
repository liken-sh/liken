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
# Flatcar release in flatcar.mk, redirects.bu, the Caddyfile, and the
# two variables. A change to any of them replaces the instance, and
# nothing on the host changes by hand. The Flatcar pin sets only the
# release that a new host boots first. Flatcar then updates the host
# to the current stable release and follows the stable channel, so a
# host that is a year old runs a current release, and a replacement
# is never needed for an update. The host has no SSH. README.md gives
# the way in for an emergency.

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

# The image comes from Amazon ECR Public's copy of Docker's official
# images. Its index digest is the same as the digest of Docker Hub's
# library/caddy:2.11.4, so the bytes are the same. Docker Hub counts
# anonymous pulls for each IPv4 address or IPv6 /64 against a small
# limit, and the host's address can share that count with other
# machines. ECR Public limits an anonymous client to one pull each
# second.

variable "caddy_image" {
  description = "The official Caddy image that the redirect host runs, with its tag and its digest"
  type        = string
  default     = "public.ecr.aws/docker/library/caddy:2.11.4@sha256:0c994536bddb66445885237f1a5dcc1916bccea922661c76b4e9fc24061f9b52"

  # Docker pulls by the digest when a reference has one, so the tag
  # is only for the reader. Without a digest, a new push to the tag
  # would change the host with no change here.
  validation {
    condition     = can(regex("@sha256:[0-9a-f]{64}$", var.caddy_image))
    error_message = "The image reference must end in @sha256:<digest>."
  }
}

# The Flatcar image, uploaded as a private Linode image. flatcar.mk
# holds the pin, and this file reads the version and the MD5 digest
# from the same lines that the Makefile includes. The MD5 digest is
# the image's identity for Terraform: a new pin replaces the image,
# and the instance below with it. The provider reads the file only
# when it creates the image, so a plan needs no download, and only an
# apply that creates the image needs `make flatcar` first. After the
# upload, the provider records the MD5 digest of the bytes it sent,
# and an apply fails if they are not the bytes that flatcar.mk names.
#
# 4459.2.4 is the newest stable release with Flatcar's first disk
# layout, whose image is 4,756,340,736 bytes uncompressed. Linode
# refuses an image larger than 6144 MiB, and the images of the later
# releases, with Flatcar's larger layout, are about 8 GB. An update
# does not change the partitions, so the host keeps the first layout,
# and Flatcar keeps updating that layout: it plans to drop it no
# earlier than 2030 (github.com/flatcar/Flatcar/issues/1917).
#
# Linode offers the metadata service, which carries the Ignition
# config, only to an image that is marked for cloud-init, so the
# upload sets cloud_init. Its region is the one the release bucket
# uses, and us-east offers the metadata service.

locals {
  flatcar = {
    for pair in regexall("(?m)^(FLATCAR_[A-Z0-9]+) := (\\S+)$", file("${path.module}/flatcar.mk")) :
    pair[0] => pair[1]
  }
  flatcar_image = "${path.module}/flatcar/${local.flatcar.FLATCAR_VERSION}/flatcar_production_akamai_image.bin.gz"
}

resource "linode_image" "flatcar" {
  label       = "flatcar-${replace(local.flatcar.FLATCAR_VERSION, ".", "-")}"
  description = "Flatcar Container Linux ${local.flatcar.FLATCAR_VERSION}, stable, the first boot of the liken.sh redirect host"
  region      = "us-east"
  cloud_init  = true

  file_path = local.flatcar_image
  file_hash = local.flatcar.FLATCAR_MD5
}

# The Ignition config, from the Butane file and the Caddyfile. The ct
# provider transpiles Butane inside the plan, so no rendered JSON is
# ever in history to drift from its source, and strict mode makes a
# Butane warning an error.
#
# The Caddyfile reaches the user data byte for byte, so it carries no
# comments: a comment edit would replace the host and ask Let's
# Encrypt for every certificate again. Its explanation is here.
#
# Caddy gets a Let's Encrypt certificate for each name, and renews
# it, over HTTP-01 on port 80 or TLS-ALPN-01 on port 443. The
# challenges need no credential, so the host holds no DNS token. The
# list of names is fixed on purpose. A policy that asked for a
# certificate for any name that arrives would let a stranger spend the
# weekly Let's Encrypt quota of liken.sh by asking for random names,
# and GitHub Pages renews the apex certificate from that same quota.
#
# The one site block lists each name twice, as https and as http://.
# The HTTPS address makes Caddy manage the certificate. The http://
# address answers plain HTTP with the same 301, straight to
# liken.sh, so a name redirects over HTTP before it has its
# certificate. Caddy's HTTP server answers an ACME HTTP-01 challenge
# before it runs any site's routes (modules/caddyhttp/server.go), so
# the http:// address does not take the challenge from Caddy.
#
# {labels.2} is the third label from the right of the request's host,
# which is the name itself: display in display.liken.sh. {uri} is the
# path and the query of the request, so a deep link keeps its page.
#
# The two sites with no host answer 404 for every other request: a
# name that is not in the list, a host with a trailing dot, or the
# bare address over HTTP. Over HTTPS, a name without a certificate
# fails its TLS handshake before any site answers. The global options
# turn off the admin endpoint, which nothing uses; turn off saving
# the configuration, because the container's root filesystem is
# read-only; and stop HTTP/3, because the firewall passes TCP only.

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

# The firewall passes only the two ports that Caddy answers, and ICMP.
# Port 80 carries the HTTP-01 challenge and the plain HTTP redirects,
# and 443 carries the HTTPS redirects and the TLS-ALPN-01 challenge.
# ICMP carries the errors that the network sends back, and IPv6 needs
# one of them: a router does not fragment an IPv6 packet, so it sends
# Packet Too Big, and path-MTU discovery fails if the host never
# receives it. Every other inbound packet is dropped, SSH included. Outbound traffic passes,
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

  inbound {
    label    = "icmp"
    action   = "ACCEPT"
    protocol = "ICMP"
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
#
# The watchdog (Linode's Lassie) boots the instance again after it
# powers off. A reboot from inside the guest, such as the reboot
# after each Flatcar update, reaches Linode as a power-off, so without
# the watchdog the first update would leave the host down.

resource "linode_instance" "redirects" {
  label                = "liken-sh-redirects"
  region               = "us-east"
  type                 = "g6-nanode-1"
  firewall_id          = linode_firewall.redirects.id
  interface_generation = "legacy_config"
  watchdog_enabled     = true

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
# their answers, and a name in redirect_names must have no record of
# its own. A name with no record and no entry in redirect_names
# reaches the host too. It gets a 404 over plain HTTP, and its TLS
# handshake fails over HTTPS, because Caddy has no certificate for it.

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

# The instance's ID and addresses, for the checks and the reboot in
# README.md.

output "redirect_host" {
  description = "The redirect host's Linode ID and public addresses"
  value = {
    id   = linode_instance.redirects.id
    ipv4 = one(linode_instance.redirects.ipv4)
    ipv6 = split("/", linode_instance.redirects.ipv6)[0]
  }
}
