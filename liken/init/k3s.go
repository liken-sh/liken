package main

// How init decides this machine's k3s role, writes k3s's configuration,
// and prepares the clusterState filesystem that k3s keeps its state on.
//
// A machine is a leader when its name is in the Cluster's spec.leaders,
// and a follower otherwise. k3s calls these roles "server" and "agent",
// and those words appear here only where k3s's own files and flags use
// them.
//
// The supervisor starts k3s with no arguments, so k3s reads all of its
// configuration from files. Besides its config file, k3s reads every
// *.yaml file in the directory <name>.yaml.d/ beside it. The image
// ships the static configuration: /etc/rancher/k3s/config.yaml for
// leaders and agent.yaml for followers. At each boot, init writes the
// settings that come from the Cluster and the Machine, such as the node
// IP and the cluster's CIDRs, to boot.yaml in the .d directory. Init
// never changes the static files.

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

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// Package variables, so that tests can point the code at files in a
// temporary directory.
var (
	// The static configuration that the image ships.
	k3sServerConfig = "/etc/rancher/k3s/config.yaml"
	k3sAgentConfig  = "/etc/rancher/k3s/agent.yaml"

	// k3sKubeletConfig is the KubeletConfiguration file that init
	// writes when the cluster sets a kubelet option that has no flag.
	// It is outside the .d directories, because k3s would parse a file
	// there as k3s configuration.
	k3sKubeletConfig = "/etc/rancher/k3s/kubelet.yaml"

	// k3sKubeletConfigCopy is where k3s copies k3sKubeletConfig when it
	// starts with kubelet-arg config=. The kubelet reads every file in
	// that directory, and k3s updates the copy only while the argument
	// is set. The directory is on clusterState and survives reboots, so
	// when the cluster stops setting the options, init deletes the copy.
	// Otherwise the kubelet would keep the old settings at every start.
	k3sKubeletConfigCopy = "/var/lib/rancher/k3s/agent/etc/kubelet.conf.d/10-cli-config.conf"

	// k3sContainerdDropIn is the file that sets containerd's log level.
	//
	// k3s renders containerd's configuration from a template each time it
	// starts containerd, so init cannot edit that file. The rendered
	// file imports config-v3.toml.d/*.toml from its directory, and
	// containerd merges each imported file over it. So a file in that
	// directory sets the level without depending on k3s's template.
	//
	// liken does not replace the template with config-v3.toml.tmpl. A
	// replacement that fails to parse or leaves out a key that
	// containerd needs stops containerd, and a liken machine has no
	// shell to repair it. The person who runs the cluster can still
	// replace the template. If a later k3s stops importing the
	// directory, containerd still starts, at its default level.
	//
	// "v3" is the configuration version that k3s renders for containerd
	// 2.x. containerd reads the imported files in glob order, and the
	// prefix 10- sets this file's place in that order.
	//
	// The directory is on clusterState and survives reboots, so init
	// deletes the file when the cluster stops setting a level.
	k3sContainerdDropIn = "/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/10-liken-log-level.toml"

	// tokenPath is the cluster's join token in the image. The identity
	// package mints it offline with the CAs, and the token contains a
	// hash of the CA. Init passes k3s the path as token-file, so the
	// secret is not in a config file or on a command line.
	tokenPath = "/etc/liken/token"

	// seedSourceDir is the directory in the image that seedClusterState
	// copies onto the clusterState filesystem.
	seedSourceDir = "/var/lib/rancher"
)

// k3s's auto-deploy directory and liken's subdirectory of it, relative
// to /var/lib/rancher in the image or to the root of the clusterState
// filesystem. features.go explains the subdirectory.
const (
	k3sManifestsRel   = "k3s/server/manifests"
	likenManifestsRel = k3sManifestsRel + "/liken"
)

// leaderJoinConfig returns whether a leader sets cluster-init, which
// creates the etcd cluster, and the URL of the leader it joins, if any.
//
// With one leader, the cluster uses sqlite through kine and the leader
// joins nothing. With more than one, the cluster uses embedded etcd.
// The first name in spec.leaders is the founding leader, and it sets
// cluster-init. On the boot that adds a second leader, k3s converts the
// founder's sqlite datastore to etcd in place, as k3s documents. Every
// other leader joins the founder at the founder's declared address on
// the node network, or at spec.endpoint when the founder declares none.
// A leader renders the same settings on every boot, as k3s recommends
// for a rejoin. The founder matters only here: once etcd runs, the
// founder is an ordinary member, and etcd elects its own raft leader.
//
// An adopted cluster (spec.origin: Adopted) already has a datastore
// that liken did not create, and cluster-init would start a second,
// separate cluster. So no leader sets cluster-init, and even a single
// leader joins. The founder joins spec.endpoint, the address of the
// existing control plane, and the other leaders join the founder. etcd
// replicates the existing data to each new member.
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

// nodeAddress returns the first of the machine's addresses inside the
// Cluster's spec.network.nodeCIDR, and its interface. Other nodes reach
// this machine at that address. It returns empty strings when the
// cluster declares no nodeCIDR, when the nodeCIDR does not parse, and
// when no address matches. Without a node IP, k3s uses the interface
// with the default route, which is the wrong one on a machine whose
// default route is an internet uplink.
func nodeAddress(clusterDoc *cluster.Cluster, conns []*connection) (ip, ifname string) {
	if !declaresNodeCIDR(clusterDoc) {
		return "", ""
	}
	_, subnet, err := net.ParseCIDR(clusterDoc.Spec.Network.NodeCIDR)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: the nodeCIDR %q of the cluster is not valid, so no node IP is derived: %v\n", clusterDoc.Spec.Network.NodeCIDR, err)
		return "", ""
	}
	for _, conn := range conns {
		if conn.addr != nil && subnet.Contains(conn.addr.IP) {
			return conn.addr.IP.String(), conn.ifname
		}
	}
	return "", ""
}

// declaresNodeCIDR reports whether the cluster sets a nodeCIDR. A caller
// that must wait for a matching address calls it to tell "no nodeCIDR"
// apart from "no address matches yet", because nodeAddress returns
// empty strings for both.
func declaresNodeCIDR(clusterDoc *cluster.Cluster) bool {
	return clusterDoc != nil && clusterDoc.Spec.Network.NodeCIDR != ""
}

// k3sBootInputs is what writeK3sBootConfig passes to k3sBootConfig.
type k3sBootInputs struct {
	role          api.Role
	clusterDoc    *cluster.Cluster
	nodeIP        string
	nodeInterface string
	haveToken     bool
	clusterInit   bool
	joinURL       string
	nodeLabels    map[string]string
	nodeTaints    []machine.NodeTaint
	kubeletConfig string
	debug         bool

	// exposeEtcdMetrics is the resolved spec.runtime.etcd.exposeMetrics
	// of the cluster, which is true when the field is unset.
	exposeEtcdMetrics bool
}

// k3sBootConfig renders boot.yaml, where each key is the name of a k3s
// flag.
func k3sBootConfig(in k3sBootInputs) string {
	role, clusterDoc := in.role, in.clusterDoc
	var b strings.Builder
	b.WriteString("# Written by liken at boot: the configuration only the boot can\n")
	b.WriteString("# derive, joined with the static file this directory sits beside.\n")

	// A leader requires the token from every machine that joins, and a
	// follower presents it. A follower also checks the CA hash in the
	// token, so it joins only the cluster that minted the token.
	if in.haveToken {
		fmt.Fprintf(&b, "token-file: %s\n", tokenPath)
	}

	if role == api.RoleFollower {
		fmt.Fprintf(&b, "server: %s\n", clusterDoc.Spec.Endpoint)
	} else {
		// k3s's bundled components (Traefik, the service load balancer,
		// metrics-server) stay off unless spec.features enables them,
		// so that everything beyond the control plane is a workload
		// that the cluster declares. DisabledComponents returns every
		// bundled component that the cluster does not enable, and every
		// one of them when there is no cluster document. It also
		// returns each manifest of k3s that needs a controller that the
		// keys below turn off, because k3s's deploy controller applies
		// every manifest again on every pass while one of them fails.
		// Only leaders render the key, because an agent refuses it. The
		// static file sets no disable key, so this list is the whole
		// list.
		if disabled := clusterDoc.DisabledComponents(); len(disabled) > 0 {
			b.WriteString("disable:\n")
			for _, name := range disabled {
				fmt.Fprintf(&b, "  - %s\n", name)
			}
		}
		// The Helm controller, the cloud controller manager, and the
		// network policy controller run inside the k3s server process,
		// so each has its own disable key in place of an entry in the
		// disable list. Each one uses memory even when nothing uses it,
		// so each stays off unless the cluster enables a feature that
		// needs it.
		//
		// The Helm controller watches HelmChart resources. FeatureEnabled
		// also reports a feature that an enabled feature requires, and
		// traefik requires helm, because k3s deploys Traefik through a
		// HelmChart.
		if !clusterDoc.FeatureEnabled("helm") {
			b.WriteString("disable-helm-controller: true\n")
		}
		// On bare metal, the cloud controller manager does nothing but
		// run the service load balancer (klipper-lb), so it runs only
		// for servicelb. Without it, the kubelet sets the node's
		// addresses, as on any machine outside a cloud.
		if !clusterDoc.FeatureEnabled("servicelb") {
			b.WriteString("disable-cloud-controller: true\n")
		}
		// The network policy controller enforces NetworkPolicy objects,
		// which flannel alone does not. Without it, the API server still
		// accepts NetworkPolicy objects and nothing enforces them, as
		// with plain flannel.
		if !clusterDoc.FeatureEnabled("network-policy") {
			b.WriteString("disable-network-policy: true\n")
		}
		// On a cluster with more than one leader, k3s runs an etcd
		// member in the server process of each leader. etcd serves its
		// Prometheus metrics on port 2381, and k3s binds that port to
		// the loopback address only. With this key, k3s also binds it
		// to the node IP, so a Prometheus that runs in a pod can scrape
		// each leader's etcd with no proxy and no pod on the host
		// network. cluster/runtime.go explains why the key is on unless
		// the cluster turns it off.
		//
		// Every leader renders the key, because init cannot tell from
		// the Cluster whether this leader runs etcd: a cluster that
		// went from several leaders to one keeps its etcd datastore. A
		// leader that uses sqlite starts no etcd, and k3s reads the key
		// nowhere else, so it does nothing there. Only leaders render
		// it, because an agent refuses it.
		if in.exposeEtcdMetrics {
			b.WriteString("etcd-expose-metrics: true\n")
		}
		if clusterDoc != nil {
			// A single leader renders neither key and stays on sqlite.
			if in.clusterInit {
				b.WriteString("cluster-init: true\n")
			} else if in.joinURL != "" {
				fmt.Fprintf(&b, "server: %s\n", in.joinURL)
			}
			// embedded-registry turns on the Spegel registry mirror and
			// is a leader setting. Every node uses the mirror through
			// the entries that registries.go writes to registries.yaml.
			if clusterDoc.Spec.Registries.Embedded {
				b.WriteString("embedded-registry: true\n")
			}
			// Followers read the cluster's CIDRs and DNS settings from
			// the control plane that they join.
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

	// node-ip is the address that the kubelet advertises, and
	// flannel-iface is the interface that carries pod-to-pod traffic.
	// Both must be on the node network.
	if in.nodeIP != "" {
		fmt.Fprintf(&b, "node-ip: %s\n", in.nodeIP)
		fmt.Fprintf(&b, "flannel-iface: %s\n", in.nodeInterface)
	}

	// nodeport-addresses sets the addresses that NodePort services
	// listen on. Both roles render it, because kube-proxy runs on every
	// node. k3s passes each kube-proxy-arg entry to kube-proxy as a
	// flag. The static files set no kube-proxy-arg, so this list is the
	// whole list. The same holds for kubelet-arg below.
	fmt.Fprintf(&b, "kube-proxy-arg:\n  - nodeport-addresses=%s\n",
		strings.Join(clusterDoc.NodePortAddresses(), ","))

	// Both roles render kubelet-arg, because every node runs a kubelet.
	if in.kubeletConfig != "" {
		fmt.Fprintf(&b, "kubelet-arg:\n  - config=%s\n", in.kubeletConfig)
	}

	// debug raises the log level of every Kubernetes component in the
	// k3s process. Both roles render it, because a follower also runs a
	// kubelet and a kube-proxy. When spec.runtime.k3s.debug is false,
	// the drop-in has no debug key.
	if in.debug {
		b.WriteString("debug: true\n")
	}

	// The Machine's spec.nodeLabels are set when the node registers, so
	// a reinstalled machine is never an unlabeled node that the
	// scheduler places the wrong pods on. The + suffix appends to the
	// static file's list, which has liken.sh/machine=true. A plain
	// node-label key would replace that list. The labels are sorted, so
	// the same spec renders the same bytes.
	if len(in.nodeLabels) > 0 {
		b.WriteString("node-label+:\n")
		for _, name := range slices.Sorted(maps.Keys(in.nodeLabels)) {
			fmt.Fprintf(&b, "  - %s=%s\n", name, in.nodeLabels[name])
		}
	}

	// The Machine's spec.nodeTaints are set when the node registers, so
	// no pod that a taint keeps out is scheduled before the operator
	// adds it. k3s passes node-taint to the kubelet's
	// registerWithTaints, which the kubelet reads only when it creates
	// the Node. So the key takes effect on a first boot or after a
	// reinstall, and on later boots the operator updates the Node's
	// taints. An entry is key=value:Effect, or key:Effect with no value.
	// The taints are sorted, so the same spec renders the same bytes.
	if len(in.nodeTaints) > 0 {
		taints := slices.Clone(in.nodeTaints)
		slices.SortFunc(taints, func(left, right machine.NodeTaint) int {
			if byKey := strings.Compare(left.Key, right.Key); byKey != 0 {
				return byKey
			}
			return strings.Compare(string(left.Effect), string(right.Effect))
		})
		b.WriteString("node-taint+:\n")
		for _, taint := range taints {
			entry := taint.Key
			if taint.Value != "" {
				entry += "=" + taint.Value
			}
			fmt.Fprintf(&b, "  - %s:%s\n", entry, taint.Effect)
		}
	}
	return b.String()
}

// kubeletImageGCSettings returns a KubeletConfiguration line for each
// image garbage collection setting that the cluster sets. The values
// need no conversion, because cluster/runtime.go refuses any value that
// the kubelet would refuse.
func kubeletImageGCSettings(g cluster.ImageGCSpec) []string {
	var settings []string
	if g.HighThresholdPercent != nil {
		settings = append(settings, fmt.Sprintf("imageGCHighThresholdPercent: %d", *g.HighThresholdPercent))
	}
	if g.LowThresholdPercent != nil {
		settings = append(settings, fmt.Sprintf("imageGCLowThresholdPercent: %d", *g.LowThresholdPercent))
	}
	if g.MinimumAge != "" {
		settings = append(settings, fmt.Sprintf("imageMinimumGCAge: %s", g.MinimumAge))
	}
	if g.MaximumAge != "" {
		settings = append(settings, fmt.Sprintf("imageMaximumGCAge: %s", g.MaximumAge))
	}
	return settings
}

// kubeletBootConfig wraps the settings in a KubeletConfiguration
// document, or returns "" when there are none.
//
// imageMaximumGCAge has no kubelet flag, so the settings go in a file,
// and all of them go there so that the policy is in one place. A
// kubelet flag would override the file, but k3s passes no image garbage
// collection flag.
func kubeletBootConfig(settings []string) string {
	if len(settings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Written by liken at boot: the kubelet settings that the\n")
	b.WriteString("# cluster's spec.runtime.kubelet section names.\n")
	b.WriteString("apiVersion: kubelet.config.k8s.io/v1beta1\n")
	b.WriteString("kind: KubeletConfiguration\n")
	for _, setting := range settings {
		b.WriteString(setting + "\n")
	}
	return b.String()
}

// containerdLogLevelDropIn renders the file at k3sContainerdDropIn, or
// returns "" when the level is empty.
//
// The file has only a [debug] table, which k3s's template does not
// have. It sets version = 3, the version of the file that k3s renders,
// because containerd refuses an imported file with a higher version
// than the file that imports it. containerd migrates both files to its
// current version at each start, and logs "Configuration migrated" for
// each.
//
// k3s builds containerd's command line and passes no log level, so
// nothing overrides this file unless k3s's environment sets
// CONTAINERD_LOG_LEVEL, which k3s turns into --log-level. Init does not
// set it.
func containerdLogLevelDropIn(level string) string {
	if level == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Written by liken at boot: the containerd log level that the\n")
	b.WriteString("# cluster's spec.runtime.containerd section names.\n")
	b.WriteString("version = 3\n")
	b.WriteString("\n[debug]\n")
	fmt.Fprintf(&b, "  level = %q\n", level)
	return b.String()
}

// k3sServerDB is the directory of the control plane's datastore (sqlite
// through kine, or embedded etcd) on the clusterState filesystem.
const k3sServerDB = "/var/lib/rancher/k3s/server/db"

// purgeLeaderLeftovers deletes the control-plane datastore of a machine
// that was demoted from leader to follower. etcd refuses a removed
// member that rejoins with its old data directory, so a later promotion
// would fail if the data stayed.
//
// It deletes only under a proven cluster manifest. A staged manifest
// that demotes this machine can still fail, for example when it demotes
// the only leader, and then the fallback boots the machine as a leader
// again with its datastore. A manifest is proven only after the machine
// joined as a follower and the operator promoted the manifest.
func purgeLeaderLeftovers(role api.Role, clusterManifestSource machine.ManifestSource, dbDir string) {
	if role != api.RoleFollower || clusterManifestSource != machine.ManifestSourceProven {
		return
	}
	if _, err := os.Stat(dbDir); err != nil {
		return
	}
	if err := os.RemoveAll(dbDir); err != nil {
		fmt.Fprintf(os.Stderr, "liken: deleting the control-plane datastore of this follower: %v\n", err)
		return
	}
	fmt.Println("liken: deleted the control-plane datastore that this follower kept from when it was a leader, so a later promotion starts with an empty datastore")
}

// persistNodePassword keeps k3s's node password across reboots. On its
// first join a node generates a random password, and the control plane
// records it. Every later join must present the same password, so that
// another machine cannot register under this node's name and receive
// its kubelet certificates. k3s keeps the password in
// /etc/rancher/node, which is in the RAM root, so init makes that
// directory a symlink to a directory on machineState. When machineState
// is in memory, nothing survives a reboot, and init leaves the default.
func persistNodePassword(storage machine.StorageStatus) error {
	if storage.MachineState.Backing != machine.BackingPartition {
		return nil
	}
	return linkNodePassword(filepath.Join(machine.MachineStateDir, "node"), "/etc/rancher/node")
}

// linkNodePassword makes link a symlink to dir and mints the password
// in dir. A link that already names dir is kept, and the mint still
// runs, because without the mint k3s writes the password itself. When
// the link cannot be made, it returns an error, and the boot fails:
// k3s would keep its password in the RAM root, and on a first join the
// next boot would present a password that the control plane never
// recorded.
func linkNodePassword(dir, link string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("keeping the node password on machineState: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return fmt.Errorf("keeping the node password on machineState: %w", err)
	}
	if err := os.Symlink(dir, link); err != nil {
		if target, rerr := os.Readlink(link); rerr != nil || target != dir {
			return fmt.Errorf("keeping the node password on machineState: %w", err)
		}
	}
	if err := mintNodePassword(dir); err != nil {
		fmt.Fprintf(os.Stderr, "liken: minting the node password: %v\n", err)
		return nil
	}
	fmt.Printf("liken: %s is a link to %s on machineState, so the node password survives reboots\n", link, dir)
	return nil
}

// mintNodePassword writes the node password before k3s does. k3s
// writes the file without a sync, so a power cut can leave it empty,
// and every later join then fails with "node password not set". k3s
// uses a password file that already exists, so init writes one first
// with machine.WriteDurable, in k3s's format: 16 random bytes as 32
// hex characters.
//
// A non-empty password is kept, because the control plane refuses any
// other. An empty file is replaced, which repairs a machine that lost
// power before it registered.
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

// unreachableNodePortsWarning returns a warning when
// spec.network.nodePortCIDRs is set and does not include the node IP,
// and "" otherwise. The list replaces the default of the node IP, so a
// cluster that lists only a tunnel network gets no NodePorts on the node
// network, and nothing else reports it. The boot continues, because the
// list can be deliberate.
func unreachableNodePortsWarning(clusterDoc *cluster.Cluster, nodeIP string) string {
	if clusterDoc == nil || len(clusterDoc.Spec.Network.NodePortCIDRs) == 0 || nodeIP == "" {
		return ""
	}
	address := net.ParseIP(nodeIP)
	for _, entry := range clusterDoc.Spec.Network.NodePortCIDRs {
		_, subnet, err := net.ParseCIDR(entry)
		if err == nil && subnet.Contains(address) {
			return ""
		}
	}
	return fmt.Sprintf("liken: nodePortCIDRs (%s) does not include this machine's node IP %s, so NodePort services do not respond on the node network",
		strings.Join(clusterDoc.Spec.Network.NodePortCIDRs, ","), nodeIP)
}

// writeK3sBootConfig writes boot.yaml for this machine's role, and
// writes or deletes the kubelet and containerd files. It returns the
// role, which tells the supervisor whether to start a k3s server or an
// agent.
func writeK3sBootConfig(clusterDoc *cluster.Cluster, m *machine.Machine, conns []*connection) (api.Role, error) {
	name := m.Metadata.Name
	// Role returns leader for a nil cluster, so the follower branches
	// below can read clusterDoc without a nil check.
	role := clusterDoc.Role(name)
	if clusterDoc != nil {
		fmt.Printf("liken: this machine is a %s of cluster %s\n", role, clusterDoc.Metadata.Name)
	}
	if role == api.RoleFollower && clusterDoc.Spec.Endpoint == "" {
		return role, fmt.Errorf("the cluster manifest sets no spec.endpoint, which a follower needs to join the control plane")
	}

	haveToken := true
	if _, err := os.Stat(tokenPath); err != nil {
		haveToken = false
		if role == api.RoleFollower {
			return role, fmt.Errorf("the image has no join token at %s, which a follower needs to join the control plane", tokenPath)
		}
	}

	nodeIP, nodeInterface := nodeAddress(clusterDoc, conns)
	if nodeIP != "" {
		fmt.Printf("liken: node IP is %s on %s\n", nodeIP, nodeInterface)
	} else if role == api.RoleFollower {
		fmt.Fprintf(os.Stderr, "liken: no address of this machine is inside the cluster's nodeCIDR, so k3s chooses the node IP\n")
	}

	if warning := unreachableNodePortsWarning(clusterDoc, nodeIP); warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}

	clusterInit, joinURL := leaderJoinConfig(clusterDoc, name, machine.MachineManifestDir)
	if clusterInit {
		fmt.Println("liken: this machine is the founding leader and starts the embedded etcd cluster")
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

	// The kubelet does not start when kubelet-arg names a missing file,
	// so boot.yaml names the file only after it is written.
	kubeletConfig := ""
	settings := kubeletImageGCSettings(clusterDoc.KubeletSpec().ImageGC)
	if document := kubeletBootConfig(settings); document != "" {
		if err := os.WriteFile(k3sKubeletConfig, []byte(document), 0o644); err != nil {
			return role, err
		}
		kubeletConfig = k3sKubeletConfig
	} else {
		// The restart path runs this function without a reboot, so the
		// RAM root can still have the file from an earlier run. The
		// kubelet would not read it, but it would mislead a person.
		// k3s's copy is still read, as k3sKubeletConfigCopy explains.
		_ = os.Remove(k3sKubeletConfig)
		_ = os.Remove(k3sKubeletConfigCopy)
	}

	// On a first boot, k3s has not created the directory yet.
	containerdLevel := clusterDoc.ContainerdSpec().LogLevel
	if document := containerdLogLevelDropIn(containerdLevel); document != "" {
		if err := os.MkdirAll(filepath.Dir(k3sContainerdDropIn), 0o755); err != nil {
			return role, err
		}
		if err := os.WriteFile(k3sContainerdDropIn, []byte(document), 0o644); err != nil {
			return role, err
		}
	} else {
		// Only init writes this file name, so the delete leaves the
		// other files in the directory alone.
		_ = os.Remove(k3sContainerdDropIn)
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
		nodeTaints:    m.Spec.NodeTaints,
		kubeletConfig: kubeletConfig,
		debug:         clusterDoc.RuntimeSpec().Debug,

		exposeEtcdMetrics: clusterDoc.EtcdSpec().MetricsExposed(),
	})
	if err := os.WriteFile(filepath.Join(dropInDir, "boot.yaml"), []byte(content), 0o644); err != nil {
		return role, err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(content), "\n") {
		if !strings.HasPrefix(line, "#") {
			fmt.Printf("liken: k3s config: %s\n", line)
		}
	}

	// The facts tree reports the same values in status.runtime, for a
	// person who cannot read the console.
	for _, setting := range settings {
		fmt.Printf("liken: kubelet config: %s\n", setting)
	}

	if containerdLevel != "" {
		fmt.Printf("liken: containerd config: log level %s\n", containerdLevel)
	}

	// k3sRuntimeEnv derives k3s's environment from spec.runtime.k3s and
	// the machine's memory, here so that a restart derives it again with
	// boot.yaml. It is printed, because an environment variable that
	// nobody can see makes a memory problem hard to find.
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err == nil {
		k3sMemoryDiscipline = k3sRuntimeEnv(clusterDoc.RuntimeSpec(), uint64(si.Totalram)*uint64(si.Unit))
		fmt.Printf("liken: k3s env: %s\n", strings.Join(k3sMemoryDiscipline, " "))
	}
	return role, nil
}

// clusterStateStaging is where mountAndSeedClusterState mounts the
// clusterState filesystem before it moves the mount to target. When a
// failed attempt leaves a mount here, teardownStorage unmounts it.
const clusterStateStaging = "/.liken-claim"

// mountAndSeedClusterState mounts the clusterState filesystem at target
// with the image's seed files copied in. The seed files are the CAs,
// liken's manifests, and the agent images, and the image has them under
// target, where the mount would hide them. So the filesystem is mounted
// at a staging path first, the files are copied in, and MS_MOVE moves
// the mount to target in one step. This function is here and not with
// the partition code, because the seed paths follow k3s's layout on
// disk.
func mountAndSeedClusterState(dev, target string) error {
	staging := clusterStateStaging
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("creating the staging mount point: %w", err)
	}
	if err := unix.Mount(dev, staging, "ext4", 0, ""); err != nil {
		return fmt.Errorf("mounting %s for clusterState: %w", dev, err)
	}
	if err := seedClusterState(staging); err != nil {
		return fmt.Errorf("copying the seed files into clusterState: %w", err)
	}
	sweepTornK3sFiles(staging)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("creating the mount point of clusterState: %w", err)
	}
	if err := unix.Mount(staging, target, "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("moving clusterState into place at %s: %w", target, err)
	}
	_ = os.Remove(staging)
	return nil
}

// sweepTornK3sFiles deletes the empty certificates, keys, and
// credentials that a power cut can leave in k3s's state. ext4 can
// commit a file's creation before its data, so a machine that loses
// power just after k3s writes one can reboot to an empty file. k3s does
// not regenerate a file that exists, so the control plane then fails on
// every boot. For example, the apiserver exits with "failed to find any
// PEM data" when its loopback certificate is empty. k3s regenerates
// each of these files from the CAs and the token, so deleting an empty
// one lets the next start succeed.
//
// Only k3s's identity directories are checked, because an empty file
// elsewhere, such as a lock file, is valid. mintNodePassword handles
// the node password, because k3s cannot regenerate a password that the
// control plane already recorded.
func sweepTornK3sFiles(root string) {
	remove := func(path string, info os.FileInfo) {
		if info.IsDir() || info.Size() > 0 {
			return
		}
		if err := os.Remove(path); err == nil {
			fmt.Printf("liken: deleted the empty k3s file %s, which k3s creates again\n", path)
		}
	}
	// The server's tls and cred directories have only identity files.
	for _, dir := range []string{"k3s/server/tls", "k3s/server/cred"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err == nil {
				remove(path, info)
			}
			return nil
		})
	}
	// The agent keeps its certificates, keys, and kubeconfigs at the top
	// of its directory. Its subdirectories, such as containerd's store,
	// are not checked.
	for _, pattern := range []string{"*.crt", "*.key", "*.kubeconfig"} {
		matches, _ := filepath.Glob(filepath.Join(root, "k3s/agent", pattern))
		for _, path := range matches {
			if info, err := os.Stat(path); err == nil {
				remove(path, info)
			}
		}
	}
}

// seedClusterState copies the image's seed files onto a clusterState
// filesystem. The TLS files are the cluster's identity, so they are
// copied only when the disk has none. The manifests and the agent
// images belong to the running image's liken version, so they are
// replaced at every boot, and an upgraded image starts its upgraded
// operator.
//
// Only liken's own subdirectory of the auto-deploy directory is
// replaced. k3s keeps its bundled components' manifests at the top of
// that directory, and when a boot disables a component, k3s reads its
// file at start to remove it. If the file were deleted first, the
// component would keep running.
//
// A CRD manifest is kept when the disk's copy has a higher
// liken.sh/schema-revision than the image's, because replacing it would
// downgrade a schema that the cluster serves and prune stored fields.
// schemarevision.go explains this.
//
// A failure to copy the TLS files fails the boot. With no CAs on disk,
// k3s generates its own, and then no kubeconfig and no token from this
// image work, while the machine still reports itself healthy. A failure
// to copy the manifests or the images is printed, and the boot
// continues with whatever part of the copy succeeded, because a
// running machine can still be inspected and upgraded and a stopped one
// cannot.
func seedClusterState(root string) error {
	for _, seed := range []struct {
		rel      string
		refresh  bool
		identity bool
	}{
		{"k3s/server/tls", false, true},
		{likenManifestsRel, true, false},
		{"k3s/agent/images", true, false},
	} {
		src := filepath.Join(seedSourceDir, seed.rel)
		if _, err := os.Stat(src); err != nil {
			continue // an image without k3s has no seed files
		}
		dst := filepath.Join(root, seed.rel)
		var err error
		if seed.refresh {
			keep := map[string]bool{}
			if seed.rel == likenManifestsRel {
				keep = newerCRDsOnDisk(src, dst)
			}
			err = refreshSeedDir(dst, src, keep)
		} else if _, absent := os.Stat(dst); absent != nil {
			err = copySeedTree(dst, src)
		}
		if err == nil {
			continue
		}
		if seed.identity {
			return fmt.Errorf("seeding %s: %w", seed.rel, err)
		}
		reportSeedFailure(seed.rel, err)
	}
	removeTopLevelLikenManifests(root)
	return nil
}

func copySeedTree(dst, src string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.CopyFS(dst, os.DirFS(src))
}

func reportSeedFailure(rel string, err error) {
	fmt.Fprintf(os.Stderr, "liken: k3s: seeding %s into clusterState: %v\n", rel, err)
	fmt.Fprintf(os.Stderr, "liken: k3s: the boot continues, and %s in clusterState can be incomplete\n", rel)
}

// likenManifestNames returns the file name of every manifest that
// liken writes to k3s's auto-deploy directory: the seeded manifests,
// the manifests of each feature, and the manifests that features render
// at boot.
func likenManifestNames() []string {
	names := map[string]bool{}
	seeded, _ := os.ReadDir(filepath.Join(seedSourceDir, likenManifestsRel))
	for _, entry := range seeded {
		names[entry.Name()] = true
	}
	staged, _ := filepath.Glob(filepath.Join(featuresDir, "*", "manifests", "*.yaml"))
	for _, manifest := range staged {
		names[filepath.Base(manifest)] = true
	}
	for _, rendered := range renderedFeatureManifests {
		for _, name := range rendered {
			names[name] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// removeTopLevelLikenManifests deletes liken's manifests from the top of
// k3s's auto-deploy directory, where an earlier release wrote them.
// They declare the same objects as the copies in liken's subdirectory,
// so k3s would apply each object from two addons. Only the names that
// likenManifestNames returns are deleted, so k3s's own files stay.
func removeTopLevelLikenManifests(root string) {
	for _, name := range likenManifestNames() {
		path := filepath.Join(root, k3sManifestsRel, name)
		if err := os.Remove(path); err == nil {
			fmt.Printf("liken: deleted %s from the top of k3s's auto-deploy directory, because liken's copy is in %s\n",
				name, likenManifestsRel)
		}
	}
}
