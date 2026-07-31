package main

// These tests cover resolveEndpoint: the choice between the -server
// flag and the deployment's cluster.yaml. writeKubeconfig builds on
// the same resolution, so its own tests live beside the identity
// package's kubeconfig tests and the dispatcher tests in
// main_test.go.

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/identity"
	"golang.org/x/sys/unix"
)

func TestResolveEndpointPrefersTheServerFlag(t *testing.T) {
	got, err := resolveEndpoint(t.TempDir(), "https://127.0.0.1:16443")
	if err != nil || got != "https://127.0.0.1:16443" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveEndpointReadsClusterYAML(t *testing.T) {
	dir := t.TempDir()
	doc := `apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: mycluster
spec:
  endpoint: https://10.10.0.1:6443
  leaders: [alpha]
`
	if err := os.WriteFile(filepath.Join(dir, "cluster.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveEndpoint(dir, "")
	if err != nil || got != "https://10.10.0.1:6443" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveEndpointRefusesAnEndpointlessDeployment(t *testing.T) {
	dir := t.TempDir()
	doc := `apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: mycluster
spec:
  leaders: [alpha]
`
	if err := os.WriteFile(filepath.Join(dir, "cluster.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveEndpoint(dir, ""); err == nil {
		t.Fatal("an absent endpoint with no -server must be an error")
	}
}

func TestResolveEndpointRefusesAMissingClusterYAML(t *testing.T) {
	if _, err := resolveEndpoint(t.TempDir(), ""); err == nil {
		t.Fatal("no cluster.yaml and no -server must be an error, not a panic")
	}
}

func TestEnvWithReplacesAnExistingVariable(t *testing.T) {
	got := envWith([]string{"HOME=/home/x", "KUBECONFIG=/old", "TERM=xterm"}, "KUBECONFIG", "/new")
	want := []string{"HOME=/home/x", "TERM=xterm", "KUBECONFIG=/new"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestEnvWithAppendsAMissingVariable(t *testing.T) {
	got := envWith([]string{"HOME=/home/x"}, "KUBECONFIG", "/new")
	want := []string{"HOME=/home/x", "KUBECONFIG=/new"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

// deploymentDirForTest lays out the directory the cluster commands
// take: a cluster.yaml with an endpoint, and a minted identity.
func deploymentDirForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	doc := `apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: mycluster
spec:
  endpoint: https://10.10.0.1:6443
  leaders: [alpha]
`
	if err := os.WriteFile(filepath.Join(dir, "cluster.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := identity.Mint(filepath.Join(dir, "identity"), io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPassthroughExecsTheToolWithTheKubeconfig(t *testing.T) {
	dir := deploymentDirForTest(t)
	var gotArgv, gotEnv []string
	execTool = func(path string, argv []string, env []string) error {
		gotArgv, gotEnv = argv, env
		return nil
	}
	defer func() { execTool = unix.Exec }()

	if err := passthrough("sh", []string{dir, "-c", "true"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gotArgv, []string{"sh", "-c", "true"}) {
		t.Fatalf("argv: %v", gotArgv)
	}
	want := "KUBECONFIG=" + filepath.Join(dir, "identity", "kubeconfig")
	if !slices.Contains(gotEnv, want) {
		t.Fatalf("env misses %s", want)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "identity", "kubeconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "server: https://10.10.0.1:6443") {
		t.Fatal("the kubeconfig must carry the resolved endpoint")
	}
}

func TestPassthroughNamesAMissingTool(t *testing.T) {
	dir := deploymentDirForTest(t)
	err := passthrough("no-such-tool-anywhere", []string{dir})
	if err == nil || !strings.Contains(err.Error(), "no-such-tool-anywhere") {
		t.Fatalf("a missing tool must be a plain message naming it, got %v", err)
	}
}
