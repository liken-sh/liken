// Package machine is the Machine API: liken's one configuration
// document, as Go types.
//
// A Machine is shaped like a Kubernetes resource on purpose (kubelet's
// KubeletConfiguration, k0s's config, and Talos's machine config all
// use the same shape) because one schema-validated document can be
// delivered two ways. At boot, init reads it from a file baked into
// the image (there is no API server yet). Once the cluster is up, the
// same document exists in it as a custom resource, where the liken
// operator publishes the machine's live facts into its status. The
// file you hand-write and the object `kubectl get machine -o yaml`
// returns are the same document.
//
// Two programs use this API and this package is what they share. Init
// consumes the spec (it applies the network and sysctls at boot) and
// produces facts; the operator consumes the facts and reconciles the
// spec for as long as the machine runs. The division is deliberate:
// init never talks to Kubernetes, the operator never touches boot-time
// state, and the file at /run/liken/facts.yaml is the one-way channel
// between them.
//
// The API group is liken.sh: CRD groups are DNS names, and we own
// that one.
package machine

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

const (
	// APIVersion is the full group/version string a Machine document
	// declares, and the URL segment the operator speaks to the API
	// server: /apis/liken.sh/v1alpha1/machines.
	APIVersion = "liken.sh/v1alpha1"

	// ManifestPath is where the image carries the machine's manifest.
	// Init reads it directly; the operator reads the same file through
	// a hostPath mount, to seed the in-cluster Machine on first boot.
	ManifestPath = "/etc/liken/machine.yaml"

	// FactsPath is where init publishes what it learned about the
	// machine, shaped exactly like MachineStatus. /run is a fresh
	// tmpfs every boot, which is the point: facts describe the current
	// boot only, and never survive into the next one.
	FactsPath = "/run/liken/facts.yaml"

	// SysctlDir is the kernel's tuning interface: one file per
	// parameter. The sysctl helpers take the directory as a parameter
	// so tests can point them at a miniature copy; real callers pass
	// this.
	SysctlDir = "/proc/sys"
)

// Version is the liken release this binary was built from, stamped by
// the build (-ldflags -X) from the VERSION file at the repo root. It
// reaches the cluster as status.version.liken; eventually a version
// field in the spec will be how upgrades are requested.
var Version = "dev"

// The struct tags are json, not yaml, because parsing goes through
// sigs.k8s.io/yaml, the same converter Kubernetes tooling uses. It
// turns YAML into JSON before unmarshalling, which is what gives
// Kubernetes documents their camelCase convention and means these
// structs serialize identically whether they're read from a file or
// from the API server.
type Machine struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       MachineSpec   `json:"spec,omitzero"`
	Status     MachineStatus `json:"status,omitzero"`
}

// ObjectMeta carries the small slice of Kubernetes object metadata
// liken actually uses. Name is the machine's hostname and its node
// name. ResourceVersion only matters on the API-server transport: it's
// the cluster's optimistic-concurrency counter, and the operator hands
// it back when watching so the server knows where to resume.
type ObjectMeta struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// MachineSpec is the declared half: what a person (or, eventually, a
// git repository) asks this machine to be. Each field notes who acts on
// it and when, because the actuators differ: some state can only be
// set while the machine is being built, some can be reconciled live.
type MachineSpec struct {
	// Network is applied by init at boot. It can't be reconciled from
	// inside the cluster, because reaching the cluster depends on it;
	// changes take effect on the next boot.
	Network NetworkSpec `json:"network,omitzero"`

	// Sysctls is kernel tuning: parameter name to desired value, e.g.
	// "vm.overcommit_memory": "1". Applied twice, by design: init sets
	// them at boot so they hold before k3s starts, and the operator
	// reconciles them live afterward, so a kubectl edit takes effect
	// without a reboot.
	Sysctls map[string]string `json:"sysctls,omitempty"`

	// Storage assigns storage roles to disks (see storage.go). Applied
	// by init at boot, before k3s: a filesystem can't be swapped under
	// a running cluster.
	Storage StorageSpec `json:"storage,omitzero"`
}

// NetworkSpec is deliberately almost empty: the default is zero
// configuration: DHCP on the first physical interface, hostname from
// the manifest, DNS from the lease. Fields exist here only for
// machines that need to deviate from that.
type NetworkSpec struct {
	// Interface pins network bring-up to a specific interface name
	// (e.g. "eth1"). Empty means: the first interface that looks like
	// real hardware.
	Interface string `json:"interface,omitempty"`
}

// Load reads a Machine manifest from a file. A machine with no
// manifest is still a valid machine (everything defaults), but a
// manifest that exists and doesn't parse, or declares some other kind,
// is a configuration error and is reported as one.
func Load(path string) (*Machine, error) {
	m := &Machine{}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.UnmarshalStrict(raw, m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if m.Kind != "Machine" {
		return nil, fmt.Errorf("%s: expected kind Machine, got %q", path, m.Kind)
	}
	return m, nil
}
