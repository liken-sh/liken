// Package machine is the Machine API: liken's one configuration
// document, as Go types.
//
// A Machine is deliberately shaped like a Kubernetes resource
// (kubelet's KubeletConfiguration, k0s's config, and Talos's machine
// config all use the same shape) because one schema-validated
// document can be delivered two ways. At boot, init reads it from a file baked into
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
//
// A note on naming: machine.MachineSpec and machine.MachineStatus
// stutter against Go's naming advice, deliberately. The types mirror
// the CRD kind (Machine) and Kubernetes' XxxSpec/XxxStatus
// convention, and matching what `kubectl explain machine.spec` shows
// is worth more here than avoiding the echo.
package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"sigs.k8s.io/yaml"
)

const (
	// APIVersion is the full group/version string a Machine document
	// declares, and the URL segment the operator speaks to the API
	// server: /apis/liken.sh/v1alpha1/machines.
	APIVersion = "liken.sh/v1alpha1"

	// MachineManifestDir is where the image carries Machine manifests,
	// one file per machine (<name>.yaml). One image boots a whole
	// fleet, so the image carries every machine's manifest and each
	// boot selects its own: by the liken.machine=<name> kernel
	// parameter, or, on a machine with exactly one manifest, by its
	// being the only choice.
	MachineManifestDir = "/etc/liken/machines"

	// BootManifestPath is where init publishes the manifest this boot
	// actually ran under (the staged or proven copy from machineState,
	// or the image's seed on a first boot). The operator reads it
	// through a hostPath mount to know which Machine it manages and to
	// seed the in-cluster Machine on first boot. Like the facts file,
	// it lives under /run because it describes the current boot only.
	BootManifestPath = "/run/liken/machine.yaml"

	// BootClusterManifestPath is the same publication for the cluster
	// document: the exact bytes this boot derived its role from. The
	// operator needs the bytes, not just their hash, because drift
	// detection compares documents by meaning. A hand-written seed
	// and the operator's canonical rendering of the same spec are
	// different bytes that say the same thing, and a formatting
	// difference should never reboot the fleet.
	BootClusterManifestPath = "/run/liken/cluster.yaml"

	// FactsPath is where init publishes what it learned about the
	// machine, shaped exactly like MachineStatus. /run is a fresh
	// tmpfs every boot, which suits the facts exactly: they describe
	// the current boot only, and never survive into the next one.
	FactsPath = "/run/liken/facts.yaml"

	// SysctlDir is the kernel's tuning interface: one file per
	// parameter. The sysctl helpers take the directory as a parameter
	// so tests can point them at a miniature copy; real callers pass
	// this.
	SysctlDir = "/proc/sys"
)

// Version is the liken version this binary was built as, stamped by
// the build (-ldflags -X): a release name when the releases domain is
// building, the git-described commit for a development build
// (version.mk at the repo root explains the mechanism). It reaches
// the cluster as status.version.liken, which the operator compares
// against the Cluster's spec.version target to decide whether this
// machine needs an upgrade.
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
// Generation counts spec changes (the API server bumps it on spec
// writes and leaves it alone on status writes), which is what lets a
// condition say which version of the spec it judged.
type ObjectMeta struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
}

// MachineSpec is the declared half: what a person (or a git
// repository, via whatever GitOps engine a deployment chooses) asks
// this machine to be. Each field notes who acts on
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

	// Modules names extra kernel modules this machine loads at boot,
	// beyond the fixed list the OS itself needs (the image's
	// modules.conf): the drivers for whatever hardware this machine's
	// workloads use. Init loads them once the boot's manifest is
	// known, so they cannot serve the boot path itself; a driver the
	// boot depends on belongs in the fixed list. The image build ships
	// the union of every machine's declared modules, reading the same
	// manifests it bakes, which means a module declared here is only
	// loadable if the booted image was built from manifests that
	// declared it; status.modules reports each name's outcome either
	// way. Edits are staged and take effect at the next boot, like
	// storage.
	Modules []string `json:"modules,omitempty"`

	// NodeLabels is this machine's scheduling identity: the labels its
	// Kubernetes Node object carries, which is what workloads select
	// on (which machine has the GPU, which one sits on battery-backed
	// power). Applied twice, like sysctls: init renders them into the
	// k3s boot drop-in so the node registers already wearing them, and
	// the operator reconciles them live afterward. The operator also
	// removes a label this spec once declared and no longer does; the
	// kubelet applies registration labels but never removes stale
	// ones, so without that pass a retracted label would linger until
	// someone noticed. Labels applied outside this spec (kubectl
	// label, other controllers) are never touched.
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// Storage assigns storage roles to disks (see storage.go). Applied
	// by init at boot, before k3s: a filesystem can't be swapped under
	// a running cluster. Edits are staged to the machineState
	// filesystem and take effect at the next boot; RebootPolicy says
	// who initiates that boot.
	Storage StorageSpec `json:"storage,omitzero"`

	// RebootPolicy is what the operator may do when applying the spec
	// requires a reboot (today: any storage change). Manual, the
	// default, stages the change and reports it; any next boot
	// applies it. Auto lets the operator reboot the machine itself.
	// Manual is the default because on a single-node cluster a reboot
	// is a total outage, and a mistyped edit should never reboot the
	// machine automatically.
	RebootPolicy RebootPolicy `json:"rebootPolicy,omitempty"`
}

// RebootPolicy is who initiates the reboot a staged change waits on.
// Anything unrecognized reads as Manual, so an unrecognized value can
// never cause an automatic reboot.
type RebootPolicy string

const (
	RebootAuto   RebootPolicy = "Auto"
	RebootManual RebootPolicy = "Manual"
)

func (s MachineSpec) RebootPolicyOrDefault() RebootPolicy {
	if s.RebootPolicy == RebootAuto {
		return RebootAuto
	}
	return RebootManual
}

// NetworkSpec is deliberately almost empty. The default is zero
// configuration: DHCP on the first physical interface, hostname from
// the manifest, DNS from the lease. Fields exist here only for
// machines that need to deviate from that.
type NetworkSpec struct {
	// Interfaces configures the machine's interfaces explicitly, each
	// by name. Empty means the zero-configuration default above. A
	// machine in a cluster typically declares two: an uplink that
	// still speaks DHCP, and the cluster-facing interface with the
	// static address its peers were told to find it at.
	Interfaces []InterfaceSpec `json:"interfaces,omitempty"`
}

// InterfaceSpec configures one interface. The zero value beyond Name
// means DHCP: static addressing is the deviation, so it's the part
// that must be spelled out.
type InterfaceSpec struct {
	// Name is the interface to configure (e.g. "eth1"), as the kernel
	// names it. With no udev to rename anything, kernel names follow
	// hardware enumeration order, which is stable for fixed hardware.
	Name string `json:"name"`

	// Address is a static address in CIDR form ("10.10.0.1/24"); the
	// prefix length is how the kernel learns the subnet, so it is not
	// optional. Empty means DHCP on this interface.
	Address string `json:"address,omitempty"`

	// Gateway makes this interface the default route. Optional even
	// for static addresses: a cluster segment with nothing to route to
	// declares none, and the uplink's DHCP lease supplies the real one.
	Gateway string `json:"gateway,omitempty"`

	// Nameservers to use alongside whatever DHCP leases supply.
	Nameservers []string `json:"nameservers,omitempty"`
}

// Parse reads a Machine manifest from its bytes. Parsing is strict
// because a misspelled field name in a manifest should produce an
// error someone sees, rather than becoming a setting that silently
// never applies.
func Parse(raw []byte) (*Machine, error) {
	m := &Machine{}
	if err := yaml.UnmarshalStrict(raw, m); err != nil {
		return nil, err
	}
	if m.Kind != "Machine" {
		return nil, fmt.Errorf("expected kind Machine, got %q", m.Kind)
	}
	return m, nil
}

// Load reads a Machine manifest from a file. A machine with no
// manifest is still a valid machine (everything defaults), but a
// manifest that exists and doesn't parse, or declares some other kind,
// is a configuration error and is reported as one.
func Load(path string) (*Machine, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Machine{}, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}
