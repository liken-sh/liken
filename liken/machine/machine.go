// Package machine defines the Machine API: the Go types of the
// document that configures one liken machine.
//
// A Machine has the shape of a Kubernetes resource, like
// KubeletConfiguration and Talos's machine config, so the same
// document works before and after the cluster exists. At boot, init
// reads it from a file in the image, because no API server runs yet.
// Once the cluster runs, the document is also a custom resource, and
// the operator writes the machine's state into its status. A manifest
// that a person writes by hand and the output of `kubectl get machine
// -o yaml` are the same document.
//
// Init and the operator both import this package. Init reads the spec,
// applies it at boot, and writes the facts that become the Machine's
// status to the tree under /run/liken/facts (factstree.go). The
// operator reads those facts and reconciles the spec while the machine
// runs. Init never talks to Kubernetes, and the operator never changes
// a boot-time setting on a running machine. It stages the change for
// the next boot.
//
// The api package defines what every liken document shares: the group
// and version, the metadata, and the conditions and phases of a
// status.
//
// MachineSpec and MachineStatus repeat the package name so that they
// match the CRD kind and the Kubernetes XxxSpec and XxxStatus
// convention that `kubectl explain machine.spec` shows.
package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"slices"

	"github.com/liken-sh/liken/liken/api"
	"sigs.k8s.io/yaml"
)

const (
	// MachineManifestDir is the directory in the image with one
	// manifest for each machine, named <name>.yaml, so one image boots
	// a whole fleet. A boot uses the manifest that the
	// liken.machine=<name> kernel parameter selects, or the only
	// manifest when the image has one.
	MachineManifestDir = "/etc/liken/machines"

	// BootManifestPath is where init writes the manifest that this
	// boot runs under: a staged manifest on its proving boot, the last
	// manifest that booted successfully, or, on the machine's first
	// boot, the manifest from the image. The operator reads it through
	// a hostPath mount to find which Machine it manages, and creates
	// that Machine in the cluster from it on the first boot. Init
	// writes the bytes it read, so the operator receives the manifest
	// as the person wrote it, not a re-serialized copy. The file is
	// under /run because it describes only the current boot.
	BootManifestPath = "/run/liken/machine.yaml"

	// SysctlDir is the kernel's sysctl tree. The sysctl functions take
	// the directory as an argument so that a test can pass a temporary
	// one.
	SysctlDir = "/proc/sys"
)

// Version is the liken version of this binary, which the build sets
// with -ldflags -X: a release name, or for a development build the
// output of git describe (liken/version.mk). Init reports its value as
// status.version.liken, and the operator compares that with the
// Cluster's spec.version to decide whether the machine needs an
// upgrade.
var Version = "dev"

// Machine is the document that configures one machine, as a manifest
// file or as a custom resource.
//
// The struct tags are json because sigs.k8s.io/yaml converts YAML to
// JSON before it unmarshals, so the same structs read a file and an
// API server response.
type Machine struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   api.ObjectMeta `json:"metadata"`
	Spec       MachineSpec    `json:"spec,omitzero"`
	Status     MachineStatus  `json:"status,omitzero"`
}

// GetObjectMeta returns the metadata, for the watch store and the
// operators, which read every liken object through api.Meta.
func (m *Machine) GetObjectMeta() api.Meta { return m.Metadata }

// MachineSpec is the desired state of a Machine. The comment on each
// field says whether init applies it at boot, the operator applies it
// on the running machine, or both, because an edit to a field that
// only a boot applies waits for a reboot.
type MachineSpec struct {
	// Network is applied by init at boot. The cluster reaches the
	// machine over the addresses that a network edit changes, so
	// applying the edit live would cut the machine off from the API
	// server. The operator stages the edit on the machineState
	// filesystem instead, and the next boot applies it. RebootPolicy
	// decides who starts that boot. HostEntries changes no address,
	// so the operator applies an edit to it live.
	Network NetworkSpec `json:"network,omitzero"`

	// Sysctls maps a kernel parameter to its value, for example
	// "vm.overcommit_memory": "1". Init sets them at boot, before k3s
	// starts, and the operator reconciles them on the running machine,
	// so an edit applies without a reboot.
	Sysctls map[string]string `json:"sysctls,omitempty"`

	// Rlimits maps a per-process resource to its limit, for example
	// "nofile": "1048576". Before init starts k3s, it sets on itself
	// the limits in OSRlimits and then these, and every process on the
	// machine inherits them. A process inherits its limits when it
	// starts, so an edit cannot change the k3s that is running. The
	// operator stages the edit for the next boot.
	Rlimits map[string]string `json:"rlimits,omitempty"`

	// Modules lists the kernel modules that this machine loads in
	// addition to the ones that the OS always loads (the image's
	// /etc/liken/modules.conf), usually the drivers for hardware that
	// workloads use. Init loads them after it reads the manifest, so a
	// driver that the boot itself needs belongs in modules.conf.
	//
	// The image has the kernel's whole module tree, so any module of
	// this kernel can be listed. status.modules reports the result for
	// each name, including a name that this kernel has no module for.
	//
	// The operator loads an added module without a reboot, and the
	// kernel binds it to hardware that is already plugged in. A
	// removed module stays loaded until the next boot, because the
	// operator never unloads a module.
	Modules []string `json:"modules,omitempty"`

	// ModuleParameters sets parameters for the modules in Modules. A
	// key is "<module>.<parameter>", as on the kernel command line,
	// and its module must be spelled as in Modules. A module reads its
	// parameters only when it loads, so a parameter added together
	// with its module applies without a reboot, and a change for a
	// module that is already loaded waits for the next boot.
	ModuleParameters map[string]string `json:"moduleParameters,omitempty"`

	// Serio lists the serial lines whose kernel driver binds only
	// after a program attaches the line to the serio layer, such as a
	// USB-CEC adapter (serio.go). Init keeps each attachment open
	// until the machine reboots. An entry loads no module, so the
	// serial driver, the serport line discipline, and the protocol's
	// driver belong in Modules.
	//
	// An added entry attaches without a reboot. A removed entry stays
	// attached until the next boot, because pods can hold the devices
	// that the attachment created.
	Serio []SerioAttachment `json:"serio,omitempty"`

	// NodeLabels are labels on this machine's Node, for example to let
	// workloads find the machine with the GPU. Init writes them into
	// the k3s boot drop-in, so the Node has them when it registers,
	// and the operator reconciles them after that. The kubelet never
	// removes a registration label, so the operator removes a label
	// that this spec declared before and no longer declares. It leaves
	// alone every label that this spec never declared, such as one
	// from `kubectl label`.
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// NodeTaints are the taints on this machine's Node, for example to
	// reserve the machine for one workload. A taint is identified by
	// its key and effect together, and one key can have two effects,
	// so this is a list where NodeLabels is a map.
	//
	// Init writes the taints into the k3s boot drop-in, so a new Node
	// has them when it registers and never runs a pod that they keep
	// out. The kubelet applies registration taints only when it
	// creates the Node, so on every later boot only the operator
	// applies a change. As with NodeLabels, the operator removes a
	// taint that this spec no longer declares and leaves alone one
	// that it never declared.
	NodeTaints []NodeTaint `json:"nodeTaints,omitempty"`

	// Storage assigns storage roles to disks (storage.go). Init
	// applies it at boot, before k3s starts, because a filesystem
	// cannot be replaced while the cluster runs on it. The operator
	// stages an edit for the next boot.
	Storage StorageSpec `json:"storage,omitzero"`

	// Disks sets the request queue of whole disks (disksettings.go),
	// keyed by the same device names that Storage accepts. Init applies
	// it at boot, before k3s starts, together with liken's default for
	// the clusterState disk, and the operator applies it again on every
	// pass, so an edit takes effect without a reboot. An entry that an
	// edit removes keeps its value until the next boot, as a removed
	// sysctl does.
	Disks []DiskSettings `json:"disks,omitempty"`

	// RebootPolicy decides what the operator does when a spec change
	// needs a reboot, such as a change to storage or network or a
	// removed module. With Manual, the default, the operator stages
	// the change and reports it, and the change applies at the next
	// boot. With Auto, the operator reboots the machine. Manual is the
	// default because a reboot of a single-node cluster is an outage,
	// and a mistyped edit must not cause one.
	RebootPolicy RebootPolicy `json:"rebootPolicy,omitempty"`
}

// NodeTaint is one taint on this machine's Node. Value is optional.
type NodeTaint struct {
	Key    string      `json:"key"`
	Value  string      `json:"value,omitempty"`
	Effect TaintEffect `json:"effect"`
}

// TaintEffect is a Kubernetes taint effect.
type TaintEffect string

const (
	TaintNoSchedule       TaintEffect = "NoSchedule"
	TaintPreferNoSchedule TaintEffect = "PreferNoSchedule"
	TaintNoExecute        TaintEffect = "NoExecute"
)

// RebootPolicy chooses who starts the reboot that a staged change waits
// for. Any value other than Auto counts as Manual, so an unknown value
// never causes an automatic reboot.
type RebootPolicy string

const (
	RebootAuto   RebootPolicy = "Auto"
	RebootManual RebootPolicy = "Manual"
)

// RebootPolicyOrDefault returns RebootAuto when the spec sets Auto, and
// RebootManual for every other value, including an empty one.
func (s MachineSpec) RebootPolicyOrDefault() RebootPolicy {
	if s.RebootPolicy == RebootAuto {
		return RebootAuto
	}
	return RebootManual
}

// NetworkSpec is the network configuration of a machine whose network differs
// from the default: DHCP on the first physical interface, the hostname
// from the manifest, and DNS from the DHCP lease.
type NetworkSpec struct {
	// Interfaces configures the named interfaces. When it is empty,
	// the machine uses the default. A clustered machine often declares
	// two: an uplink on DHCP, and a cluster interface with the static
	// address that the other machines connect to.
	Interfaces []InterfaceSpec `json:"interfaces,omitempty"`

	// HostEntries adds lines to /etc/hosts, below the fixed lines for
	// localhost and this machine's own name, so an entry can add a
	// name but cannot override those. Init also writes
	// /etc/nsswitch.conf so that every resolver reads /etc/hosts
	// before DNS. So a program that resolves names from local files,
	// such as the NFS mount helper, works while cluster DNS is down.
	//
	// Init writes the file at every boot, and the operator reconciles
	// it on every pass, so an edit applies without a reboot.
	HostEntries []HostEntry `json:"hostEntries,omitempty"`
}

// Validate checks the spec for errors that a person can fix in the
// manifest, before init configures any link.
//
// The CRD schema enforces the same rules for a spec applied through
// the API server: unique interface names, unique host entry addresses,
// address patterns, at least one name in each host entry, and no entry
// for localhost. Validate covers a manifest that never reaches the API
// server, such as one on an install stick.
//
// Validate does not check that the machine has an interface with each
// declared name. Init checks that against the machine's links.
func (s NetworkSpec) Validate() error {
	claimed := map[string]int{}
	for i, ifc := range s.Interfaces {
		if ifc.Name == "" {
			return fmt.Errorf("spec.network.interfaces[%d] has no name, so init cannot tell which interface it configures", i)
		}
		// Init would apply the second entry over the first.
		if first, seen := claimed[ifc.Name]; seen {
			return fmt.Errorf("spec.network.interfaces[%d] and spec.network.interfaces[%d] both declare %s, so remove one of them", first, i, ifc.Name)
		}
		claimed[ifc.Name] = i
		if ifc.Wireless != nil {
			if err := ifc.Wireless.validate(ifc.Name); err != nil {
				return err
			}
		}
	}

	claimedAddresses := map[string]int{}
	for i, entry := range s.HostEntries {
		// Init writes the address into /etc/hosts as it is, so a
		// hostname or a CIDR block would write a line that resolves
		// nothing.
		if net.ParseIP(entry.Address) == nil {
			return fmt.Errorf("spec.network.hostEntries[%d] has the address %q, which is not a literal IP address", i, entry.Address)
		}
		// The hosts file has one line for each address, and the facts
		// key the entries by address.
		if first, seen := claimedAddresses[entry.Address]; seen {
			return fmt.Errorf("spec.network.hostEntries[%d] and spec.network.hostEntries[%d] both have the address %s, so put all of its names in one entry", first, i, entry.Address)
		}
		claimedAddresses[entry.Address] = i
		if len(entry.Names) == 0 {
			return fmt.Errorf("spec.network.hostEntries[%d] (%s) has no names, so it resolves nothing", i, entry.Address)
		}
		if slices.Contains(entry.Names, "localhost") {
			return fmt.Errorf("spec.network.hostEntries[%d] (%s) lists localhost, which the fixed lines of /etc/hosts already define", i, entry.Address)
		}
	}
	return nil
}

// InterfaceSpec configures one interface. When only Name is set, the
// interface uses DHCP.
type InterfaceSpec struct {
	// Name is the interface's kernel name, for example "eth1". The
	// machine runs no udev to rename interfaces, so the names follow
	// the order in which the kernel finds the hardware, which does not
	// change on fixed hardware.
	Name string `json:"name"`

	// Address is a static address in CIDR form, for example
	// "10.10.0.1/24". The prefix length sets the subnet, so it is
	// required. An empty value means DHCP.
	Address string `json:"address,omitempty"`

	// Gateway is the default route through this interface, and is
	// optional with a static address. A cluster interface that routes
	// nowhere else sets none, and the uplink's DHCP lease supplies the
	// default route.
	Gateway string `json:"gateway,omitempty"`

	// Nameservers are used in addition to the nameservers from a DHCP
	// lease.
	Nameservers []string `json:"nameservers,omitempty"`

	// Wireless is the network that this interface joins before it is
	// addressed. When it is nil, the interface is wired.
	Wireless *WirelessSpec `json:"wireless,omitempty"`
}

// HostEntry is one line of /etc/hosts: a literal IP address, not a CIDR
// block or a hostname, and the names that resolve to it.
type HostEntry struct {
	Address string   `json:"address"`
	Names   []string `json:"names"`
}

// Parse reads a Machine manifest. Parsing is strict, so a misspelled
// field is an error instead of a setting that never applies.
func Parse(raw []byte) (*Machine, error) {
	m := &Machine{}
	if err := yaml.UnmarshalStrict(raw, m); err != nil {
		return nil, err
	}
	if m.Kind != "Machine" {
		return nil, fmt.Errorf("the manifest has kind %q, want Machine", m.Kind)
	}
	return m, nil
}

// Load reads a Machine manifest from a file. A missing file returns an
// empty Machine, because every field has a default. A file that does
// not parse, or that declares another kind, is an error.
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
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}
