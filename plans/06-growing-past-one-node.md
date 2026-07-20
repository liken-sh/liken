# Growing the cluster past one node

Milestone 6 — Done

This milestone grows the cluster past one node. A `kind: Cluster`
resource drives the growth: one leader and one follower. Every
decision is explicit; init does not discover roles at runtime.

The join token is an input, like the rest of a machine's identity.
k3s's secure token format is `K10<CA-hash>::user:pass`. The CA that
the token hashes is the server CA that identity/ already mints, so
make computes the whole token offline and adds it to the identity
bundle.

The spec carries the cluster's topology. The identity bundle carries
the secrets. No process extracts a secret from a running machine. (A
machine with no shell has no way to extract secrets in any case.)

Machines get static addresses, declared in their manifests. A machine
finds its own manifest by `liken.machine=<name>` on the kernel command
line. This is the one input channel the bootloader already controls.

One boot medium carries cluster.yaml and a manifest per machine
(node-1.yaml, node-2.yaml, ...). A single image boots the whole fleet
this way. The choice of which fleet to boot belongs to the
*deployment*, not to the OS: the manifests are an input to the image
build. The repo's own deployment is the dev-cluster/ domain. It holds
those manifests and the QEMU guests that boot them.
1. [x] The Cluster CRD: cluster.yaml is file-delivered, like the
   Machine manifest, and the operator seeds it. Every node's operator
   tries to create cluster.yaml at the same time. Only one operator
   succeeds; the others receive a 409 response, which they treat as
   success. spec.leaders names the machines that run control planes.
   spec.network holds the facts that k3s requires every node to agree
   on: cluster CIDR, service CIDR, cluster DNS, and cluster domain.
   These facts are cluster-scoped, even though k3s configures them as
   per-node flags. spec.network also holds nodeCIDR, the subnet that
   nodes use to address each other. A machine's role is derived, not
   declared: a machine is a leader when its name appears in
   spec.leaders. Promoting a follower is then a single Cluster edit,
   not a coordinated pair of Machine edits.
2. [x] The token joins the identity bundle: mint.sh hashes the server
   CA, appends a random secret, and writes the token next to the TLS
   material. This process is idempotent. Re-running mint.sh fills
   gaps but never replaces an identity that a machine already
   carries. The token lives at /etc/liken/token, outside k3s's data
   directory, because the clusterState filesystem mounts over that
   directory. Init gives k3s only the token's *path* (token-file), so
   the secret never appears in a config file or on a command line.
3. [x] Static networking: spec.network gains an interfaces list. Each
   entry has a name, an address in CIDR form, and an optional gateway
   and nameservers. No address means DHCP, and an empty spec still
   means DHCP on the first real NIC. This was an open problem before
   this milestone. The lab forced a decision, because the shared
   segment that joins two QEMU guests is a plain wire with no DHCP
   server on it. Each machine runs two interfaces: a DHCP uplink and
   the statically addressed cluster segment. The Cluster's nodeCIDR
   picks which address becomes the node IP. Without this setting,
   k3s picks the interface with the default route. That interface is
   the uplink, which is the wrong choice.
4. [x] liken.machine=: init reads its own name from the kernel command
   line and selects its seed from the manifests that the image
   carries. After the first boot, machineState carries the proven
   manifest forward, the same as before. Selection never guesses. If
   a name matches no manifest, or many manifests have no matching
   name, init powers the machine off after it prints the reason to
   the console. A first boot under the wrong identity could join the
   wrong cluster or claim another machine's disks. Both outcomes are
   worse than a failed boot. A cluster manifest that will not parse
   is fatal for the same reason: a machine that cannot tell if it is
   a leader must not guess, because a wrong guess of "leader" starts
   a second, conflicting control plane.
5. [x] The lab grows a node dimension: per-node dist directories,
   MACs, and command lines. Each guest gets two NICs. User-mode NAT
   stays each guest's internet uplink. A multicast socket segment is
   the wire the cluster communicates over; it needs no root access
   and no bridges, because every QEMU process that names the same
   group forms one virtual hub. The API-server hostfwd runs on the
   leader node only. Two terminals (`make run` and `make run
   NODE=node-2`) show two serial consoles side by side. A supporting
   discovery: k3s reads drop-in config from <config>.yaml.d/, so the
   image's static files stay untouched, and init writes only a
   boot.yaml drop-in of derived facts. Followers also need their own
   separate config file, because `k3s agent` does not accept
   leader-only keys.
6. [x] Prove it: `kubectl get nodes` shows two Ready nodes. `kubectl
   get machines` shows a leader and a follower with their segment
   addresses. `kubectl get clusters` shows the topology. A pod
   pinned to the follower runs with a cluster-CIDR address and
   resolves cluster DNS across the VXLAN. Both machines come back
   from a power cut and boot their Proven manifests, with the
   cluster and the pod intact. This work found one thing: on first
   join, k3s creates a "node password" for each node, records it on
   the server, and requires the same password on every reconnect.
   This mechanism stops an unauthorized node from registering as an
   existing node. k3s keeps the password at /etc/rancher/node/password.
   On liken, that path was the RAM root, so a rebooted follower
   generated a new password, and its own cluster rejected it. The
   password is part of machine identity, so /etc/rancher/node is now
   a symlink to machineState. The reliable way to verify a re-join is
   the node's kube-node-lease renewTime. The persisted datastore
   replays the Node status as Ready for a while, whether or not the
   kubelet actually restarted, so the Node status alone cannot
   confirm a re-join.
</content>
