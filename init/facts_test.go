package main

// Tests for the facts: the derivations that are pure over their
// inputs (how connections become network status) and the assembly
// and publication of the whole file, aimed at a tempdir through the
// package's path seams. Only the real boot ever writes under /run.

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

var factsNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// fullConn builds a connection the way DHCP or static assignment
// would have: k3s_test's conn covers derivations that only need an
// address; these tests need every field filled in.
func fullConn(t *testing.T, ifname, cidr string, method machine.AddressMethod) *connection {
	t.Helper()
	c := conn(t, ifname, cidr)
	c.method = method
	c.mac = net.HardwareAddr{0x52, 0x54, 0x00, 0x00, 0x00, 0x01}
	c.gateway = net.ParseIP("10.0.2.2")
	c.nameservers = []net.IP{net.ParseIP("10.0.2.3")}
	if method == machine.MethodDHCP {
		c.leaseTime = time.Hour
	}
	return c
}

func TestNetworkFactsWithNoConnections(t *testing.T) {
	status := networkFacts(nil, nil, factsNow)
	if status.Interface != "" || len(status.Interfaces) != 0 {
		t.Errorf("no connections means no facts: %+v", status)
	}
}

func TestNetworkFactsSummarizesTheClusterFacingInterface(t *testing.T) {
	conns := []*connection{
		fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP),
		fullConn(t, "eth1", "10.10.0.2/24", machine.MethodStatic),
	}
	status := networkFacts(labCluster(), conns, factsNow)
	if status.Interface != "eth1" {
		t.Errorf("the nodeCIDR identifies the primary interface, got %s", status.Interface)
	}
	if len(status.Interfaces) != 2 {
		t.Errorf("every interface is reported: %+v", status.Interfaces)
	}
	if status.Addresses[0] != "10.10.0.2/24" {
		t.Errorf("got %v", status.Addresses)
	}
}

func TestNetworkFactsFallsBackToTheFirstConnection(t *testing.T) {
	conns := []*connection{
		fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP),
	}
	status := networkFacts(nil, conns, factsNow)
	if status.Interface != "eth0" {
		t.Errorf("with no cluster the first connection is primary, got %s", status.Interface)
	}
	if status.Gateway != "10.0.2.2" || status.Nameservers[0] != "10.0.2.3" {
		t.Errorf("the summary carries the lease's answers: %+v", status)
	}
}

func TestInterfaceFactsForALease(t *testing.T) {
	got := interfaceFacts(fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP), factsNow)
	if got.Method != machine.MethodDHCP {
		t.Errorf("got %s", got.Method)
	}
	if got.LeaseExpires == nil || !got.LeaseExpires.Equal(factsNow.Add(time.Hour)) {
		t.Errorf("a lease has an expiry: %v", got.LeaseExpires)
	}
}

func TestInterfaceFactsForAStaticAddress(t *testing.T) {
	got := interfaceFacts(fullConn(t, "eth1", "10.10.0.2/24", machine.MethodStatic), factsNow)
	if got.Method != machine.MethodStatic {
		t.Errorf("got %s", got.Method)
	}
	if got.LeaseExpires != nil {
		t.Error("a static address never expires")
	}
}

// fakeFactsMachine assembles every seam publishFacts probes: an empty
// fake sysfs and /dev, an empty fake firmware store, tempdir
// destinations for the facts and boot manifest files, and an xtables
// probe that names no real binary, so the version goes unreported the
// way it would on an image without iptables.
func fakeFactsMachine(t *testing.T) (factsDest, manifestDest string) {
	t.Helper()
	fakeMachine(t)
	fakeFirmwareVars(t, map[string][]byte{})
	dir := t.TempDir()
	oldFacts, oldManifest, oldProbe := factsPath, bootManifestPath, xtablesProbe
	factsPath = filepath.Join(dir, "facts.yaml")
	bootManifestPath = filepath.Join(dir, "machine.yaml")
	xtablesProbe = filepath.Join(dir, "no-such-iptables")
	t.Cleanup(func() { factsPath, bootManifestPath, xtablesProbe = oldFacts, oldManifest, oldProbe })
	return factsPath, bootManifestPath
}

func TestPublishFactsPublishesTheBootStory(t *testing.T) {
	factsDest, manifestDest := fakeFactsMachine(t)
	raw := []byte("kind: Machine\nmetadata:\n  name: node-1\n")

	owner := publishFacts(factsInputs{
		cluster:    labCluster(),
		role:       machine.RoleLeader,
		choice:     &manifestChoice{raw: raw},
		conns:      []*connection{fullConn(t, "eth1", "10.10.0.2/24", machine.MethodStatic)},
		storage:    machine.AllRolesInMemory(),
		boot:       machine.BootStatus{Slot: "A"},
		modules:    []machine.ModuleStatus{{Name: "overlay", State: machine.ModuleLoaded}},
		registries: machine.RegistriesStatus{Embedded: true},
	})

	facts, err := machine.ReadFacts(factsDest)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Role != machine.RoleLeader || facts.Boot.Slot != "A" {
		t.Errorf("the file carries the boot's identity: %+v", facts)
	}
	if facts.Network.Interface != "eth1" {
		t.Errorf("the network summary names the cluster-facing interface: %+v", facts.Network)
	}
	if facts.Time.State != machine.TimeFreeRunning {
		t.Errorf("no sources and no sync reads as free-running: %+v", facts.Time)
	}
	if facts.Version.Kernel == "" || facts.Hardware.CPUs == 0 || facts.BootedAt == nil {
		t.Errorf("the machine's own answers are asked, not pinned: %+v", facts)
	}
	published, err := os.ReadFile(manifestDest)
	if err != nil || string(published) != string(raw) {
		t.Errorf("the boot manifest is the choice's exact bytes: %q, %v", published, err)
	}
	if owner.status.Role != machine.RoleLeader {
		t.Errorf("the returned owner wraps the same facts: %+v", owner.status)
	}
}

func TestPublishFactsSurvivesAnUnwritableDestination(t *testing.T) {
	fakeFactsMachine(t)
	sealed := t.TempDir()
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	factsPath = filepath.Join(sealed, "run", "facts.yaml")

	// The facts still exist in memory for the boot's later writers,
	// even when the founding write never landed.
	owner := publishFacts(factsInputs{
		role:    machine.RoleFollower,
		choice:  &manifestChoice{},
		storage: machine.AllRolesInMemory(),
	})
	if owner == nil || owner.status.Role != machine.RoleFollower {
		t.Errorf("a failed write still returns the guarded owner: %+v", owner)
	}
}

func TestFactsFileMutateRidesTheNextPublish(t *testing.T) {
	factsDest, _ := fakeFactsMachine(t)
	owner := &factsFile{status: &machine.MachineStatus{}}

	// mutate edits only the memory: nothing lands on disk yet.
	owner.mutate(func(s *machine.MachineStatus) { s.Role = machine.RoleFollower })
	if _, err := os.Stat(factsDest); !os.IsNotExist(err) {
		t.Errorf("mutate must not write the file: %v", err)
	}

	// The next publish carries the earlier edit along with its own.
	owner.publish(func(s *machine.MachineStatus) { s.Boot.Restarts++ })
	facts, err := machine.ReadFacts(factsDest)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Role != machine.RoleFollower || facts.Boot.Restarts != 1 {
		t.Errorf("both edits publish together: %+v", facts)
	}
}
