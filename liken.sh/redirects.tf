# The redirects from each manual's name to its section of liken.sh.
# Each operator's API group, such as bluetooth.liken.sh, and each CSI
# driver's name, such as git.liken.sh, is also the address of its
# manual:
#
#     https://display.liken.sh/docs/guides/install/
#       -> https://liken.sh/display/docs/guides/install/
#
# Neither Linode DNS nor GitHub Pages serves a redirect, so a
# Kubernetes cluster runs redirects/ as a workload and answers. This
# file sends the names to that cluster. README.md explains the rest.

# The cluster that runs redirects/ answers the manuals' names. Its
# address is a Linode reserved IPv4 address, which stays the same when
# the cluster's machine is replaced. The cluster has no reserved IPv6
# address, so the names have no AAAA record.
variable "redirects_ipv4" {
  description = "The public IPv4 address of the cluster that runs liken.sh/redirects"
  type        = string
  default     = "172.104.31.162"
}

# The wildcard record sends every subdomain of liken.sh to the
# cluster. In DNS, a name with a record of its own never matches a
# wildcard, so the apex, www, releases, log, and the verification
# records keep their answers, and a manual's name must have no record
# of its own. A name that the cluster's Ingress does not list gets a
# 404 over plain HTTP, and the cluster's default certificate over
# HTTPS.

resource "linode_domain_record" "redirects_a" {
  domain_id   = linode_domain.liken_sh.id
  name        = "*"
  record_type = "A"
  target      = var.redirects_ipv4
}
