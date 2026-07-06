package main

// Tests for the clock discipline's decisions: where a machine gets
// its time, what it reports about its clock, and how much slewing it
// will ask of the kernel at once. The syscalls that act on those
// decisions (clock_settime, adjtimex) are PID-1 territory and belong
// to the QEMU harness.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

func clusterWithTime(upstreams []string, endpoint string, servers ...string) *machine.Cluster {
	if servers == nil {
		servers = []string{"node-1"}
	}
	return &machine.Cluster{
		Spec: machine.ClusterSpec{
			Servers:  servers,
			Endpoint: endpoint,
			Network:  machine.ClusterNetworkSpec{NodeCIDR: "10.10.0.0/24"},
			Time:     machine.ClusterTimeSpec{Upstreams: upstreams},
		},
	}
}

// manifestsDir builds an image-style machines directory: one Machine
// manifest per name, each declaring a static address on the node
// network.
func manifestsDir(t *testing.T, addresses map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, address := range addresses {
		doc := `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: ` + name + `
spec:
  network:
    interfaces:
      - name: eth1
        address: ` + address + `
`
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTimeSourcesServerAsksTheUpstreams(t *testing.T) {
	c := clusterWithTime([]string{"time.cloudflare.com", "192.168.1.1"}, "https://10.10.0.1:6443")
	sources := timeSources(c, machine.RoleServer, t.TempDir())
	if len(sources) != 2 || sources[0] != "time.cloudflare.com" || sources[1] != "192.168.1.1" {
		t.Errorf("got %v", sources)
	}
}

func TestTimeSourcesServerWithoutUpstreamsFreeRuns(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	if sources := timeSources(c, machine.RoleServer, t.TempDir()); sources != nil {
		t.Errorf("expected free-running, got %v", sources)
	}
}

func TestTimeSourcesNilClusterFreeRuns(t *testing.T) {
	if sources := timeSources(nil, machine.RoleServer, t.TempDir()); sources != nil {
		t.Errorf("expected free-running, got %v", sources)
	}
}

func TestTimeSourcesAgentAsksEveryServer(t *testing.T) {
	c := clusterWithTime(nil, "https://cluster.example.com:6443", "node-1", "node-3")
	dir := manifestsDir(t, map[string]string{
		"node-1": "10.10.0.1/24",
		"node-2": "10.10.0.2/24", // an agent: present in the image, not a source
		"node-3": "10.10.0.3/24",
	})
	sources := timeSources(c, machine.RoleAgent, dir)
	want := []string{"10.10.0.1", "10.10.0.3", "cluster.example.com"}
	if !slices.Equal(sources, want) {
		t.Errorf("got %v, want %v", sources, want)
	}
}

func TestTimeSourcesAgentDoesNotAskTheEndpointTwice(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	dir := manifestsDir(t, map[string]string{"node-1": "10.10.0.1/24"})
	sources := timeSources(c, machine.RoleAgent, dir)
	if !slices.Equal(sources, []string{"10.10.0.1"}) {
		t.Errorf("got %v", sources)
	}
}

func TestTimeSourcesAgentFallsBackToTheEndpoint(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	sources := timeSources(c, machine.RoleAgent, t.TempDir())
	if !slices.Equal(sources, []string{"10.10.0.1"}) {
		t.Errorf("got %v", sources)
	}
}

func TestTimeStatusAfterASync(t *testing.T) {
	at := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	sync := &timeSync{
		source:  "time.cloudflare.com",
		stratum: 2,
		offset:  1280 * time.Microsecond,
		at:      at,
	}
	status := timeStatus(sync, []string{"time.cloudflare.com"})
	if !status.Synchronized {
		t.Error("a fresh sync should report synchronized")
	}
	if status.Source != "time.cloudflare.com" {
		t.Errorf("source: got %q", status.Source)
	}
	if status.Stratum != 3 {
		t.Errorf("a stratum-2 source makes this machine stratum 3, got %d", status.Stratum)
	}
	if status.Offset != "1.28ms" {
		t.Errorf("offset: got %q", status.Offset)
	}
	if status.LastSync == nil || !status.LastSync.Equal(at) {
		t.Errorf("lastSync: got %v", status.LastSync)
	}
}

func TestTimeStatusFreeRunning(t *testing.T) {
	status := timeStatus(nil, nil)
	if status.Synchronized {
		t.Error("free-running must not claim synchronized")
	}
	if status.Stratum != 10 {
		t.Errorf("free-running reports the local-clock convention, got %d", status.Stratum)
	}
}

func TestTimeStatusNeverSynced(t *testing.T) {
	status := timeStatus(nil, []string{"10.10.0.1"})
	if status.Synchronized {
		t.Error("no sync yet must not claim synchronized")
	}
	if status.Stratum != 16 {
		t.Errorf("unsynchronized reports stratum 16, got %d", status.Stratum)
	}
}

func TestSlewAmountPassesSmallOffsetsThrough(t *testing.T) {
	if got := slewAmount(3 * time.Millisecond); got != 3*time.Millisecond {
		t.Errorf("got %v", got)
	}
	if got := slewAmount(-3 * time.Millisecond); got != -3*time.Millisecond {
		t.Errorf("got %v", got)
	}
}

func TestSlewAmountClampsLargeOffsets(t *testing.T) {
	if got := slewAmount(3 * time.Second); got != 500*time.Millisecond {
		t.Errorf("got %v", got)
	}
	if got := slewAmount(-3 * time.Second); got != -500*time.Millisecond {
		t.Errorf("got %v", got)
	}
}
