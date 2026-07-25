package machine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifest writes a manifest into a fresh directory and returns
// its path, so each test can describe a machine in a few lines of
// YAML.
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
		t.Fatalf("expected defaults, got error: %v", err)
	}
	if m.Metadata.Name != "" {
		t.Errorf("expected empty name, got %q", m.Metadata.Name)
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
		t.Errorf("name: got %q", m.Metadata.Name)
	}
	interfaces := m.Spec.Network.Interfaces
	if len(interfaces) != 2 {
		t.Fatalf("interfaces: got %v", interfaces)
	}
	if interfaces[0].Name != "eth0" || interfaces[0].Address != "" {
		t.Errorf("eth0 should default to DHCP: got %+v", interfaces[0])
	}
	if interfaces[1].Address != "10.10.0.1/24" {
		t.Errorf("eth1 address: got %q", interfaces[1].Address)
	}
	if interfaces[1].Gateway != "10.10.0.254" {
		t.Errorf("eth1 gateway: got %q", interfaces[1].Gateway)
	}
	if len(interfaces[1].Nameservers) != 1 || interfaces[1].Nameservers[0] != "9.9.9.9" {
		t.Errorf("eth1 nameservers: got %v", interfaces[1].Nameservers)
	}
	if got := m.Spec.Sysctls["vm.overcommit_memory"]; got != "1" {
		t.Errorf("sysctl: got %q", got)
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
		t.Errorf("modules: got %v", m.Spec.Modules)
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
		t.Errorf("nodeLabels: got %q", got)
	}
	if got := m.Spec.NodeLabels["topology.kubernetes.io/zone"]; got != "closet" {
		t.Errorf("nodeLabels: got %q", got)
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
		t.Fatal("expected an error for kind Toaster")
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
		t.Fatal("expected an error for the misspelled field")
	}
}

func TestLoadReportsAnUnreadableFile(t *testing.T) {
	path := unreadableFile(t, filepath.Join(t.TempDir(), "machine.yaml"))
	if _, err := Load(path); err == nil {
		t.Error("a manifest that exists but can't be read is an error, not a default machine")
	}
}

func TestNetworkValidateAcceptsATypicalClusterMachine(t *testing.T) {
	// The shape a clustered machine declares: a DHCP uplink and a
	// cluster segment with a fixed address on it.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth1", Address: "10.10.0.1/24"},
	}}
	if err := spec.Validate(); err != nil {
		t.Error(err)
	}
}

func TestNetworkValidateRejectsAnInterfaceThatNamesNoPort(t *testing.T) {
	// An entry with no name configures nothing. Left to the boot, it
	// would be a silent no-op on a machine with no shell.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{{Address: "10.0.0.1/24"}}}
	if err := spec.Validate(); err == nil {
		t.Error("expected an error for an interface with no name")
	}
}

func TestNetworkValidateRejectsTwoEntriesForOnePort(t *testing.T) {
	// The API server enforces this for a spec applied through it,
	// because the list is keyed by name. A manifest carried in on a
	// stick reaches init without that check, and two entries for one
	// port would configure the second over the first.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth0", Address: "10.0.0.1/24"},
	}}
	err := spec.Validate()
	if err == nil {
		t.Fatal("expected an error for two entries naming eth0")
	}
	if !strings.Contains(err.Error(), "eth0") {
		t.Errorf("the error must name the port declared twice: %v", err)
	}
}

func TestNetworkValidateAcceptsNoInterfacesAtAll(t *testing.T) {
	// An empty spec is the zero-configuration default, not a
	// mistake.
	if err := (NetworkSpec{}).Validate(); err != nil {
		t.Error(err)
	}
}
