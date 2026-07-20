// Package machine is the Machine API. It defines liken's one
// configuration document as Go types.
//
// A Machine has the same shape as a Kubernetes resource. Kubelet's
// KubeletConfiguration, k0s's config, and Talos's machine config also
// use this shape. This design lets one schema-validated document
// reach the machine in two ways. At boot, init reads the document
// from a file baked into the image, because no API server exists yet
// at that point. After the cluster starts, the same document exists
// in the cluster as a custom resource. There, the liken operator
// publishes the machine's live facts into the document's status. The
// file that a person writes by hand and the object that the command
// `kubectl get machine -o yaml` returns are the same document.
//
// Two programs use this API, and this package is what they share.
// Init reads the spec and applies the network settings and the
// sysctls at boot. Init also produces the facts. The operator reads
// the facts and reconciles the spec for as long as the machine runs.
// This division is deliberate. Init never talks to Kubernetes. The
// operator never touches boot-time state. The file at
// `/run/liken/facts.yaml` is the one-way channel between the two
// programs.
//
// The api package defines the document's shape: the group and
// version it declares, its metadata, and the condition and phase
// vocabulary that its status uses. Every other liken document shares
// this same shape.
//
// A note on naming: the names `machine.MachineSpec` and
// `machine.MachineStatus` repeat the package name. Go's naming advice
// warns against this repetition, but this package repeats it on
// purpose. The types mirror the CRD kind, `Machine`, and Kubernetes'
// `XxxSpec`/`XxxStatus` convention. Matching what the command
// `kubectl explain machine.spec` shows is worth more than avoiding
// this repetition.
package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/liken-sh/liken/api"
	"sigs.k8s.io/yaml"
)

const (
	// MachineManifestDir is the directory where the image carries
	// Machine manifests, one file for each machine (<name>.yaml). One
	// image boots a whole fleet of machines, so the image carries
	// every machine's manifest. Each boot selects its own manifest by
	// the liken.machine=<name> kernel parameter. On a machine with
	// exactly one manifest, that manifest is the only choice, and the
	// boot uses it automatically.
	MachineManifestDir = "/etc/liken/machines"

	// BootManifestPath is the file where init publishes the manifest
	// that this boot actually ran under. This manifest is the staged
	// or proven copy from machineState, or, on a first boot, the
	// image's seed manifest. The operator reads this file through a
	// hostPath mount. The operator uses the file to know which Machine
	// it manages, and to seed the in-cluster Machine on the first
	// boot. Like the facts file, this file lives under /run because it
	// describes only the current boot.
	BootManifestPath = "/run/liken/machine.yaml"

	// FactsPath is the file where init publishes what it learned about
	// the machine. The file has the exact same shape as MachineStatus.
	// /run is a fresh tmpfs on every boot, and this suits the facts
	// well. The facts describe only the current boot, and never
	// survive into the next boot.
	FactsPath = "/run/liken/facts.yaml"

	// SysctlDir is the kernel's tuning interface: one file for each
	// parameter. The sysctl helper functions take the directory as a
	// parameter, so tests can point them at a small copy of the
	// directory. Real callers pass this constant.
	SysctlDir = "/proc/sys"
)

// Version is the liken version that this binary was built as. The
// build process stamps this value using -ldflags -X. When the
// releases domain builds the binary, the value is a release name. For
// a development build, the value is the git-described commit
// (version.mk at the repo root explains this mechanism). This value
// reaches the cluster as status.version.liken. The operator compares
// this value against the Cluster's spec.version target to decide
// whether this machine needs an upgrade.
var Version = "dev"

// The struct tags are json, not yaml, because parsing goes through
// sigs.k8s.io/yaml, the same converter that Kubernetes tooling uses.
// This converter turns YAML into JSON before it unmarshals the data.
// This step gives Kubernetes documents their camelCase convention. It
// also means these structs serialize the same way, whether the data
// comes from a file or from the API server.
type Machine struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   api.ObjectMeta `json:"metadata"`
	Spec       MachineSpec    `json:"spec,omitzero"`
	Status     MachineStatus  `json:"status,omitzero"`
}

// MachineSpec is the declared half of a Machine. It states what a
// person asks this machine to be. A git repository can also declare
// this, through whatever GitOps engine a deployment chooses. Each
// field notes who acts on it and when, because the actuators differ.
// Some state can only be set while the machine is being built. Some
// state can be reconciled live.
type MachineSpec struct {
	// Network is applied by init at boot. The cluster cannot reconcile
	// this field from inside the cluster, because reaching the cluster
	// depends on the network settings. Changes to this field take
	// effect at the next boot.
	Network NetworkSpec `json:"network,omitzero"`

	// Sysctls is kernel tuning: it maps a parameter name to its
	// desired value, for example "vm.overcommit_memory": "1". The
	// system applies sysctls twice, by design. Init sets them at boot,
	// so they hold their values before k3s starts. The operator then
	// reconciles them live, so a kubectl edit takes effect without a
	// reboot.
	Sysctls map[string]string `json:"sysctls,omitempty"`

	// Modules names extra kernel modules that this machine loads at
	// boot, beyond the fixed list that the OS itself needs (the
	// image's modules.conf). These extra modules are the drivers for
	// whatever hardware this machine's workloads use. Init loads them
	// only after it knows the boot's manifest, so these modules cannot
	// serve the boot path itself. A driver that the boot depends on
	// must belong in the fixed list instead. The image build ships the
	// union of every machine's declared modules, because it reads the
	// same manifests that it bakes into the image. This means a module
	// declared here is only loadable if the booted image was built
	// from manifests that also declared it. status.modules reports the
	// outcome for each module name, either way. Edits to this field
	// are staged and take effect at the next boot, like storage.
	Modules []string `json:"modules,omitempty"`

	// NodeLabels is this machine's scheduling identity: the labels
	// that its Kubernetes Node object carries. Workloads select
	// machines using these labels, for example to find which machine
	// has the GPU, or which machine runs on battery-backed power. The
	// system applies node labels twice, like sysctls. Init renders the
	// labels into the k3s boot drop-in, so the node already carries
	// them when it registers. The operator then reconciles the labels
	// live afterward. The operator also removes a label that this spec
	// once declared but no longer declares. The kubelet applies
	// registration labels but never removes stale ones, so without
	// this removal step, a retracted label would remain until someone
	// noticed it. The operator never touches labels applied outside
	// this spec, such as labels set by kubectl label or by other
	// controllers.
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// Storage assigns storage roles to disks (see storage.go). Init
	// applies this field at boot, before k3s starts, because a
	// filesystem cannot be swapped while a cluster is running. Edits
	// are staged to the machineState filesystem and take effect at the
	// next boot. RebootPolicy says who starts that boot.
	Storage StorageSpec `json:"storage,omitzero"`

	// RebootPolicy states what the operator may do when applying the
	// spec requires a reboot. Today, only a storage change requires a
	// reboot. Manual, the default, stages the change and reports it;
	// the next boot, whenever it happens, applies the change. Auto
	// lets the operator reboot the machine itself. Manual is the
	// default because a reboot on a single-node cluster is a total
	// outage, and a mistyped edit should never reboot the machine
	// automatically.
	RebootPolicy RebootPolicy `json:"rebootPolicy,omitempty"`
}

// RebootPolicy states who starts the reboot that a staged change
// waits on. The system treats any unrecognized value as Manual, so an
// unrecognized value can never cause an automatic reboot.
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
// configuration: DHCP on the first physical interface, the hostname
// from the manifest, and DNS from the DHCP lease. Fields exist here
// only for machines that need to differ from this default.
type NetworkSpec struct {
	// Interfaces configures the machine's interfaces explicitly, each
	// by name. An empty value means the zero-configuration default
	// described above. A machine in a cluster typically declares two
	// interfaces. One is an uplink that still uses DHCP. The other is
	// the cluster-facing interface, which uses the static address
	// that other machines were configured to use when they contact
	// it.
	Interfaces []InterfaceSpec `json:"interfaces,omitempty"`
}

// InterfaceSpec configures one interface. Beyond Name, the zero value
// means DHCP. Static addressing is the deviation from the default, so
// a person must spell it out explicitly.
type InterfaceSpec struct {
	// Name is the interface to configure (for example, "eth1"), using
	// the name that the kernel gives it. Because no udev process
	// renames interfaces, kernel names follow the hardware enumeration
	// order, which stays stable for fixed hardware.
	Name string `json:"name"`

	// Address is a static address in CIDR form (for example,
	// "10.10.0.1/24"). The prefix length tells the kernel the subnet,
	// so the prefix length is not optional. An empty value means DHCP
	// on this interface.
	Address string `json:"address,omitempty"`

	// Gateway makes this interface the default route. This field is
	// optional, even for static addresses. A cluster segment with
	// nothing to route to declares no gateway, and the uplink's DHCP
	// lease supplies the real default route.
	Gateway string `json:"gateway,omitempty"`

	// Nameservers lists nameservers to use in addition to any that
	// DHCP leases supply.
	Nameservers []string `json:"nameservers,omitempty"`
}

// Parse reads a Machine manifest from its bytes. Parsing is strict,
// because a misspelled field name in a manifest should produce an
// error that someone sees. Without strict parsing, a misspelled field
// would become a setting that silently never applies.
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
// manifest is still a valid machine, because every field defaults.
// But a manifest that exists and does not parse, or that declares
// some other kind, is a configuration error. Load reports this error
// as a configuration error.
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
