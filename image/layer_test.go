package image

// Tests for the deployment layer. The fixtures stand in for the two
// inputs: a manifests directory (a cluster document and machines) and
// a minted identity. These are small fakes with the same shapes as
// the real thing, so the tests need no real deployment.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/identity"
	"github.com/liken-sh/liken/machine"
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

// fixtureWirelessManifests writes a deployment whose one machine
// joins ssid on wlan0 with the given security.
func fixtureWirelessManifests(t *testing.T, ssid string, security machine.WirelessSecurity) string {
	t.Helper()
	dir := fixtureManifests(t)
	m := fmt.Sprintf(`apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  network:
    interfaces:
      - name: wlan0
        wireless:
          ssid: %q
          security: %q
`, ssid, security)
	if err := os.WriteFile(filepath.Join(dir, "machines", "node-1.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fixtureIdentity mints an identity and writes one passphrase file
// for each network in psk. An empty map leaves no psk directory, the
// shape of a deployment with no wifi.
func fixtureIdentity(t *testing.T, psk map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := identity.Mint(dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	for ssid, passphrase := range psk {
		if err := os.MkdirAll(filepath.Join(dir, "psk"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "psk", ssid), []byte(passphrase), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// builtLayer runs Layer over the fixtures and parses the archive.
func builtLayer(t *testing.T, manifests string) map[string]cpioEntry {
	t.Helper()
	return builtLayerFrom(t, manifests, fixtureIdentity(t, nil))
}

// builtLayerFrom runs Layer over one manifests directory and one
// identity directory, and parses the archive back into entries.
func builtLayerFrom(t *testing.T, manifests, identityDir string) map[string]cpioEntry {
	t.Helper()
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
	if err := identity.Kubeconfig(identityDir, "mycluster", "https://127.0.0.1:16443", io.Discard); err != nil {
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

func TestLayerCarriesAPassphrase(t *testing.T) {
	// A passphrase is the same class of secret as the token, so it
	// travels with the identity and lands at the token's mode.
	entries := builtLayerFrom(t,
		fixtureWirelessManifests(t, "stonypoint", machine.WirelessWPAPSK),
		fixtureIdentity(t, map[string]string{"stonypoint": "swordfish1"}))
	e, ok := entries["etc/liken/psk/stonypoint"]
	if !ok {
		t.Fatal("the layer carries no passphrase for stonypoint")
	}
	if string(e.data) != "swordfish1" {
		t.Errorf("passphrase content: %q", e.data)
	}
	if e.mode&0o777 != 0o600 {
		t.Errorf("passphrase mode: %o", e.mode&0o777)
	}
}

// Both ways a manifest asks for wpa-psk are covered here: the word
// itself, and the unset field that SecurityOrDefault resolves to it.
func TestLayerRefusesAWirelessMachineWithNoPassphrase(t *testing.T) {
	cases := map[string]machine.WirelessSecurity{
		"a declared wpa-psk": machine.WirelessWPAPSK,
		"an unset security":  "",
	}
	for name, security := range cases {
		t.Run(name, func(t *testing.T) {
			manifests := fixtureWirelessManifests(t, "stonypoint", security)
			identityDir := fixtureIdentity(t, nil)
			out := filepath.Join(t.TempDir(), "deployment.cpio")
			err := Layer(manifests, identityDir, out)
			if err == nil {
				t.Fatal("the layer packed a wireless machine with no passphrase")
			}
			want := filepath.Join(identityDir, "psk", "stonypoint")
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error should name %s, got %v", want, err)
			}
			// The refusal happens before the output exists, so no
			// half-built archive is left for a later step to pack.
			if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a refused build left %s behind: %v", out, err)
			}
		})
	}
}

// The two deployments that need no psk directory at all: no radio,
// and a radio on an open network.
func TestLayerCarriesNoPassphraseFile(t *testing.T) {
	cases := map[string]string{
		"a deployment with no wireless interface": fixtureManifests(t),
		"an open network":                         fixtureWirelessManifests(t, "stonypoint-guest", machine.WirelessOpen),
	}
	for name, manifests := range cases {
		t.Run(name, func(t *testing.T) {
			for entry := range builtLayerFrom(t, manifests, fixtureIdentity(t, nil)) {
				if strings.HasPrefix(entry, "etc/liken/psk") {
					t.Errorf("the layer carries %s", entry)
				}
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
