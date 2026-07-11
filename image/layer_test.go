package image

// Tests for the deployment layer. The fixtures stand in for the three
// inputs: a manifests directory (a cluster document and machines), a
// minted identity, and a kernel dist tree with a depmod index — small
// fakes with the same shapes, so the tests need no vendored kernel
// and no real deployment.

import (
	"io"
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

// fixtureKernel builds a fake kernel dist: a release name, a module
// tree with a couple of modules, and the depmod index files the
// build would have vendored.
func fixtureKernel(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	release := "9.9.9-test"
	if err := os.WriteFile(filepath.Join(dir, "release"), []byte(release+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modules := filepath.Join(dir, "lib", "modules", release)
	if err := os.MkdirAll(filepath.Join(modules, "kernel", "drivers", "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ko := range []string{"kernel/drivers/net/dummy.ko.zst", "kernel/drivers/net/veth.ko.zst"} {
		if err := os.WriteFile(filepath.Join(modules, ko), []byte("elf bytes of "+ko), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// dummy stands alone; veth depends on dummy (not true of the real
	// veth, but the point is the closure).
	dep := "kernel/drivers/net/dummy.ko.zst:\n" +
		"kernel/drivers/net/veth.ko.zst: kernel/drivers/net/dummy.ko.zst\n"
	if err := os.WriteFile(filepath.Join(modules, "modules.dep"), []byte(dep), 0o644); err != nil {
		t.Fatal(err)
	}
	builtin := "kernel/fs/binfmt_misc.ko\n"
	if err := os.WriteFile(filepath.Join(modules, "modules.builtin"), []byte(builtin), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"modules.builtin.modinfo", "modules.order"} {
		if err := os.WriteFile(filepath.Join(modules, f), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if err := Layer(manifests, identityDir, fixtureKernel(t), out, io.Discard); err != nil {
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
	// The operator's credential lives beside the identity but is not
	// part of it: a machine image carrying the admin certificate
	// would hand cluster-admin to anyone who reads the disk.
	manifests := fixtureManifests(t)
	identityDir := t.TempDir()
	if err := identity.Mint(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := identity.Kubeconfig(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "deployment.cpio")
	if err := Layer(manifests, identityDir, fixtureKernel(t), out, io.Discard); err != nil {
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

func TestLayerShipsDeclaredModulesWithTheirClosure(t *testing.T) {
	entries := builtLayer(t, fixtureManifests(t, "veth"))
	// veth pulls dummy via modules.dep, and shipping any module means
	// shipping the full index the composed image will resolve with.
	for _, want := range []string{
		"lib/modules/9.9.9-test/kernel/drivers/net/veth.ko.zst",
		"lib/modules/9.9.9-test/kernel/drivers/net/dummy.ko.zst",
		"lib/modules/9.9.9-test/modules.dep",
		"lib/modules/9.9.9-test/modules.builtin",
	} {
		if _, ok := entries[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
}

func TestLayerSkipsModulesForAModulelessDeployment(t *testing.T) {
	entries := builtLayer(t, fixtureManifests(t))
	for name := range entries {
		if strings.HasPrefix(name, "lib/modules/") {
			t.Errorf("moduleless deployment shipped %s", name)
		}
	}
}

func TestLayerAcceptsABuiltinModule(t *testing.T) {
	// A declared name the kernel carries built in needs no file; the
	// layer must not fail the build over it.
	entries := builtLayer(t, fixtureManifests(t, "binfmt_misc"))
	for name := range entries {
		if strings.HasSuffix(name, ".ko.zst") {
			t.Errorf("builtin declaration shipped %s", name)
		}
	}
}

func TestLayerRefusesAnUnknownModule(t *testing.T) {
	manifests := fixtureManifests(t, "no-such-driver")
	identityDir := t.TempDir()
	if err := identity.Mint(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "deployment.cpio")

	err := Layer(manifests, identityDir, fixtureKernel(t), out, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "no-such-driver") {
		t.Errorf("unknown module was not refused: %v", err)
	}
}
