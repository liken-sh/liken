package main

// These tests cover resolveEndpoint: the choice between the -server
// flag and the deployment's cluster.yaml. writeKubeconfig builds on
// the same resolution, so its own tests live beside the identity
// package's kubeconfig tests and the dispatcher tests in
// main_test.go.

import (
	"os"
	"path/filepath"
	"testing"
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
