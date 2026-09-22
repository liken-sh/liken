# The operator on the host network

Plan 05.

## The problem

An equipment protocol has two halves. The commands and the state go
over unicast: TCP to a Denon on port 23, HTTPS to a WiiM. Finding the
device in the first place is link-local multicast. SSDP, mDNS, and
whatever Cast uses all broadcast on the LAN, and the set of them grows
as the operator grows. A receiver, an amplifier, a television, and a
phone that casts to the screen are four protocols and one discovery
problem.

The operator today is a single-replica Deployment with ordinary pod
networking. That reaches a declared address and nothing else. On the
house cluster, a normal pod on the flannel overlay answered a WiiM on
port 443 over unicast, got no responders to an SSDP M-SEARCH, and saw
no LAN responder to an mDNS query. A hostNetwork pod on the same node
returned every device on the LAN to the same search, and mDNS listed
all three WiiM amps with their UUIDs. A one-off hostNetwork Job
resolved each device to its UDN and its location.

Without host network there is no discovery, so every device needs a
declared address, and a house on DHCP needs a reservation and a name
for each one. That is not plug and play, which is what this operator
is for.

This plan gives the operator host network, keeps it a Deployment, and
moves discovery into the process. It also fixes the shape for
equipment that attaches to a node, which is not this plan's to build.

## The design

### One Deployment on the host network

The operator stays one Deployment at one replica and gains
`hostNetwork: true`. The pod runs on one node and shares that node's
network namespace, so its SSDP and mDNS queries reach the LAN and the
answers come back to it. Everything else stays as it is: the same
image, the same drivers, the same status writes through the API
server.

A node selector or affinity pins the pod to a node on the equipment's
segment. In a one-segment house every node qualifies. A cluster that
spans segments needs one manager per segment, which is an open problem
below and not this plan's work.

Nothing in the `equipment.Driver` contract changes. A driver reaches
its device by unicast once it has an address, and the address comes
from discovery or from the spec.

### Discovery in the process

The operator sends SSDP M-SEARCH and browses mDNS from its own network
namespace. It resolves a declared identity to a current address: the
LinkPlay UUID for a WiiM, and whatever identity each later protocol
carries. It runs at startup and after a failed connect, it caches the
answer, and it repeats the search rather than trusting one response.
One Job run missed one of the three amps in a six-second window, so a
single M-SEARCH is not enough.

The address is never the identity. A `spec.wiim` block declares the
`uuid` and may declare an `address` as a hint. The driver reads
`getStatusEx`, compares `uuid`, and rediscovers when the hint does not
match, so an address that moved never drives the wrong amp. The
identity and address rules live in `wiim/AGENTS.md`, and the WiiM
driver itself is plan 06.

### The metrics port

Host network moves the metrics listener into the node's network
namespace, where it collides with the other host-network pods on that
node. Port 9200 is the port every liken process shares, and
`liken-machine-operator` already holds 9200 as a host port on every
node. `bluetooth-operator` took 9250 for the same reason. This
operator leaves 9200 and takes 9260 from that series, and the
Prometheus scrape moves with it.

### The namespace and the grant

The pod needs a namespace that admits host network. `liken-system`
already runs host-network DaemonSets, and the operator deploys there
the way its siblings do. The grant is one pod and not one pod per
node, which is the whole of the privilege this plan adds.

### Node-attached equipment

CEC over a USB adapter, serial, and IR attach to a node and not to a
network. This plan does not build them. The CEC adapter is also the
only way to wake a TV from the cluster, because the receiver is a CEC
responder and originates nothing. When the three land, they run on the
nodes that carry the hardware, because that is where the device is.
The split is attachment and not protocol: network equipment is the
Deployment, and wired equipment is a component on its node. The
`equipment.Driver` contract stays in-process in whichever workload
runs the driver, so the binary grows a second mode and the contract
does not change.

## Phases

1. The Deployment gains `hostNetwork` and `ClusterFirstWithHostNet`,
   the metrics listener moves to 9260, and discovery runs in the
   process. The search is `wiim.Discover` in `wiim/discovery.go`, and
   the loop that consumes it and reconciles the Receivers the operator
   owns is `discovery.go`. This is the built phase.

## What was considered and set aside

- **A DaemonSet with host network and a Lease.** The network work is
  cluster-wide, so the DaemonSet runs a host-network pod on every node
  to do the work of one. A Lease elects the work and does not reduce
  the grant: every pod holds host network, and the ones without the
  Lease do nothing. The shape becomes right when a cluster spans
  segments and needs one manager per segment.

- **A transient hostNetwork Job, with the operator unprivileged.**
  Measured and it works: a one-off Job returned each device's UDN and
  its location. Set aside because the answer has to cross back to the
  operator through a ConfigMap, a status write, or a log line, and the
  Job runs again for each discovery. One host-network pod does the
  same work in-process. The Job stays the fallback if the grant is
  ever refused.

- **A DHCP reservation and a DNS name per device.** Stable and
  unprivileged, and for a fixed rack it is the simplest answer. Set
  aside because it is not plug and play, and this operator is for a
  house whose devices are on DHCP.

- **Per-protocol workloads**, one Deployment for Denon, one for WiiM,
  one DaemonSet for CEC. Set aside because the network protocols are
  one domain and none of them is privileged. Separate workloads add a
  wire between them and a lifecycle to manage, and isolate nothing.
  The split that matters is by attachment, not by protocol.

## Verification

The measurements above ran on the house cluster on 2026-09-20,
from a flannel pod, a hostNetwork pod on one node, and a one-off
hostNetwork Job.

The plan is proved when the operator, built as a host-network
Deployment, discovers the house's three WiiM amps and the Denon
without a declared address and drives them, and when the WiiM driver
reports them under `status.wiim`.

## What this leaves for later

- A cluster that spans network segments. Host network gives the
  operator one node's LAN, so a per-segment manager and the Lease that
  names it wait for that.
- The shape of the node-attached component for CEC, serial, and IR:
  one component for all three, on the nodes that carry the hardware,
  and how it reports to the operator.
- The metrics port number, and whether the scrape targets the node's
  address or a Service.
- Whether the session's MQTT bus is reachable from a host-network pod
  the same way it is from a normal one. The drill checks it.
