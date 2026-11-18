package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

var factsNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// fullConn builds a connection with every field that DHCP or a static
// address sets. conn in k3s_test.go sets only the address.
func fullConn(t *testing.T, ifname, cidr string, method machine.AddressMethod) *connection {
	t.Helper()
	c := conn(t, ifname, cidr)
	c.method = method
	c.mac = net.HardwareAddr{0x52, 0x54, 0x00, 0x00, 0x00, 0x01}
	c.gateway = net.ParseIP("10.0.2.2")
	c.nameservers = []net.IP{net.ParseIP("10.0.2.3")}
	if method == machine.MethodDHCP {
		c.leaseTime = time.Hour
		c.leaseExpires = factsNow.Add(time.Hour)
	}
	return c
}

func TestRuntimeFactsReportTheImageGCPolicy(t *testing.T) {
	got := runtimeFacts(imageGCCluster(), 1<<30).Kubelet.ImageGC
	want := machine.ImageGCStatus{
		HighThresholdPercent: 70, LowThresholdPercent: 60,
		MinimumAge: "5m", MaximumAge: "168h",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// An absent status field means the kubelet's default applies.
func TestRuntimeFactsWithoutAPolicyReportNone(t *testing.T) {
	for name, clusterDoc := range map[string]*cluster.Cluster{
		"no cluster": nil,
		"no policy":  labCluster(),
	} {
		if got := runtimeFacts(clusterDoc, 1<<30).Kubelet; got != (machine.KubeletRuntimeStatus{}) {
			t.Errorf("%s: got %+v, want an empty block", name, got)
		}
	}
}

func TestRuntimeFactsReportTheLogLevels(t *testing.T) {
	got := runtimeFacts(debugCluster(), 1<<30)
	if !got.K3s.Debug {
		t.Error("K3s.Debug is false, want the cluster's debug flag, true")
	}
	if got.Containerd.LogLevel != "warn" {
		t.Errorf("got containerd level %q, want warn", got.Containerd.LogLevel)
	}
}

// An absent status field means each program's own default applies.
func TestRuntimeFactsWithoutLevelsReportNone(t *testing.T) {
	for name, clusterDoc := range map[string]*cluster.Cluster{
		"no cluster": nil,
		"no level":   labCluster(),
	} {
		got := runtimeFacts(clusterDoc, 1<<30)
		if got.K3s.Debug {
			t.Errorf("%s: K3s.Debug is true, want false when the field is unset", name)
		}
		if got.Containerd != (machine.ContainerdRuntimeStatus{}) {
			t.Errorf("%s: got %+v, want an empty block", name, got.Containerd)
		}
	}
}

func TestNetworkFactsWithNoConnections(t *testing.T) {
	status := networkFacts(nil, nil)
	if status.Interface != "" || len(status.Interfaces) != 0 {
		t.Errorf("the status is %+v, want empty with no connections", status)
	}
}

func TestNetworkFactsSummarizesTheClusterFacingInterface(t *testing.T) {
	conns := []*connection{
		fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP),
		fullConn(t, "eth1", "10.10.0.2/24", machine.MethodStatic),
	}
	status := networkFacts(labCluster(), conns)
	if status.Interface != "eth1" {
		t.Errorf("the primary interface is %s, want eth1, whose address is in the nodeCIDR", status.Interface)
	}
	if len(status.Interfaces) != 2 {
		t.Errorf("the interfaces are %+v, want both connections", status.Interfaces)
	}
	if status.Addresses[0] != "10.10.0.2/24" {
		t.Errorf("the addresses are %v, want 10.10.0.2/24 first", status.Addresses)
	}
}

func TestNetworkFactsFallsBackToTheFirstConnection(t *testing.T) {
	conns := []*connection{
		fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP),
	}
	status := networkFacts(nil, conns)
	if status.Interface != "eth0" {
		t.Errorf("the primary interface is %s, want the first connection, eth0, with no cluster", status.Interface)
	}
	if status.Gateway != "10.0.2.2" || status.Nameservers[0] != "10.0.2.3" {
		t.Errorf("the summary is %+v, want the lease's gateway and nameserver", status)
	}
}

func TestInterfaceFactsForALease(t *testing.T) {
	got := interfaceFacts(fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP))
	if got.Method != machine.MethodDHCP {
		t.Errorf("the method is %s, want %s", got.Method, machine.MethodDHCP)
	}
	if got.LeaseExpires == nil || !got.LeaseExpires.Equal(factsNow.Add(time.Hour)) {
		t.Errorf("the lease expires at %v, want an hour after the ACK", got.LeaseExpires)
	}
}

// The radio watch rewrites the whole network subtree. If the expiry
// came from the clock, a machine with a radio would report a wired
// lease that never runs out.
func TestALeaseExpiryDoesNotMoveWithTheClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conns := []*connection{fullConn(t, "eth0", "10.0.2.15/24", machine.MethodDHCP)}

		first := networkFacts(nil, conns)
		time.Sleep(time.Hour)
		second := networkFacts(nil, conns)

		if first.LeaseExpires == nil || second.LeaseExpires == nil {
			t.Fatalf("the lease expiries are %v and %v, want both set", first.LeaseExpires, second.LeaseExpires)
		}
		if !first.LeaseExpires.Equal(*second.LeaseExpires) {
			t.Errorf("the expiry is %v on the first call and %v on the second, want the same on both", first.LeaseExpires, second.LeaseExpires)
		}
		if !first.LeaseExpires.Equal(factsNow.Add(time.Hour)) {
			t.Errorf("the expiry is %v, want the one that the ACK set, an hour after it", first.LeaseExpires)
		}
	})
}

func TestInterfaceFactsForAStaticAddress(t *testing.T) {
	got := interfaceFacts(fullConn(t, "eth1", "10.10.0.2/24", machine.MethodStatic))
	if got.Method != machine.MethodStatic {
		t.Errorf("the method is %s, want %s", got.Method, machine.MethodStatic)
	}
	if got.LeaseExpires != nil {
		t.Errorf("the lease expires at %v, want no expiry for a static address", got.LeaseExpires)
	}
}

// fakeFactsMachine replaces everything that publishBootFacts reads or
// writes: sysfs, /dev, the EFI variables, the facts tree, the boot
// manifest, and iptables, which does not exist, so Xtables stays empty.
func fakeFactsMachine(t *testing.T) (tree machine.FactsTree, manifestDest string) {
	t.Helper()
	fakeMachine(t)
	fakeFirmwareVars(t, map[string][]byte{})
	dir := t.TempDir()
	oldTree, oldManifest, oldProbe := factsTree, bootManifestPath, xtablesProbe
	factsTree = machine.FactsTree{Dir: filepath.Join(dir, "facts")}
	bootManifestPath = filepath.Join(dir, "machine.yaml")
	xtablesProbe = filepath.Join(dir, "no-such-iptables")
	t.Cleanup(func() { factsTree, bootManifestPath, xtablesProbe = oldTree, oldManifest, oldProbe })
	return factsTree, bootManifestPath
}

func TestPublishBootFactsWritesTheBootFacts(t *testing.T) {
	tree, manifestDest := fakeFactsMachine(t)
	raw := []byte("kind: Machine\nmetadata:\n  name: node-1\n")

	// publishBootFacts reads the kernel command line itself.
	const cmdline = "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A panic=10"
	fakeCmdline(t, cmdline+"\n")

	publishBootFacts(tree, bootFacts{
		clusterDoc: labCluster(),
		role:       api.RoleLeader,
		conns:      []*connection{fullConn(t, "eth1", "10.10.0.2/24", machine.MethodStatic)},
		storage:    machine.AllRolesInMemory(),
		boot:       machine.BootStatus{Slot: "A"},
		modules:    []machine.ModuleStatus{{Name: "overlay", State: machine.ModuleLoaded}},
		registries: machine.RegistriesStatus{Embedded: true},
		time:       timeStatus(nil, nil),
	})
	publishBootManifest(&manifestChoice{raw: raw})

	facts, err := tree.Read()
	if err != nil {
		t.Fatalf("reading the facts tree: %v", err)
	}
	if facts.Role != api.RoleLeader || facts.Boot.Slot != "A" {
		t.Errorf("the facts are %+v, want role leader on slot A", facts)
	}
	if facts.Boot.CommandLine != cmdline {
		t.Errorf("the command line in the tree is %q, want the whole command line", facts.Boot.CommandLine)
	}
	if facts.Network.Interface != "eth1" {
		t.Errorf("the network summary is %+v, want eth1, the interface that faces the cluster", facts.Network)
	}
	if facts.Time.State != machine.TimeFreeRunning {
		t.Errorf("the time status is %+v, want FreeRunning with no sources and no sync", facts.Time)
	}
	if facts.Version.Kernel == "" || facts.Hardware.CPUs == 0 || facts.Boot.Time == nil {
		t.Errorf("the facts are %+v, want the kernel, the CPU count, and the boot time read from the machine", facts)
	}
	// A scalar fact is its value and a newline, so cat prints it.
	roleFile, err := os.ReadFile(filepath.Join(tree.Dir, "role"))
	if err != nil || string(roleFile) != "leader\n" {
		t.Errorf("the role file is %q with error %v, want \"leader\\n\"", roleFile, err)
	}
	published, err := os.ReadFile(manifestDest)
	if err != nil || string(published) != string(raw) {
		t.Errorf("the boot manifest is %q with error %v, want the choice's exact bytes", published, err)
	}
}

func TestPublishBootFactsSurvivesAnUnwritableTree(t *testing.T) {
	fakeFactsMachine(t)
	sealed := t.TempDir()
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	factsTree = machine.FactsTree{Dir: filepath.Join(sealed, "facts")}

	// Every write fails, and publishBootFacts must still return.
	publishBootFacts(factsTree, bootFacts{
		role:    api.RoleFollower,
		storage: machine.AllRolesInMemory(),
		time:    timeStatus(nil, nil),
	})
	if _, err := factsTree.Read(); err == nil {
		t.Error("factsTree.Read returned no error on the sealed tree, want one, because the tree has no root")
	}
}

func TestPublishBootFactsIncludesTheLastCrash(t *testing.T) {
	tree, _ := fakeFactsMachine(t)
	when := time.Date(2026, 7, 23, 4, 12, 9, 0, time.UTC)

	publishBootFacts(tree, bootFacts{
		storage: machine.AllRolesInMemory(),
		time:    timeStatus(nil, nil),
		lastCrash: &machine.CrashStatus{
			Time:    &when,
			Reason:  machine.CrashPanic,
			Message: "Kernel panic - not syncing: sysrq triggered crash",
			Records: "/sys/fs/pstore",
		},
	})

	facts, err := tree.Read()
	if err != nil {
		t.Fatalf("reading the facts tree: %v", err)
	}
	if facts.LastCrash == nil || facts.LastCrash.Reason != machine.CrashPanic {
		t.Fatalf("the crash record is %+v, want a Panic record in the facts tree", facts.LastCrash)
	}
	if !facts.LastCrash.Time.Equal(when) || facts.LastCrash.Records != "/sys/fs/pstore" {
		t.Errorf("the crash record is %+v, want its time and records path read back", facts.LastCrash)
	}
	reason, err := os.ReadFile(filepath.Join(tree.Dir, "lastCrash", "reason"))
	if err != nil || string(reason) != "Panic\n" {
		t.Errorf("lastCrash/reason is %q with error %v, want \"Panic\\n\"", reason, err)
	}
}
