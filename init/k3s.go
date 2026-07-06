package main

// From one machine to a cluster: deciding what this machine is, and
// telling k3s.
//
// k3s draws the same line liken does: leaders run a control plane,
// followers run workloads and take direction (k3s's names for the
// same roles are "server" and "agent", and those words appear here
// only where k3s's own files and flags demand them). Which one this
// machine should be is not the machine's own business to declare:
// the Cluster manifest names the leaders, and a machine derives its
// role by looking for its own name in that list. Everything
// role-specific about starting k3s follows from that one derivation.
//
// k3s is configured by file, not flags (the supervisor's empty
// argument lists are deliberate), and its config loader has a feature
// built for exactly liken's situation: alongside a config file, k3s
// reads every *.yaml in a sibling <name>.yaml.d/ directory as
// drop-ins. So the split is: what a person decided lives in the
// image's static files (/etc/rancher/k3s/config.yaml for leaders,
// agent.yaml for followers, both reviewable in the repo), and what
// only the boot can know (this machine's node IP, the cluster's
// address plan, where the join token sits) lands in a drop-in
// written here. Init never rewrites a file a person wrote.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/chrisguidry/liken/machine"
)

const (
	// The static halves, shipped in the image.
	k3sServerConfig = "/etc/rancher/k3s/config.yaml"
	k3sAgentConfig  = "/etc/rancher/k3s/agent.yaml"

	// tokenPath is where the image carries the cluster's join token,
	// minted offline by identity/mint.sh like the CAs it hashes.
	// Handed to k3s as a token-file so the secret itself never
	// appears in a config file or on a command line.
	tokenPath = "/etc/liken/token"
)

// leaderJoinConfig decides a leader's datastore keys, by leader
// count. One leader is exactly the cluster liken has always run:
// sqlite (via kine), no etcd, nothing to join — single-node stays
// cheap on purpose. More than one means embedded etcd, and the first
// entry in spec.leaders is the founding leader: it renders
// cluster-init: true, which on the migration boot tells k3s to move
// the existing sqlite datastore into etcd in place (the documented
// path that made starting on sqlite safe rather than a dead end).
// Every other leader points server: at the founder — its declared
// address on the node network, or the endpoint when it declares none
// — and joins. Rejoins keep the same flags every boot, which is
// k3s's recommended steady state.
//
// The founding leader is a config-derivation role, nothing more: the
// first name in a list. etcd's raft leader is elected and moves
// between members; the founder holds no such office, and once the
// cluster is up it is one voice among an odd number.
func leaderJoinConfig(cluster *machine.Cluster, name, manifestDir string) (clusterInit bool, joinURL string) {
	if cluster == nil || len(cluster.Spec.Leaders) < 2 {
		return false, ""
	}
	founder := cluster.Spec.Leaders[0]
	if name == founder {
		return true, ""
	}
	if addr := declaredNodeAddress(cluster, manifestDir, founder); addr != "" {
		return false, fmt.Sprintf("https://%s:6443", addr)
	}
	return false, cluster.Spec.Endpoint
}

// nodeAddress picks which of the machine's addresses is its node IP:
// the address Kubernetes traffic uses, and the one other nodes are
// told to reach it at. The Cluster's nodeCIDR decides: the interface
// whose address falls inside it is the cluster-facing one. A machine
// with several interfaces needs this to be explicit; k3s left to
// itself picks the interface holding the default route, which on a
// machine with an internet uplink is exactly the wrong one.
func nodeAddress(cluster *machine.Cluster, conns []*connection) (ip, ifname string) {
	if cluster == nil || cluster.Spec.Network.NodeCIDR == "" {
		return "", ""
	}
	_, subnet, err := net.ParseCIDR(cluster.Spec.Network.NodeCIDR)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: cluster nodeCIDR %q: %v\n", cluster.Spec.Network.NodeCIDR, err)
		return "", ""
	}
	for _, conn := range conns {
		if conn.addr != nil && subnet.Contains(conn.addr.IP) {
			return conn.addr.IP.String(), conn.ifname
		}
	}
	return "", ""
}

// k3sBootConfig renders the drop-in: everything k3s must be told that
// only this boot could decide. Plain key: value lines, because every
// value here is a string k3s maps onto one of its flags.
func k3sBootConfig(role string, cluster *machine.Cluster, nodeIP, nodeInterface string, haveToken, clusterInit bool, joinURL string) string {
	var b strings.Builder
	b.WriteString("# Written by liken at boot: the configuration only the boot can\n")
	b.WriteString("# derive, joined with the static file this directory sits beside.\n")

	// The join token, for both roles: the leader requires exactly this
	// token from anyone joining, and a follower presents it. Because
	// the token embeds a hash of the cluster CA, a follower also uses
	// it to verify it is joining the cluster it thinks it is.
	if haveToken {
		fmt.Fprintf(&b, "token-file: %s\n", tokenPath)
	}

	if role == machine.RoleFollower {
		// "server" is k3s's config key for "the control plane I take
		// direction from": a follower points at the endpoint.
		fmt.Fprintf(&b, "server: %s\n", cluster.Spec.Endpoint)
	} else if cluster != nil {
		// The datastore keys, decided by leaderJoinConfig: the
		// founding leader of a multi-leader cluster runs (and, on the
		// migration boot, creates) embedded etcd; the other leaders
		// join it. A single leader renders neither and stays sqlite.
		if clusterInit {
			b.WriteString("cluster-init: true\n")
		} else if joinURL != "" {
			fmt.Fprintf(&b, "server: %s\n", joinURL)
		}
		// The cluster's address plan is leader configuration;
		// followers learn it from the control plane they join.
		net := cluster.Spec.Network
		for _, entry := range []struct{ key, value string }{
			{"cluster-cidr", net.ClusterCIDR},
			{"service-cidr", net.ServiceCIDR},
			{"cluster-dns", net.ClusterDNS},
			{"cluster-domain", net.ClusterDomain},
		} {
			if entry.value != "" {
				fmt.Fprintf(&b, "%s: %s\n", entry.key, entry.value)
			}
		}
	}

	// The node IP and the interface it lives on, when the Cluster's
	// nodeCIDR identified one. node-ip is what the kubelet advertises;
	// flannel-iface is which wire pod-to-pod traffic rides. They must
	// agree, and they must both point at the cluster segment.
	if nodeIP != "" {
		fmt.Fprintf(&b, "node-ip: %s\n", nodeIP)
		fmt.Fprintf(&b, "flannel-iface: %s\n", nodeInterface)
	}
	return b.String()
}

// persistNodePassword gives k3s's node password a durable home. On
// its first join, a machine mints a random secret (its "node
// password"), the leader records it, and every reconnect after must
// present the same one: it's what stops a stranger from registering
// as an existing node and receiving its kubelet certificates. k3s
// keeps it at /etc/rancher/node/password — which on liken is the RAM
// root, where it would vanish every reboot and the machine would
// knock on its own cluster's door with the wrong secret. The password
// is machine identity, and the machine's durable identity data lives
// on machineState, so /etc/rancher/node becomes a symlink onto that
// filesystem. A machine whose machineState is memory-backed keeps the
// tmpfs default: nothing about it survives reboots anyway.
func persistNodePassword(storage machine.StorageStatus) {
	if storage.MachineState.Backing != machine.BackingPartition {
		return
	}
	dir := filepath.Join(machine.MachineStateDir, "node")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "liken: node identity: %v\n", err)
		return
	}
	if err := os.MkdirAll("/etc/rancher", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "liken: node identity: %v\n", err)
		return
	}
	if err := os.Symlink(dir, "/etc/rancher/node"); err != nil {
		fmt.Fprintf(os.Stderr, "liken: node identity: %v\n", err)
		return
	}
	fmt.Printf("liken: node identity: /etc/rancher/node persists on machineState\n")
}

// writeK3sBootConfig derives this machine's role and k3s
// configuration and writes the drop-in beside the role's static
// config file. It returns the role so the supervisor knows which k3s
// to start.
func writeK3sBootConfig(cluster *machine.Cluster, name string, conns []*connection) (string, error) {
	role := cluster.Role(name)
	if cluster != nil {
		fmt.Printf("liken: this machine is a cluster %s (cluster %s)\n", role, cluster.Metadata.Name)
	}
	if role == machine.RoleFollower && cluster.Spec.Endpoint == "" {
		return role, fmt.Errorf("this machine is a follower, but the cluster manifest declares no endpoint to join")
	}

	haveToken := true
	if _, err := os.Stat(tokenPath); err != nil {
		haveToken = false
		if role == machine.RoleFollower {
			return role, fmt.Errorf("this machine is a follower, but the image carries no join token at %s", tokenPath)
		}
	}

	nodeIP, nodeInterface := nodeAddress(cluster, conns)
	if nodeIP != "" {
		fmt.Printf("liken: node IP is %s on %s\n", nodeIP, nodeInterface)
	} else if role == machine.RoleFollower {
		fmt.Fprintf(os.Stderr, "liken: no address falls inside the cluster's nodeCIDR; k3s will guess a node IP\n")
	}

	clusterInit, joinURL := leaderJoinConfig(cluster, name, machine.MachineManifestDir)
	if clusterInit {
		fmt.Println("liken: this machine is the founding leader; embedded etcd runs here")
	} else if joinURL != "" {
		fmt.Printf("liken: joining the control plane at %s\n", joinURL)
	}

	base := k3sServerConfig
	if role == machine.RoleFollower {
		base = k3sAgentConfig
	}
	dropInDir := base + ".d"
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		return role, err
	}
	content := k3sBootConfig(role, cluster, nodeIP, nodeInterface, haveToken, clusterInit, joinURL)
	if err := os.WriteFile(filepath.Join(dropInDir, "boot.yaml"), []byte(content), 0o644); err != nil {
		return role, err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(content), "\n") {
		if !strings.HasPrefix(line, "#") {
			fmt.Printf("liken: k3s config: %s\n", line)
		}
	}
	return role, nil
}
