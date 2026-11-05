# A machine with IPv6 serves its Services on IPv4 only

Open problem, and an open design question. A `liken` machine on a
network that sends router advertisements gets a global IPv6 address
from SLAAC and a default route, and the machine can use them. But the
cluster is single-stack IPv4, so a `LoadBalancer` `Service`, such as
the one that `servicelb` makes for Traefik, answers on the machine's
IPv4 address only. A site that the cluster serves can therefore have
no AAAA record. The Machine's status also does not show the IPv6
address, so nothing in the cluster tells you that the machine has one.

## The recorded case

On 2026-11-04, a one-node cluster on a Linode instance ran
`2026.11.02-002` with the `traefik` and `servicelb` features. A pod on
the host network read the machine's IPv6 state:

* `net.ipv6.conf.{all,default,eth0}.forwarding` was `0`, and
  `accept_ra` was `1`, as `machine/sysctldefaults.go` intends.
* `eth0` had a global address from SLAAC, `scope global dynamic`,
  with a default route through the router's link-local address, which
  the router advertisement supplied.
* The routing table had no on-link route for the address's `/64`.
  Traffic to other hosts in that `/64` went through the router, and it
  arrived.

From the machine, ping to a public IPv6 address and to a second
instance in the same region succeeded. From the second instance:

* `https://[<address>]:6443/version` answered with a 401 in a few
  milliseconds, because the API server listens on `[::]:6443`.
* Port 80 refused the connection at once. The provider's firewall
  admitted the port from every IPv6 source, so the refusal came from
  the machine: nothing listened for IPv6 on port 80.

`servicelb` exposes a `LoadBalancer` `Service` with a pod on each
machine that writes DNAT rules for the cluster's address families.
The cluster has one IPv4 `clusterCIDR` and one IPv4 `serviceCIDR`, so
it writes IPv4 rules only, and the IPv6 address gets none.

The Machine's `status.network.addresses` and
`status.network.interfaces[].address` listed only the IPv4 address of
`eth0`, from its DHCP lease. The interface's `address` field holds one
address, so it has no place for a second address from SLAAC.

## Why this is not a quick change

* A dual-stack cluster needs an IPv6 range in `clusterCIDR` and in
  `serviceCIDR`, and an IPv6 node address. The Cluster reference
  states that those network fields cannot change after the install,
  so an existing cluster would need a reinstall to become dual-stack.
* `spec.network.nodeCIDR` takes one subnet, and a node's IPv6 address
  from SLAAC is not known before the machine boots.
* A Machine interface's `address` field takes one address, and an
  empty field selects DHCP. So an interface cannot have both a DHCP
  lease for IPv4 and a static IPv6 address, such as an address from a
  routed range that stays with the site when the instance changes.

## Directions not yet weighed

* Support dual-stack clusters: two ranges in each network field, and
  the node's IPv6 address given to k3s with `--node-ip`. This is the
  general answer, and it is the largest.
* Run the ingress controller on the host network, so it listens on
  `[::]:80` and `[::]:443` and the cluster stays single-stack. This
  serves IPv6 to the ingress only, and it gives up the
  `LoadBalancer` `Service` for the ingress.
* Let a Machine interface take more than one address, or an IPv6
  address in addition to its DHCP lease, so a machine can hold an
  address that DNS names for a long time.
* Report every global address of an interface in the Machine's
  status, including the addresses from SLAAC.

## What is not measured

Only one machine, on one provider, was checked. A network that sends
no router advertisements, or one that assigns IPv6 with DHCPv6, was
not tested.
