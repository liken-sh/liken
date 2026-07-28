package image

// Tests for the deployment layer. The fixtures stand in for the two
// inputs: a manifests directory (a cluster document and machines) and
// a minted identity. These are small fakes with the same shapes as
// the real thing, so the tests need no real deployment.

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/identity"
)

// fixtureManifests writes a minimal deployment: one cluster document
// and one machine, optionally declaring kernel modules.
func fixtureManifests(t *testing.T, modules ...string) string {
	t.Helper()
	dir := t.TempDir()
	cluster := `apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: fixture
`
	if err := os.WriteFile(filepath.Join(dir, "cluster.yaml"), []byte(cluster), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "machines"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := strings.Builder{}
	m.WriteString(`apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
`)
	if len(modules) > 0 {
		m.WriteString("spec:\n  modules:\n")
		for _, name := range modules {
			m.WriteString("    - " + name + "\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "machines", "node-1.yaml"), []byte(m.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// builtLayer runs Layer over the fixtures and parses the archive.
func builtLayer(t *testing.T, manifests string) map[string]cpioEntry {
	t.Helper()
	identityDir := t.TempDir()
	if err := identity.Mint(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "deployment.cpio")
	if err := Layer(manifests, identityDir, out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]cpioEntry{}
	for _, e := range readArchive(t, raw) {
		byName[e.name] = e
	}
	return byName
}

func TestLayerCarriesTheDeployment(t *testing.T) {
	entries := builtLayer(t, fixtureManifests(t))
	for _, want := range []string{
		"etc/liken/cluster.yaml",
		"etc/liken/machines/node-1.yaml",
		"etc/liken/token",
		"var/lib/rancher/k3s/server/tls/server-ca.crt",
		"var/lib/rancher/k3s/server/tls/server-ca.key",
		"var/lib/rancher/k3s/server/tls/etcd/peer-ca.key",
	} {
		if _, ok := entries[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	if e := entries["etc/liken/token"]; e.mode&0o777 != 0o600 {
		t.Errorf("token mode: %o", e.mode&0o777)
	}
}

func TestLayerLeavesTheKubeconfigBehind(t *testing.T) {
	// The operator's credential lives beside the identity, but is not
	// part of it. A machine image carrying the admin certificate would
	// hand cluster-admin access to anyone who reads the disk.
	manifests := fixtureManifests(t)
	identityDir := t.TempDir()
	if err := identity.Mint(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := identity.Kubeconfig(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "deployment.cpio")
	if err := Layer(manifests, identityDir, out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range readArchive(t, raw) {
		if strings.Contains(e.name, "kubeconfig") {
			t.Errorf("the layer carries %s", e.name)
		}
	}
}

func TestLayerRefusesAnUnwritableOutput(t *testing.T) {
	identityDir := t.TempDir()
	if err := identity.Mint(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "no-such-dir", "deployment.cpio")
	if err := Layer(fixtureManifests(t), identityDir, out); err == nil {
		t.Error("an unwritable output path was not refused")
	}
}

func TestLayerNeedsACompleteIdentity(t *testing.T) {
	// The identity package's Bundle list is the contract: every file
	// on it must exist, or the layer would install machines that can
	// never join their cluster.
	identityDir := t.TempDir()
	if err := identity.Mint(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(identityDir, "token")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "deployment.cpio")
	if err := Layer(fixtureManifests(t), identityDir, out); err == nil {
		t.Error("an incomplete identity was not refused")
	}
}

// TestLayerRefusesADocumentAMachineWouldRefuse covers the reason the
// layer parses at all. Each of these documents packs and installs
// without complaint if nobody reads it, and then stops every boot from
// the disk afterward. The build is the last moment a person is reading
// output, so this is where the refusal has to happen.
func TestLayerRefusesADocumentAMachineWouldRefuse(t *testing.T) {
	cases := map[string]struct {
		file string
		doc  string
	}{
		"a mirror with no endpoints": {
			file: "cluster.yaml",
			doc: `apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: fixture
spec:
  registries:
    mirrors:
      registry.example: []
`,
		},
		"a cluster document that is not a Cluster": {
			file: "cluster.yaml",
			doc: `apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: fixture
`,
		},
		"a machine manifest with an unknown field": {
			file: "machines/node-1.yaml",
			doc: `apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  storgae: {}
`,
		},
		"a storage role with no device": {
			file: "machines/node-1.yaml",
			doc: `apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  storage:
    clusterState:
      size: 2Gi
`,
		},
		"one port declared twice": {
			file: "machines/node-1.yaml",
			doc: `apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  network:
    interfaces:
      - name: eth1
        address: 10.10.0.1/24
      - name: eth1
        address: 10.10.0.2/24
`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			manifests := fixtureManifests(t)
			if err := os.WriteFile(filepath.Join(manifests, tc.file), []byte(tc.doc), 0o644); err != nil {
				t.Fatal(err)
			}
			identityDir := t.TempDir()
			if err := identity.Mint(identityDir, io.Discard); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "deployment.cpio")
			err := Layer(manifests, identityDir, out)
			if err == nil {
				t.Fatal("the layer packed a document a machine would refuse")
			}
			if !strings.Contains(err.Error(), tc.file) {
				t.Errorf("the error should name %s, got %v", tc.file, err)
			}
			// Nothing is written until everything validates, so a
			// refusal leaves no half-built archive for a later step to
			// pack into an image.
			if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a refused build left %s behind: %v", out, err)
			}
		})
	}
}

func TestLayerCarriesNoModules(t *testing.T) {
	// A declared module is a boot-time load from the system image's
	// whole tree, never layer content. Even a manifest that declares
	// modules yields a layer with no lib/modules entries.
	entries := builtLayer(t, fixtureManifests(t, "veth", "usb_storage"))
	for name := range entries {
		if strings.HasPrefix(name, "lib/modules/") {
			t.Errorf("the layer carries %s", name)
		}
	}
}
