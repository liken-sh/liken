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

func TestLoadParsesAnInterfaceDeclaredByMAC(t *testing.T) {
	// A machine with two ports of the same model cannot be described
	// by kernel names, so a manifest may leave the name out and give
	// the port's address instead.
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: big
spec:
  network:
    interfaces:
      - mac: e0:51:d8:aa:bb:01
        address: 10.0.0.243/24
        gateway: 10.0.0.1
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	interfaces := m.Spec.Network.Interfaces
	if len(interfaces) != 1 {
		t.Fatalf("interfaces: got %v", interfaces)
	}
	if interfaces[0].MAC != "e0:51:d8:aa:bb:01" {
		t.Errorf("mac: got %q", interfaces[0].MAC)
	}
	if interfaces[0].Name != "" {
		t.Errorf("a manifest that gives only a mac declares no name: got %q", interfaces[0].Name)
	}
}

func TestNetworkValidateAcceptsEitherWayOfNamingAPort(t *testing.T) {
	// Name and MAC are both first-class, and an entry may carry
	// both, which asks the boot to check that they agree.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{MAC: "e0:51:d8:aa:bb:02"},
		{Name: "eth2", MAC: "e0:51:d8:aa:bb:03"},
	}}
	if err := spec.Validate(); err != nil {
		t.Error(err)
	}
}

func TestNetworkValidateAcceptsTheFormsPeoplePaste(t *testing.T) {
	// net.ParseMAC reads every spelling a person is likely to copy:
	// a Linux tool's colons, a firmware screen's hyphens, and a
	// switch console's dotted quads.
	for _, mac := range []string{"e0:51:d8:aa:bb:01", "E0-51-D8-AA-BB-01", "e051.d8aa.bb02"} {
		spec := NetworkSpec{Interfaces: []InterfaceSpec{{MAC: mac}}}
		if err := spec.Validate(); err != nil {
			t.Errorf("%s: %v", mac, err)
		}
	}
}

func TestNetworkValidateRejectsAnInterfaceThatNamesNoPort(t *testing.T) {
	// An entry with neither field configures nothing. Left to the
	// boot, it would be a silent no-op on a machine with no shell.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{{Address: "10.0.0.1/24"}}}
	if err := spec.Validate(); err == nil {
		t.Error("expected an error for an interface with neither a name nor a mac")
	}
}

func TestNetworkValidateRejectsAMalformedMAC(t *testing.T) {
	spec := NetworkSpec{Interfaces: []InterfaceSpec{{MAC: "e0:51:d8:aa:bb"}}}
	err := spec.Validate()
	if err == nil {
		t.Fatal("expected an error for a MAC address of five octets")
	}
	if !strings.Contains(err.Error(), "e0:51:d8:aa:bb") {
		t.Errorf("the error must quote what was written: %v", err)
	}
}

func TestNetworkValidateRejectsTwoEntriesForOnePort(t *testing.T) {
	// The CRD's list of interfaces is atomic, so the API server no
	// longer refuses a repeated key. Two entries for one port would
	// configure the second over the first.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth0", Address: "10.0.0.1/24"},
	}}
	if err := spec.Validate(); err == nil {
		t.Error("expected an error for two entries naming eth0")
	}
}

func TestNetworkValidateSeesThroughADifferentSpellingOfOneMAC(t *testing.T) {
	// Two spellings of one address are still one port.
	spec := NetworkSpec{Interfaces: []InterfaceSpec{
		{MAC: "e0:51:d8:aa:bb:01"},
		{MAC: "E0-51-D8-AA-BB-01"},
	}}
	if err := spec.Validate(); err == nil {
		t.Error("expected an error for one address written twice")
	}
}

func TestNetworkValidateAcceptsNoInterfacesAtAll(t *testing.T) {
	// An empty spec is the zero-configuration default, not a
	// mistake.
	if err := (NetworkSpec{}).Validate(); err != nil {
		t.Error(err)
	}
}

func TestInterfaceIdentityUsesTheWordsTheManifestUsed(t *testing.T) {
	// Every console message about an interface names it this way, so
	// a person finds the line they wrote and not a kernel name they
	// never chose.
	cases := map[string]InterfaceSpec{
		"eth0":                         {Name: "eth0"},
		"MAC e0:51:d8:aa:bb:01":        {MAC: "e0:51:d8:aa:bb:01"},
		"eth0 (MAC e0:51:d8:aa:bb:01)": {Name: "eth0", MAC: "e0:51:d8:aa:bb:01"},
	}
	for want, spec := range cases {
		if got := spec.Identity(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
