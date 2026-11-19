package machine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
)

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machine.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileIsAValidMachine(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("loading a missing file: %v, want an empty Machine", err)
	}
	if m.Metadata.Name != "" {
		t.Errorf("name is %q, want empty", m.Metadata.Name)
	}
}

func TestLoadParsesSpec(t *testing.T) {
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: liken-dev
spec:
  network:
    interfaces:
      - name: eth0
      - name: eth1
        address: 10.10.0.1/24
        gateway: 10.10.0.254
        nameservers: [9.9.9.9]
  sysctls:
    vm.overcommit_memory: "1"
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Metadata.Name != "liken-dev" {
		t.Errorf("name is %q, want liken-dev", m.Metadata.Name)
	}
	interfaces := m.Spec.Network.Interfaces
	if len(interfaces) != 2 {
		t.Fatalf("interfaces are %v, want 2", interfaces)
	}
	if interfaces[0].Name != "eth0" || interfaces[0].Address != "" {
		t.Errorf("eth0 is %+v, want a name and no address, which means DHCP", interfaces[0])
	}
	if interfaces[1].Address != "10.10.0.1/24" {
		t.Errorf("eth1 address is %q, want 10.10.0.1/24", interfaces[1].Address)
	}
	if interfaces[1].Gateway != "10.10.0.254" {
		t.Errorf("eth1 gateway is %q, want 10.10.0.254", interfaces[1].Gateway)
	}
	if len(interfaces[1].Nameservers) != 1 || interfaces[1].Nameservers[0] != "9.9.9.9" {
		t.Errorf("eth1 nameservers are %v, want [9.9.9.9]", interfaces[1].Nameservers)
	}
	if got := m.Spec.Sysctls["vm.overcommit_memory"]; got != "1" {
		t.Errorf("sysctl vm.overcommit_memory is %q, want 1", got)
	}
}

func TestLoadParsesModules(t *testing.T) {
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: liken-dev
spec:
  modules: [nvidia, v4l2loopback]
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Spec.Modules) != 2 || m.Spec.Modules[0] != "nvidia" || m.Spec.Modules[1] != "v4l2loopback" {
		t.Errorf("modules are %v, want [nvidia v4l2loopback]", m.Spec.Modules)
	}
}

func TestLoadParsesNodeLabels(t *testing.T) {
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: liken-dev
spec:
  nodeLabels:
    guid.foo/gpu: "true"
    topology.kubernetes.io/zone: closet
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Spec.NodeLabels["guid.foo/gpu"]; got != "true" {
		t.Errorf("nodeLabels guid.foo/gpu is %q, want true", got)
	}
	if got := m.Spec.NodeLabels["topology.kubernetes.io/zone"]; got != "closet" {
		t.Errorf("nodeLabels topology.kubernetes.io/zone is %q, want closet", got)
	}
}

func TestLoadParsesNodeTaints(t *testing.T) {
	// The second taint has no value, which is optional.
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: liken-dev
spec:
  nodeTaints:
    - key: guid.foo/drill
      value: node-taints
      effect: PreferNoSchedule
    - key: node-role.kubernetes.io/database
      effect: NoSchedule
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	taints := m.Spec.NodeTaints
	if len(taints) != 2 {
		t.Fatalf("nodeTaints are %v, want 2", taints)
	}
	want := NodeTaint{Key: "guid.foo/drill", Value: "node-taints", Effect: TaintPreferNoSchedule}
	if taints[0] != want {
		t.Errorf("nodeTaints[0] is %+v, want %+v", taints[0], want)
	}
	want = NodeTaint{Key: "node-role.kubernetes.io/database", Effect: TaintNoSchedule}
	if taints[1] != want {
		t.Errorf("nodeTaints[1] is %+v, want %+v", taints[1], want)
	}
}

func TestLoadRejectsWrongKind(t *testing.T) {
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Toaster
metadata:
  name: liken-dev
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted kind Toaster, want an error")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: liken-dev
spec:
  networkk: {}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted the misspelled field networkk, want an error")
	}
}

func TestLoadReportsAnUnreadableFile(t *testing.T) {
	path := unreadableFile(t, filepath.Join(t.TempDir(), "machine.yaml"))
	if _, err := Load(path); err == nil {
		t.Error("Load of a manifest that exists but cannot be read returned no error, want an error and not a default Machine")
	}
}

func TestNetworkValidateAcceptsATypicalClusterMachine(t *testing.T) {
	spec := NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth1", Address: "10.10.0.1/24"},
	}}
	if err := spec.Validate(); err != nil {
		t.Error(err)
	}
}

func TestNetworkValidateRejectsAnInterfaceWithNoName(t *testing.T) {
	// Without a name, init cannot tell which interface the entry
	// configures.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{{Address: "10.0.0.1/24"}}}
	if err := spec.Validate(); err == nil {
		t.Error("Validate accepted an interface with no name, want an error")
	}
}

func TestNetworkValidateRejectsTwoEntriesForOnePort(t *testing.T) {
	// The API server rejects this through the list's name key, but a
	// manifest on an install stick never reaches the API server.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth0", Address: "10.0.0.1/24"},
	}}
	err := spec.Validate()
	if err == nil {
		t.Fatal("Validate accepted two entries for eth0, want an error")
	}
	if !strings.Contains(err.Error(), "eth0") {
		t.Errorf("the error is %q, want it to name eth0", err)
	}
}

func TestNetworkValidateAcceptsNoInterfacesAtAll(t *testing.T) {
	// An empty spec is the default configuration, not an error.
	if err := (NetworkSpec{}).Validate(); err != nil {
		t.Error(err)
	}
}

func TestLoadParsesHostEntries(t *testing.T) {
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: liken-dev
spec:
  network:
    hostEntries:
      - address: 10.10.0.20
        names: [nas, nas.home.arpa]
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := m.Spec.Network.HostEntries
	if len(entries) != 1 {
		t.Fatalf("hostEntries are %v, want 1", entries)
	}
	want := HostEntry{Address: "10.10.0.20", Names: []string{"nas", "nas.home.arpa"}}
	if !reflect.DeepEqual(entries[0], want) {
		t.Errorf("hostEntries[0] is %+v, want %+v", entries[0], want)
	}
}

func TestNetworkValidateAcceptsAHostEntry(t *testing.T) {
	spec := NetworkSpec{HostEntries: []HostEntry{
		{Address: "10.10.0.20", Names: []string{"nas", "nas.home.arpa"}},
	}}
	if err := spec.Validate(); err != nil {
		t.Error(err)
	}
}

func TestNetworkValidateRejectsAHostEntryThatIsNotALiteralAddress(t *testing.T) {
	spec := NetworkSpec{HostEntries: []HostEntry{
		{Address: "nas.example.com", Names: []string{"nas"}},
	}}
	if err := spec.Validate(); err == nil {
		t.Error("Validate accepted a hostname as an address, want an error")
	}
}

func TestNetworkValidateRejectsTwoHostEntriesForOneAddress(t *testing.T) {
	spec := NetworkSpec{HostEntries: []HostEntry{
		{Address: "10.10.0.20", Names: []string{"nas"}},
		{Address: "10.10.0.20", Names: []string{"nas2"}},
	}}
	err := spec.Validate()
	if err == nil {
		t.Fatal("Validate accepted two entries for 10.10.0.20, want an error")
	}
	if !strings.Contains(err.Error(), "10.10.0.20") {
		t.Errorf("the error is %q, want it to name 10.10.0.20", err)
	}
}

func TestNetworkValidateRejectsAHostEntryWithNoNames(t *testing.T) {
	spec := NetworkSpec{HostEntries: []HostEntry{{Address: "10.10.0.20"}}}
	if err := spec.Validate(); err == nil {
		t.Error("Validate accepted a host entry with no names, want an error")
	}
}

func TestNetworkValidateRejectsAHostEntryNamedLocalhost(t *testing.T) {
	spec := NetworkSpec{HostEntries: []HostEntry{
		{Address: "10.10.0.20", Names: []string{"localhost"}},
	}}
	if err := spec.Validate(); err == nil {
		t.Error("Validate accepted an entry that lists localhost, want an error")
	}
}

func TestMachineReturnsItsMetadata(t *testing.T) {
	m := &Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "42"}}
	if meta := m.GetObjectMeta(); meta.GetName() != "node-1" || meta.GetResourceVersion() != "42" {
		t.Errorf("GetObjectMeta = %q at %q, want node-1 at 42", meta.GetName(), meta.GetResourceVersion())
	}
}
