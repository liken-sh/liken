package main

// From one machine to a cluster: deciding what this machine is, and
// telling k3s.
//
// k3s draws the same line that liken does: leaders run a control
// plane, and followers run workloads and take direction (k3s's names
// for the same roles are "server" and "agent", and those words
// appear here only where k3s's own files and flags require them).
// Which role this machine should have is not the machine's own
// business to declare. The Cluster manifest names the leaders, and a
// machine derives its role by looking for its own name in that list.
// Everything role-specific about starting k3s follows from that one
// derivation.
//
// k3s is configured by file, not by flags (the supervisor's empty
// argument lists are deliberate), and its config loader has a
// feature built for exactly liken's situation: alongside a config
// file, k3s reads every *.yaml in a sibling <name>.yaml.d/ directory
// as drop-ins. So the split is this: what a person decided lives in
// the image's static files (/etc/rancher/k3s/config.yaml for
// leaders, agent.yaml for followers, both reviewable in the repo),
// and what only the boot can know (this machine's node IP, the
// cluster's address plan, where the join token sits) lands in a
// drop-in that this file writes. Init never rewrites a file that a
// person wrote.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// These are package variables rather than constants, so tests can
// point the derivations at files of their own making.
var (
	// The static halves, shipped in the image.
	k3sServerConfig = "/etc/rancher/k3s/config.yaml"
	k3sAgentConfig  = "/etc/rancher/k3s/agent.yaml"

	// tokenPath is where the image carries the cluster's join token,
	// minted offline like the CAs it hashes (see the identity
	// package). This code hands the token to k3s as a token-file, so
	// the secret itself never appears in a config file or on a
	// command line.
	tokenPath = "/etc/liken/token"

	// seedSourceDir is where the image bakes k3s's seed files, the
	// tree that seedClusterState copies onto the clusterState
	// filesystem.
	seedSourceDir = "/var/lib/rancher"
)

// leaderJoinConfig decides a leader's datastore keys, based on leader
// count. One leader is exactly the cluster that liken has always
// run: sqlite (via kine), no etcd, nothing to join. Keeping
// single-node cheap is deliberate. More than one leader means
// embedded etcd, and the first entry in spec.leaders is the founding
// leader. The founding leader renders cluster-init: true, which on
// the migration boot tells k3s to move the existing sqlite datastore
// into etcd in place (the documented path that made starting on
// sqlite safe rather than a dead end). Every other leader points
// server: at the founder, using the founder's declared address on
// the node network, or the endpoint when the founder declares no
// address, and joins. Rejoins keep the same flags every boot, which
// is k3s's recommended steady state.
//
// The founding leader matters only for deriving configuration: it is
// the first name in a list, nothing more. etcd's raft leader is
// elected and moves between members. The founder holds no ongoing
// special position, and once the cluster is up, it is an ordinary
// member among an odd number of them.
//
// An adopted cluster (spec.origin: Adopted) changes one assumption:
// the datastore already exists, in a cluster that liken did not
// create, and initializing a second one next to it would split the
// cluster in two. So under adoption, every leader joins: the founder
// through the endpoint (the one address that the existing control
// plane is known to be reachable at), and the others prefer the
// founder as usual. No leader renders cluster-init or falls back to
// sqlite, not even a lone leader. Each joining leader becomes an etcd
// member, and raft replicates the existing keyspace to it, so the
// cluster's state carries over without ever being exported or
// copied.
func leaderJoinConfig(clusterDoc *cluster.Cluster, name, manifestDir string) (clusterInit bool, joinURL string) {
	if clusterDoc == nil {
		return false, ""
	}
	if clusterDoc.Spec.Origin == cluster.OriginAdopted {
		if leaders := clusterDoc.Spec.Leaders; len(leaders) > 0 && name != leaders[0] {
			if addr := declaredNodeAddress(clusterDoc, manifestDir, leaders[0]); addr != "" {
				return false, fmt.Sprintf("https://%s:6443", addr)
			}
		}
		return false, clusterDoc.Spec.Endpoint
	}
	if len(clusterDoc.Spec.Leaders) < 2 {
		return false, ""
	}
	founder := clusterDoc.Spec.Leaders[0]
	if name == founder {
		return true, ""
	}
	if addr := declaredNodeAddress(clusterDoc, manifestDir, founder); addr != "" {
		return false, fmt.Sprintf("https://%s:6443", addr)
	}
	return false, clusterDoc.Spec.Endpoint
}

// nodeAddress picks which of the machine's addresses is its node IP:
// the address that Kubernetes traffic uses, and the one that other
// nodes are told to reach it at. The Cluster's nodeCIDR decides
// this. The interface whose address falls inside nodeCIDR is the
// cluster-facing one. A machine with several interfaces needs this
// choice to be explicit. If left to decide on its own, k3s picks the
// interface that holds the default route, which on a machine with an
// internet uplink is exactly the wrong interface.
func nodeAddress(clusterDoc *cluster.Cluster, conns []*connection) (ip, ifname string) {
	if clusterDoc == nil || clusterDoc.Spec.Network.NodeCIDR == "" {
		return "", ""
	}
	_, subnet, err := net.ParseCIDR(clusterDoc.Spec.Network.NodeCIDR)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: cluster nodeCIDR %q: %v\n", clusterDoc.Spec.Network.NodeCIDR, err)
		return "", ""
	}
	for _, conn := range conns {
		if conn.addr != nil && subnet.Contains(conn.addr.IP) {
			return conn.addr.IP.String(), conn.ifname
		}
	}
	return "", ""
}

// k3sBootInputs gathers everything that the drop-in needs and that
// only this boot could decide. writeK3sBootConfig fills it in, and
// k3sBootConfig renders it. This is a struct rather than a parameter
// list, because seven positional arguments with adjacent strings and
// bools invite mistakes. Named fields read correctly at the call
// site.
type k3sBootInputs struct {
	role          api.Role
	clusterDoc    *cluster.Cluster
	nodeIP        string
	nodeInterface string
	haveToken     bool
	clusterInit   bool
	joinURL       string
	nodeLabels    map[string]string
}

// k3sBootConfig renders the drop-in: everything that k3s must be
// told and that only this boot could decide. It renders plain
// key: value lines, because every value here is a string that k3s
// maps onto one of its flags.
func k3sBootConfig(in k3sBootInputs) string {
	role, clusterDoc := in.role, in.clusterDoc
	var b strings.Builder
	b.WriteString("# Written by liken at boot: the configuration only the boot can\n")
	b.WriteString("# derive, joined with the static file this directory sits beside.\n")

	// The join token applies to both roles: the leader requires
	// exactly this token from anyone joining, and a follower presents
	// it. Because the token embeds a hash of the cluster CA, a
	// follower also uses it to verify that it is joining the cluster
	// it thinks it is.
	if in.haveToken {
		fmt.Fprintf(&b, "token-file: %s\n", tokenPath)
	}

	if role == api.RoleFollower {
		// "server" is k3s's config key for "the control plane I take
		// direction from": a follower points at the endpoint.
		fmt.Fprintf(&b, "server: %s\n", clusterDoc.Spec.Endpoint)
	} else {
		// The disable list: which of k3s's bundled components
		// (Traefik, the service load balancer, metrics-server) stay
		// off. liken disables them on principle: anything beyond the
		// control plane should be a declared, visible workload. The
		// Cluster's spec.features is that declaration, so this code
		// computes the list as the bundled set minus the cluster's
		// opt-ins (DisabledComponents, in the cluster package). This
		// renders on leaders only, because disable is a server-side
		// key that an agent would refuse. It always renders as the
		// complete list, never a fragment merged with a default
		// somewhere else, so the value has exactly one author. A
		// machine with no cluster document disables everything
		// bundled: the minimum viable cluster is the default, and
		// features are always an opt-in.
		if disabled := clusterDoc.DisabledComponents(); len(disabled) > 0 {
			b.WriteString("disable:\n")
			for _, name := range disabled {
				fmt.Fprintf(&b, "  - %s\n", name)
			}
		}
		// The Helm controller is not on the disable list, because it
		// is not a deployable component. It is a controller compiled
		// into the k3s server process. It watches for HelmChart
		// resources to render, and it holds informer caches whether
		// or not any exist. On a small machine, this memory use is
		// worth naming, so this controller follows the same rule as
		// the bundled components: off unless the cluster declares the
		// helm feature, or a feature that requires it, the way
		// traefik does, since k3s deploys Traefik through a
		// HelmChart.
		if !clusterDoc.FeatureEnabled("helm") {
			b.WriteString("disable-helm-controller: true\n")
		}
		// The embedded cloud controller manager is in the same
		// position: a controller inside the k3s server process,
		// spending memory and holding a leader-election lease on
		// every leader. On real clouds, an external provider replaces
		// it. On bare metal, its only real job here is running the
		// service load balancer (klipper-lb lives inside it, since
		// k3s moved ServiceLB there), so it runs exactly when
		// servicelb is declared. Without it, the kubelet initializes
		// the node itself, addresses included, which is the ordinary
		// arrangement for a machine that runs in no cloud.
		if !clusterDoc.FeatureEnabled("servicelb") {
			b.WriteString("disable-cloud-controller: true\n")
		}
		// The network policy controller is likewise embedded. It
		// turns NetworkPolicy resources into per-node packet
		// filtering, which the flannel CNI cannot do alone. A cluster
		// that wants that enforcement declares it. On a cluster that
		// does not, the controller would spend its memory watching
		// for resources that never come. Without this controller,
		// Kubernetes accepts NetworkPolicy documents and enforces
		// nothing, which matches flannel's own behavior. This is why
		// the feature's absence is a safe default rather than a
		// broken one.
		if !clusterDoc.FeatureEnabled("network-policy") {
			b.WriteString("disable-network-policy: true\n")
		}
		if clusterDoc != nil {
			// The datastore keys, decided by leaderJoinConfig: the
			// founding leader of a multi-leader cluster runs embedded
			// etcd (and creates it, on the migration boot), and the
			// other leaders join it. A single leader renders neither
			// key and stays on sqlite.
			if in.clusterInit {
				b.WriteString("cluster-init: true\n")
			} else if in.joinURL != "" {
				fmt.Fprintf(&b, "server: %s\n", in.joinURL)
			}
			// The embedded registry mirror (Spegel) is a server-side
			// key. The control plane runs the coordination, and every
			// node participates through the mirror entries that init
			// renders into registries.yaml (registries.go).
			if clusterDoc.Spec.Registries.Embedded {
				b.WriteString("embedded-registry: true\n")
			}
			// The cluster's address plan is leader configuration.
			// Followers learn it from the control plane that they
			// join.
			net := clusterDoc.Spec.Network
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
	}

	// The node IP and the interface it lives on, when the Cluster's
	// nodeCIDR identified one. node-ip is what the kubelet
	// advertises. flannel-iface is which interface carries pod-to-pod
	// traffic. They must agree, and they must both point at the
	// cluster segment.
	if in.nodeIP != "" {
		fmt.Fprintf(&b, "node-ip: %s\n", in.nodeIP)
		fmt.Fprintf(&b, "flannel-iface: %s\n", in.nodeInterface)
	}

	// The spec's node labels, so the node registers with its
	// scheduling identity already set. A freshly reinstalled machine
	// must not spend its first minutes as a blank node that workloads
	// wrongly select against. The + suffix is k3s's append syntax for
	// list values. A plain node-label key in a drop-in would replace
	// the static file's list, erasing liken.sh/machine=true, and
	// appending is the whole point of a drop-in. This code sorts the
	// labels, so the same spec always renders the same bytes.
	if len(in.nodeLabels) > 0 {
		b.WriteString("node-label+:\n")
		for _, name := range slices.Sorted(maps.Keys(in.nodeLabels)) {
			fmt.Fprintf(&b, "  - %s=%s\n", name, in.nodeLabels[name])
		}
	}
	return b.String()
}

// k3sServerDB is where k3s keeps the control plane's datastore
// (sqlite via kine, or embedded etcd) on the clusterState filesystem.
const k3sServerDB = "/var/lib/rancher/k3s/server/db"

// purgeLeaderLeftovers removes a demoted machine's old control-plane
// datastore. A machine that served as a leader and was demoted keeps
// its etcd data on clusterState. etcd refuses to let a
// permanently-removed member rejoin with its old data directory, so
// a later re-promotion would fail against the leftover data. A
// follower has no reason to keep a datastore, and deleting it is
// what makes demotion truly reversible.
//
// The proven-source guard is what makes this safe to automate. A
// staged document that demotes this machine is still on trial. If it
// fails to prove (for example, an edit that wrongly demotes the only
// leader), the fallback boots the leader role again and needs its
// datastore exactly where it was. The cleanup happens only after a
// demotion has already proved out: the machine joined its cluster as
// a follower, and the operator promoted the document. The cleanup
// runs on the boot after that.
func purgeLeaderLeftovers(role api.Role, clusterManifestSource machine.ManifestSource, dbDir string) {
	if role != api.RoleFollower || clusterManifestSource != machine.ManifestSourceProven {
		return
	}
	if _, err := os.Stat(dbDir); err != nil {
		return
	}
	if err := os.RemoveAll(dbDir); err != nil {
		fmt.Fprintf(os.Stderr, "liken: purging the old control-plane datastore: %v\n", err)
		return
	}
	fmt.Println("liken: this follower once served as a leader; its old control-plane datastore is purged so a future promotion starts clean")
}

// persistNodePassword gives k3s's node password durable storage. On
// its first join, a machine mints a random secret (its "node
// password"). The leader records it, and every reconnect after that
// must present the same secret. This is what stops a stranger from
// registering as an existing node and receiving its kubelet
// certificates. k3s keeps this secret at
// /etc/rancher/node/password, which on liken is the RAM root: the
// password would vanish on every reboot, and the machine would
// present the wrong secret when it tried to rejoin its own cluster.
// The password is machine identity, and the machine's durable
// identity data lives on machineState, so /etc/rancher/node becomes
// a symlink onto that filesystem. A machine whose machineState is
// memory-backed keeps the tmpfs default, since nothing about it
// survives reboots anyway.
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
	if err := mintNodePassword(dir); err != nil {
		fmt.Fprintf(os.Stderr, "liken: node identity: %v\n", err)
		return
	}
	fmt.Printf("liken: node identity: /etc/rancher/node persists on machineState\n")
}

// mintNodePassword writes the node password that k3s would otherwise
// mint for itself on first join. Letting k3s write it risks locking
// a machine out of its own cluster after a single power cut. k3s's
// write is a plain create, and a cut between the create and the data
// reaching disk leaves a 0-byte file. Every boot after that presents
// an empty password, and the cluster refuses it ("node password not
// set"), forever. k3s honors a password file that already exists, so
// init mints the password first, with the same atomic, fsynced write
// that the staging files get, in the same format that k3s generates
// (32 hex characters of real randomness).
//
// A password that already exists here is kept, because the cluster
// recorded it at registration and would refuse a replacement. A
// 0-byte file is the torn write described above, not a credential,
// so it counts as absent. This recovers a machine whose cut happened
// before registration; a cut after registration was unrecoverable
// either way.
func mintNodePassword(dir string) error {
	path := filepath.Join(dir, "password")
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		return nil
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	return machine.WriteDurable(path, []byte(hex.EncodeToString(secret)+"\n"))
}

// writeK3sBootConfig derives this machine's role and k3s
// configuration and writes the drop-in beside the role's static
// config file. It returns the role so the supervisor knows which k3s
// to start.
func writeK3sBootConfig(clusterDoc *cluster.Cluster, m *machine.Machine, conns []*connection) (api.Role, error) {
	name := m.Metadata.Name
	// Role is safe to call with a nil cluster document by design: a
	// machine with no cluster document is a leader of one. This rule
	// is what keeps the follower branches below, which dereference
	// cluster freely, off the nil path. A follower can only be
	// derived from a document.
	role := clusterDoc.Role(name)
	if clusterDoc != nil {
		fmt.Printf("liken: this machine is a cluster %s (cluster %s)\n", role, clusterDoc.Metadata.Name)
	}
	if role == api.RoleFollower && clusterDoc.Spec.Endpoint == "" {
		return role, fmt.Errorf("this machine is a follower, but the cluster manifest declares no endpoint to join")
	}

	haveToken := true
	if _, err := os.Stat(tokenPath); err != nil {
		haveToken = false
		if role == api.RoleFollower {
			return role, fmt.Errorf("this machine is a follower, but the image carries no join token at %s", tokenPath)
		}
	}

	nodeIP, nodeInterface := nodeAddress(clusterDoc, conns)
	if nodeIP != "" {
		fmt.Printf("liken: node IP is %s on %s\n", nodeIP, nodeInterface)
	} else if role == api.RoleFollower {
		fmt.Fprintf(os.Stderr, "liken: no address falls inside the cluster's nodeCIDR; k3s will guess a node IP\n")
	}

	clusterInit, joinURL := leaderJoinConfig(clusterDoc, name, machine.MachineManifestDir)
	if clusterInit {
		fmt.Println("liken: this machine is the founding leader; embedded etcd runs here")
	} else if joinURL != "" {
		fmt.Printf("liken: joining the control plane at %s\n", joinURL)
	}

	base := k3sServerConfig
	if role == api.RoleFollower {
		base = k3sAgentConfig
	}
	dropInDir := base + ".d"
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		return role, err
	}
	content := k3sBootConfig(k3sBootInputs{
		role:          role,
		clusterDoc:    clusterDoc,
		nodeIP:        nodeIP,
		nodeInterface: nodeInterface,
		haveToken:     haveToken,
		clusterInit:   clusterInit,
		joinURL:       joinURL,
		nodeLabels:    m.Spec.NodeLabels,
	})
	if err := os.WriteFile(filepath.Join(dropInDir, "boot.yaml"), []byte(content), 0o644); err != nil {
		return role, err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(content), "\n") {
		if !strings.HasPrefix(line, "#") {
			fmt.Printf("liken: k3s config: %s\n", line)
		}
	}

	// The Go runtime discipline is set alongside the configuration.
	// It is derived from the same cluster document (the helm feature
	// is what enlarges the k3s heap), and re-deriving it here means
	// an applied restart re-scales the ceiling on the same restart
	// that reconfigures k3s. This code echoes it like the config
	// lines, because an invisible environment variable is where a
	// memory problem is hard to find.
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err == nil {
		k3sMemoryDiscipline = k3sRuntimeEnv(clusterDoc.RuntimeSpec(), uint64(si.Totalram)*uint64(si.Unit), clusterDoc.FeatureEnabled("helm"))
		fmt.Printf("liken: k3s env: %s\n", strings.Join(k3sMemoryDiscipline, " "))
	}
	return role, nil
}

// clusterStateStaging is the private mountpoint where clusterState's
// filesystem sits while its seed files are layered in, before the
// mount moves to its real path. It is a shared constant because two
// files must agree on it exactly: mountAndSeedClusterState stages the
// mount here, and teardownStorage (storage.go) unmounts this same
// path when a failed reconcile leaves it behind.
const clusterStateStaging = "/.liken-claim"

// mountAndSeedClusterState mounts clusterState's filesystem with the
// image's seed files layered in. The image bakes the seeds (the
// pre-generated CAs, the operator's manifests, and the container
// image) underneath clusterState's own mountpoint, and mounting over
// them would hide all of them. So this function first mounts the
// filesystem to the side, seeds it from the image's copies, and only
// then moves it into place. MS_MOVE re-attaches a live mount
// atomically. This function lives here rather than with the
// partition machinery, because everything it knows, the seed paths,
// what refreshes, and what persists, belongs to k3s's on-disk
// layout.
func mountAndSeedClusterState(dev, target string) error {
	staging := clusterStateStaging
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", staging, err)
	}
	if err := unix.Mount(dev, staging, "ext4", 0, ""); err != nil {
		return fmt.Errorf("mounting %s for clusterState: %w", dev, err)
	}
	if err := seedClusterState(staging); err != nil {
		return fmt.Errorf("seeding clusterState: %w", err)
	}
	sweepTornK3sFiles(staging)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", target, err)
	}
	if err := unix.Mount(staging, target, "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("moving clusterState into place at %s: %w", target, err)
	}
	_ = os.Remove(staging)
	return nil
}

// sweepTornK3sFiles removes the 0-byte identity files that a power
// cut can leave under k3s's state. ext4 can commit a file's creation
// before its data reaches disk, so a machine that loses power just
// after k3s writes a certificate, key, or credential can reboot to a
// file that exists with no bytes in it. k3s treats these files as
// create-once: it reads and trusts an empty one rather than
// regenerating it, and the result is a control plane that cannot
// start (an apiserver that reads a 0-byte loopback certificate
// reports "failed to find any PEM data" and exits, on every restart,
// forever). None of these files matter on their own: every
// certificate, key, and credential here is re-minted on demand from
// the CAs and token that the image carries, so deleting a torn one
// turns an unbootable machine into an ordinary boot. This sweep is
// deliberately scoped to k3s's identity directories. 0-byte files
// elsewhere (lock files, disabled charts) are not init's concern.
// The node password follows the same pattern with its own handling
// (mintNodePassword), because it lives on machineState, and k3s
// would not re-mint a password that is already registered.
func sweepTornK3sFiles(root string) {
	remove := func(path string, info os.FileInfo) {
		if info.IsDir() || info.Size() > 0 {
			return
		}
		if err := os.Remove(path); err == nil {
			fmt.Printf("liken: swept torn k3s file %s (0 bytes; it will be re-minted)\n", path)
		}
	}
	// The server's tls and cred trees hold nothing but identity
	// material, so every 0-byte file there is torn.
	for _, dir := range []string{"k3s/server/tls", "k3s/server/cred"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err == nil {
				remove(path, info)
			}
			return nil
		})
	}
	// The agent keeps its certificates, keys, and kubeconfigs at the
	// top of its directory, next to subtrees (containerd's store)
	// that are not identity and not walked.
	for _, pattern := range []string{"*.crt", "*.key", "*.kubeconfig"} {
		matches, _ := filepath.Glob(filepath.Join(root, "k3s/agent", pattern))
		for _, path := range matches {
			if info, err := os.Stat(path); err == nil {
				remove(path, info)
			}
		}
	}
}

// seedClusterState copies the image's seed files into a clusterState
// filesystem. This code copies the TLS material only if the disk has
// none, because those keys are the cluster's identity, and a disk
// that already has an identity keeps it. The manifests and the
// operator image refresh on every boot, because they are pinned to
// the liken version of the running image, and an upgraded image must
// deliver its upgraded operator.
func seedClusterState(root string) error {
	for _, seed := range []struct {
		rel     string
		refresh bool
	}{
		{"k3s/server/tls", false},
		{"k3s/server/manifests", true},
		{"k3s/agent/images", true},
	} {
		src := filepath.Join(seedSourceDir, seed.rel)
		if _, err := os.Stat(src); err != nil {
			continue // an image without k3s has no seed files
		}
		dst := filepath.Join(root, seed.rel)
		if seed.refresh {
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		} else if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
			return err
		}
	}
	return nil
}
