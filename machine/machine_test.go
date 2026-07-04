package machine

import (
	"os"
	"path/filepath"
	"testing"
)

// writeManifest drops a manifest into a fresh directory and returns its
// path, so each test can describe a machine in a few lines of YAML.
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
    interface: eth1
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
	if m.Spec.Network.Interface != "eth1" {
		t.Errorf("interface: got %q", m.Spec.Network.Interface)
	}
	if got := m.Spec.Sysctls["vm.overcommit_memory"]; got != "1" {
		t.Errorf("sysctl: got %q", got)
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
