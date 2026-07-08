# Growing the cluster past one node

Milestone 6 — Done

Growing the cluster past one node, driven by a `kind: Cluster`
resource: one leader and one follower, with every decision made
explicitly rather than discovered at runtime. The join token is
an input like the rest of identity: k3s's secure token format
is `K10<CA-hash>::user:pass`, and the CA it hashes is the
server CA that identity/ already mints, so make computes the
whole token offline and bakes it into the identity bundle.
The spec carries topology; the identity bundle carries secrets;
nothing is ever extracted from a running machine (and a machine
with no shell has no way to extract secrets anyway). Machines
get static addresses declared in their manifests, and a machine
finds its own manifest by `liken.machine=<name>` on the kernel
command line, the one input channel the bootloader already
controls. One boot medium carries cluster.yaml and a manifest
per machine (node-1.yaml, node-2.yaml, ...), so a single image
boots the whole fleet. Which fleet it boots is a *deployment's*
decision, not the OS's: the manifests are an input to the image
build. The repo's own deployment is the dev-cluster/ domain,
which holds those manifests and the QEMU guests that boot them.
1. [x] The Cluster CRD: cluster.yaml is file-delivered like the
   Machine manifest and seeded by the operator; every node's
   operator races to create it, and the losers treat their 409
   responses as success. spec.leaders names the machines that
   run control planes, and spec.network holds the facts k3s
   requires every node to agree on (cluster CIDR, service
   CIDR, cluster DNS, cluster domain), cluster-scoped facts
   even though k3s configures them as per-node flags. It also
   holds nodeCIDR, the subnet nodes address each other on. A
   machine's role is derived, not declared: a machine is a
   leader if its name appears in spec.leaders. Promoting a
   follower is then a Cluster edit, not a coordinated pair of
   Machine edits.
2. [x] The token joins the identity bundle: mint.sh hashes the
   server CA, appends a random secret, and writes the token
   next to the TLS material. This is idempotent: re-running
   mint.sh fills gaps but never replaces an identity machines
   already carry. The token lives at /etc/liken/token, outside
   k3s's data directory, because the clusterState filesystem
   mounts over that. Init gives k3s the *path* (token-file), so
   the secret never appears in a config file or on a command
   line.
3. [x] Static networking: spec.network grows an interfaces list
   (name, address in CIDR form, optional gateway and
   nameservers; no address means DHCP, and an empty spec still
   means DHCP on the first real NIC). This was an open
   problem; the lab forced it onto the critical path, because
   the shared segment joining two QEMU guests is a dumb wire
   with no DHCP server on it. Each machine runs two
   interfaces: a DHCP uplink and the statically-addressed
   cluster segment, and the Cluster's nodeCIDR is what picks
   which address becomes the node IP (left to itself, k3s picks
   the interface with the default route, which is the uplink
   and exactly the wrong choice).
4. [x] liken.machine=: init reads its name from the kernel command
   line and selects its seed from the manifests the image
   carries; after first boot, machineState carries the proven
   manifest forward exactly as before. Selection refuses to
   guess: a name matching no manifest, or many manifests with
   no name, powers the machine off after printing the reason
   to the console. A first boot under the wrong identity could
   join the wrong cluster or claim another machine's disks,
   which is worse than failing to boot. A cluster manifest
   that won't parse is fatal the same way: a machine that
   can't tell if it's a
   leader must not guess, because guessing "leader" starts a
   rival control plane.
5. [x] The lab grows a node dimension: per-node dist directories,
   MACs, and command lines. Each guest gets two NICs: user-mode
   NAT stays as each guest's internet uplink, and a multicast
   socket segment (no root, no bridges: every QEMU naming the
   same group is one virtual hub) is the wire the cluster
   communicates over. The API-server hostfwd lives on the
   leader node only. Two terminals (`make run`, `make run
   NODE=node-2`) show two serial consoles side by side. A
   supporting discovery: k3s reads drop-in config from
   <config>.yaml.d/, so the image's static files stay untouched
   and init writes only a boot.yaml drop-in of derived facts.
   Followers also need their own config file entirely, because
   `k3s agent` refuses leader-only keys.
6. [x] Prove it: `kubectl get nodes` shows two Ready nodes,
   `kubectl get machines` shows a leader and a follower with
   their segment addresses, `kubectl get clusters` shows the
   topology, a pod pinned to the follower runs with a
   cluster-CIDR address and resolves cluster DNS across the
   VXLAN, and both machines come back from a power cut booting
   their Proven manifests, with the cluster and the pod intact.
   One discovery came out of this: on first join, k3s creates a
   "node password" for each node, records it server-side, and
   requires the same one on every reconnect. That is what stops
   a stranger from registering as an existing node. k3s keeps
   the password at /etc/rancher/node/password, which on liken
   was the RAM root, so a rebooted follower presented a freshly
   generated password to its own cluster and was refused. The
   password is machine identity, so /etc/rancher/node is now a
   symlink onto machineState. The reliable way to verify a
   re-join is the node's kube-node-lease renewTime, because
   Node status replayed from the persisted datastore reads
   Ready for a while whether or not the kubelet actually came
   back.
