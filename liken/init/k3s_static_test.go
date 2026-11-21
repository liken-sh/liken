package main

// Tests for the static k3s configuration that the image ships. A key
// that is the same on every machine of a role is in these files, and
// init never writes them, so the tests read them from the repository.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// staticK3sConfigDir is where the repository keeps the files that the
// image has at /etc/rancher/k3s.
const staticK3sConfigDir = "../image/etc/rancher/k3s"

// staticK3sConfigHasLine reports whether a static k3s configuration
// file has the line. A comment that mentions a key starts with "#", so
// it does not match.
func staticK3sConfigHasLine(t *testing.T, name, line string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(staticK3sConfigDir, name))
	if err != nil {
		t.Fatalf("reading the static k3s configuration %s: %v", name, err)
	}
	return strings.Contains("\n"+string(raw), "\n"+line+"\n")
}

func TestTheLeaderConfigExposesEtcdMetrics(t *testing.T) {
	if !staticK3sConfigHasLine(t, "config.yaml", "etcd-expose-metrics: true") {
		t.Errorf("config.yaml has no etcd-expose-metrics: true, want it so that a leader's etcd serves its metrics on the node IP")
	}
}

func TestTheFollowerConfigNamesNoEtcdMetricsKey(t *testing.T) {
	if staticK3sConfigHasLine(t, "agent.yaml", "etcd-expose-metrics: true") {
		t.Errorf("agent.yaml has etcd-expose-metrics: true, want it absent, because a k3s agent refuses a server key and does not start")
	}
}
