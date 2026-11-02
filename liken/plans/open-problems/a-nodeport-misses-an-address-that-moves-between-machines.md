# A NodePort misses an address that moves between machines

Open problem, and an open design question. When a cluster sets
`spec.network.nodePortCIDRs`, kube-proxy opens each NodePort only on
the local addresses that it found at its last sync. An address that
arrives on a machine later, such as a virtual IP that kube-vip moves
to another leader, gets no NodePort until kube-proxy syncs again. On a
quiet cluster, that can take up to an hour. During that time, every
NodePort on the address refuses connections.

## The recorded failure

On 2026-11-02, a home cluster with several leaders finished its rollout to
`2026.11.01-001`. Its leaders share a virtual IP that kube-vip
advertises with ARP, and the cluster's git forge takes SSH on a
NodePort of that address. Flux syncs the cluster through that port.

About three minutes after the last machine rebooted, the API server
on the leader that held the virtual IP did not answer kube-vip's lease
renewal within its 3-second deadline. kube-vip on that leader gave up
the lease, and another leader took the lease and the address. From
then on, every NodePort on the virtual IP refused connections, and
Flux could not fetch the repository. The same NodePorts answered on
each machine's own address. Ports that a pod serves on the host
network, such as the API server's 6443, answered on the virtual IP.

On the new holder:

* kube-proxy ran with `--proxy-mode=iptables` and
  `--nodeport-addresses` set to two subnets, the node network and a
  tunnel network, which `liken/init` renders from `nodePortCIDRs`. The
  virtual IP is inside the node network's subnet.
* `iptables-save -t nat` held one `KUBE-NODEPORTS` jump for each of
  the machine's own two addresses, and none for the virtual IP. `ip
  addr` showed the virtual IP on the interface, as a `/32`.
* kube-proxy's metric `kubeproxy_sync_proxy_rules_last_timestamp_seconds`
  dated its last sync two minutes before the address arrived.

A rollout restart of one Deployment changed an `EndpointSlice`, and
kube-proxy synced. The jump for the virtual IP appeared, and the
NodePorts answered within five seconds. An annotation on a `Service`
did not cause a sync, because it changes no proxy rule.

The same failure happened once during the rollout itself, and it
ended in under five minutes with no action. That is consistent with
the many `EndpointSlice` changes of a rollout, which make kube-proxy
sync often. Nobody measured how earlier moves of the address ended.

## Why kube-proxy does not see the address

With a list of CIDRs, kube-proxy's iptables proxier calls
`GetNodeIPs` on each sync. That function lists the machine's interface
addresses, keeps the ones inside the CIDRs, and writes one rule per
address: `-A KUBE-SERVICES -d <address>/32 -j KUBE-NODEPORTS`. The
list of addresses is fixed until the next sync.

kube-proxy syncs when a `Service` or an `EndpointSlice` changes in a
way that changes its rules, and otherwise once an hour
(`FullSyncPeriod` in `pkg/proxy/util`). It does not watch the
interface addresses. Its `--iptables-sync-period` sets only how often
it checks that its canary chain still exists.

When the list holds `0.0.0.0/0`, kube-proxy writes one rule with
`-m addrtype --dst-type LOCAL` instead. The kernel evaluates that
match for each packet, so a new address is covered at once. `liken`
does not render that form, because it opens the NodePorts on every
local address, including the uplink and the interfaces of the CNI.

The default of `nodePortCIDRs` does not have this failure, because
`liken` renders kube-proxy's `primary` keyword, and that keyword
opens the NodePorts only on the node IP, which does not move. It
never opens them on a virtual IP.

## What else is affected

The `nodePortCIDRs` description in `liken/cluster/cluster.go` and on
the `Cluster` reference page says that a NodePort answers on "every
local address inside any of them". That is true only for the
addresses present at kube-proxy's last sync. Correct it with the
remedy.

## Possible remedies

None of these is selected.

1. **Force a sync when an address arrives.** A `liken` component on
   each machine subscribes to netlink address events, then reads the
   addresses once, as the event rule in the root `AGENTS.md`
   describes. When an address inside `nodePortCIDRs` appears or
   disappears, it deletes kube-proxy's canary chain
   `KUBE-PROXY-CANARY`. kube-proxy's monitor notices the missing chain
   within its sync period, 30 seconds by default, and runs a full
   sync. This uses a recovery path that kube-proxy has for itself,
   but it depends on an internal chain name.
2. **Match local addresses in the kernel, and filter the rest.**
   Render `0.0.0.0/0`, so kube-proxy matches every local address for
   each packet, and add a `liken` rule ahead of `KUBE-NODEPORTS` that
   drops NodePort traffic to an address outside `nodePortCIDRs`. The
   coverage is immediate, but `liken` then writes iptables rules of
   its own beside kube-proxy's.
3. **Guide deployments away from NodePorts on a moving address.** A
   `LoadBalancer` `Service`, served by `servicelb` or by kube-vip's
   service mode, gets rules for its own address that do not depend on
   the node's interface addresses. The `Cluster` reference would say
   that `nodePortCIDRs` does not cover an address that moves. This
   changes no code, but it leaves the failure in place for every
   deployment that does not read the warning.
4. **Ask upstream.** kube-proxy could resync when an interface address
   inside `--nodeport-addresses` changes. That would fix the failure
   for every distribution, but it gives no date.

The work needs a design decision between these remedies.
