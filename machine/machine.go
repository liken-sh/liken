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
// operator never touches boot-time state. The facts tree under
// `/run/liken/facts` is the one-way channel between the two programs
// (see factstree.go, which defines the tree and its grammar).
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
	"net"
	"os"
	"slices"

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
	// hostPath mount. The operator uses the file to identify the
	// Machine it manages, and to seed the in-cluster Machine on the first
	// boot. Like the facts tree, this file lives under /run because it
	// describes only the current boot. It stays one whole file, so the
	// operator reads the manifest's exact bytes, not a rendering.
	BootManifestPath = "/run/liken/machine.yaml"

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
// this value against the Cluster's spec.version target to determine
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
// this, through the cluster's flux feature. Each
// field notes who acts on it and when, because the actuators differ.
// Some state can only be set while the machine is being built. Some
// state can be reconciled live.
type MachineSpec struct {
	// Network is applied by init at boot. The cluster cannot reconcile
	// most of this field live, because the cluster reaches this
	// machine over the addresses that an edit changes: re-addressing a
	// running machine would cut the connection that carries the next
	// instruction. So an edit converges the same way a storage edit
	// does. The operator stages it to the machineState filesystem, and
	// the next boot applies it. RebootPolicy says who starts that
	// boot. HostEntries is the one exception: it names no address of
	// this machine's own, so an edit to it cannot cut the connection,
	// and it reconciles live instead (see its own comment below).
	Network NetworkSpec `json:"network,omitzero"`

	// Sysctls is kernel tuning: it maps a parameter name to its
	// desired value, for example "vm.overcommit_memory": "1". The
	// system applies sysctls twice, by design. Init sets them at boot,
	// so they hold their values before k3s starts. The operator then
	// reconciles them live, so a kubectl edit takes effect without a
	// reboot.
	Sysctls map[string]string `json:"sysctls,omitempty"`

	// Rlimits is per-process resource tuning: it maps a resource to
	// its desired limit, for example "nofile": "1048576". Init applies
	// the limits every liken machine holds (machine.OSRlimits) and
	// then these, to itself, before it starts k3s. Every process on
	// the machine inherits the result.
	//
	// Unlike sysctls, these cannot reconcile live. The kernel fixes a
	// process's limits when it forks, so no edit can reach a k3s that
	// is already running. An edit stages to the machineState
	// filesystem and applies at the next boot, the way storage and
	// network do. RebootPolicy says who starts that boot.
	Rlimits map[string]string `json:"rlimits,omitempty"`

	// Modules names extra kernel modules that this machine loads at
	// boot, beyond the fixed list that the OS itself needs (the
	// image's modules.conf). These extra modules are the drivers for
	// whatever hardware this machine's workloads use. Init loads them
	// only after it reads the boot's manifest, so these modules cannot
	// serve the boot path itself. A driver that the boot depends on
	// must belong in the fixed list instead.
	//
	// The image carries the kernel build's whole module tree, so any
	// name that kernel has is loadable here, whatever manifests
	// existed when the image was built. status.modules reports the
	// outcome for each name, including the name this kernel has no
	// module for.
	//
	// A name added here converges without a reboot: loading a driver
	// is live-capable, and the kernel binds a resident driver to
	// hardware that is already plugged in. Removing a name needs a
	// boot, because unloading is not part of that path.
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

	// NodeTaints is the repelling half of that same scheduling
	// identity. A label draws a workload in; a taint keeps out every
	// pod that does not tolerate it, which is how a machine dedicated
	// to one job stays dedicated to it. A taint's identity is its key
	// together with its effect, and one key can carry two effects at
	// once, so this is a list where NodeLabels is a map.
	//
	// Init renders these taints into the k3s boot drop-in, so a node
	// that registers for the first time is born repelling and never
	// accepts, in its first minutes, the pods that the taint exists to
	// keep out. The kubelet applies registration taints only when it
	// creates the Node object, on a first boot or after a reinstall. On
	// every later boot the Node object already exists and the setting
	// does nothing, so the operator's live reconciliation is the only
	// mechanism from then on. The operator also removes a taint that
	// this spec once declared and no longer declares, and it never
	// touches a taint applied outside this spec, such as one set by
	// kubectl taint or by another controller.
	NodeTaints []NodeTaint `json:"nodeTaints,omitempty"`

	// Storage assigns storage roles to disks (see storage.go). Init
	// applies this field at boot, before k3s starts, because a
	// filesystem cannot be swapped while a cluster is running. Edits
	// are staged to the machineState filesystem and take effect at the
	// next boot. RebootPolicy says who starts that boot.
	Storage StorageSpec `json:"storage,omitzero"`

	// RebootPolicy states what the operator may do when applying the
	// spec requires a reboot. A storage change requires one, a network
	// change requires one, and so does retracting a module. Manual,
	// the default, stages the change and reports it;
	// the next boot, whenever it happens, applies the change. Auto
	// lets the operator reboot the machine itself. Manual is the
	// default because a reboot on a single-node cluster is a total
	// outage, and a mistyped edit should never reboot the machine
	// automatically.
	RebootPolicy RebootPolicy `json:"rebootPolicy,omitempty"`
}

// NodeTaint is one taint on this machine's Node object. A pod
// tolerates a taint by naming its key and its effect, so those two
// fields together are the taint's identity. The value is optional: a
// taint often needs none, because the key and the effect alone already
// say to stay off this machine.
type NodeTaint struct {
	Key    string      `json:"key"`
	Value  string      `json:"value,omitempty"`
	Effect TaintEffect `json:"effect"`
}

// TaintEffect states what happens to a pod that does not tolerate the
// taint. NoSchedule keeps a new pod off the node and leaves the pods
// already running there alone. PreferNoSchedule asks the scheduler to
// avoid the node but permits it when no other node fits. NoExecute
// keeps new pods off and evicts the running ones as well.
type TaintEffect string

const (
	TaintNoSchedule       TaintEffect = "NoSchedule"
	TaintPreferNoSchedule TaintEffect = "PreferNoSchedule"
	TaintNoExecute        TaintEffect = "NoExecute"
)

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

	// HostEntries names addresses that resolve with no DNS lookup.
	// Init writes each one as a line in /etc/hosts, below the three
	// fixed lines that define localhost and this machine's own name,
	// so an entry can add a name but never override those two. Init
	// also writes /etc/nsswitch.conf on every boot, so that the hosts
	// file wins over DNS on every resolver the machine runs. This
	// gives a program that resolves a name from the host's own files,
	// such as the NFS mount helper, an answer that does not depend on
	// cluster DNS being up.
	//
	// Unlike the rest of network, an edit here applies live: init
	// writes the file at every boot, so the entries prove the cold
	// start on their own, and the machine operator then reconciles
	// the same file on every pass, so a later edit lands within one
	// reconcile pass, with no reboot.
	HostEntries []HostEntry `json:"hostEntries,omitempty"`
}

// Validate checks the spec's internal consistency. It catches the
// errors that a person can fix in the manifest, before the code
// touches any link.
//
// The API server enforces every one of these rules on any spec
// applied through it: the interface list is keyed by name, the host
// entry list is keyed by address, an address matches a pattern, a
// name list holds at least one item, and a CEL rule refuses the name
// localhost. This check exists for the specs that reach a machine
// another way: init also reads manifests that a person wrote by hand
// and carried in on a stick, and no API server ever saw those.
//
// Whether the machine really has a port with a declared name is a
// question only the machine can answer, so init answers that one
// against the links that exist.
func (s NetworkSpec) Validate() error {
	claimed := map[string]int{}
	for i, ifc := range s.Interfaces {
		if ifc.Name == "" {
			return fmt.Errorf("interface %d declares no name; the name is how an entry says which port it means", i)
		}
		// Two entries that name the same port are a manifest bug,
		// because the second entry's addressing would land on a link
		// the first already configured.
		if first, seen := claimed[ifc.Name]; seen {
			return fmt.Errorf("interfaces %d and %d both declare %s; declare each port once", first, i, ifc.Name)
		}
		claimed[ifc.Name] = i
	}

	claimedAddresses := map[string]int{}
	for i, entry := range s.HostEntries {
		// A hosts file line takes one literal address, not a hostname
		// or a CIDR block. init writes this value straight into
		// /etc/hosts with no lookup step, so a value that does not
		// parse as an address would write a line that resolves
		// nothing.
		if net.ParseIP(entry.Address) == nil {
			return fmt.Errorf("host entry %d declares %q as its address, which is not a literal address", i, entry.Address)
		}
		// One address is one line of the file, and the facts record
		// keys entries by address. Two entries that declare the same
		// address would collide on both, so a manifest declares each
		// address once, with all of its names.
		if first, seen := claimedAddresses[entry.Address]; seen {
			return fmt.Errorf("host entries %d and %d both declare %s; declare each address once", first, i, entry.Address)
		}
		claimedAddresses[entry.Address] = i
		if len(entry.Names) == 0 {
			return fmt.Errorf("host entry %d (%s) declares no names; an entry with no name resolves nothing", i, entry.Address)
		}
		// The fixed lines that init writes first already define
		// localhost. An entry that names it again could only shadow
		// or contradict a fixed line, never add to it.
		if slices.Contains(entry.Names, "localhost") {
			return fmt.Errorf("host entry %d (%s) names localhost; the fixed lines already define localhost, ahead of any entry", i, entry.Address)
		}
	}
	return nil
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

// HostEntry is one static line for /etc/hosts: one address and the
// names that resolve to it. Address is a literal IP address, not a
// CIDR block or a hostname, because a hosts file line answers with
// one address, and Names is the list of names that get that answer.
type HostEntry struct {
	Address string   `json:"address"`
	Names   []string `json:"names"`
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
