package main

// Tests for the facts: the derivations that are pure over their
// inputs, for example how connections become network status, and the
// boot's write-once publication into the facts tree, aimed at a tempdir
// through the package's seams. Only the real boot ever writes under
// /run.

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

var factsNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// fullConn builds a connection the way DHCP or static assignment
// would build one. k3s_test's conn covers derivations that need only
// an address; these tests need every field filled in.
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

// The facts carry the image collection policy init rendered, so the
// Machine's status says the same thing the console printed and the
// kubelet's configuration file holds.
func TestRuntimeFactsCarryTheImageGCPolicy(t *testing.T) {
	got := runtimeFacts(imageGCCluster(), 1<<30).Kubelet.ImageGC
	want := machine.ImageGCStatus{
		HighThresholdPercent: 70, LowThresholdPercent: 60,
		MinimumAge: "5m", MaximumAge: "168h",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A cluster that names no policy reports none, so an absent field in
// status reads as the kubelet's own default.
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

// The facts carry both log levels, so the Machine's status says the
// same thing the console printed, the k3s drop-in holds, and the
// containerd drop-in gives containerd.
func TestRuntimeFactsCarryTheLogLevels(t *testing.T) {
	got := runtimeFacts(debugCluster(), 1<<30)
	if !got.K3s.Debug {
		t.Error("the k3s debug flag should reach the facts")
	}
	if got.Containerd.LogLevel != "warn" {
		t.Errorf("got containerd level %q, want warn", got.Containerd.LogLevel)
	}
}

// A cluster that names no level reports none, so an absent field in
// status reads as the default that each reader supplies for itself.
func TestRuntimeFactsWithoutLevelsReportNone(t *testing.T) {
	for name, clusterDoc := range map[string]*cluster.Cluster{
		"no cluster": nil,
		"no level":   labCluster(),
	} {
		got := runtimeFacts(clusterDoc, 1<<30)
		if got.K3s.Debug {
			t.Errorf("%s: an unset field is not debug logging", name)
		}
		if got.Containerd != (machine.ContainerdRuntimeStatus{}) {
			t.Errorf("%s: got %+v, want an empty block", name, got.Containerd)
		}
	}
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

// fakeFactsMachine assembles every seam publishBootFacts probes: an
// empty fake sysfs and /dev, an empty fake firmware store, a tempdir
// facts tree and boot manifest, and an xtables probe that names no real
// binary. This leaves the version's xtables unreported, the same as it
// would be on an image without iptables.
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

func TestPublishBootFactsPublishesTheBootStory(t *testing.T) {
	tree, manifestDest := fakeFactsMachine(t)
	raw := []byte("kind: Machine\nmetadata:\n  name: node-1\n")

	// publishBootFacts reads the command line from the kernel rather
	// than taking it as an argument, so the test supplies one. Without
	// this the tree would carry whatever booted the machine running
	// the tests.
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
		t.Fatal(err)
	}
	if facts.Role != api.RoleLeader || facts.Boot.Slot != "A" {
		t.Errorf("the tree carries the boot's identity: %+v", facts)
	}
	// Console parity: init prints this line to the console, so a
	// reader with no console has to find it here.
	if facts.Boot.CommandLine != cmdline {
		t.Errorf("the tree carries the whole command line: %q", facts.Boot.CommandLine)
	}
	if facts.Network.Interface != "eth1" {
		t.Errorf("the network summary names the cluster-facing interface: %+v", facts.Network)
	}
	if facts.Time.State != machine.TimeFreeRunning {
		t.Errorf("no sources and no sync reads as free-running: %+v", facts.Time)
	}
	if facts.Version.Kernel == "" || facts.Hardware.CPUs == 0 || facts.Boot.Time == nil {
		t.Errorf("the machine's own answers are asked, not pinned: %+v", facts)
	}
	// cat parity: role is one file, the API word plus one newline.
	roleFile, err := os.ReadFile(filepath.Join(tree.Dir, "role"))
	if err != nil || string(roleFile) != "leader\n" {
		t.Errorf("the role file reads as one word: %q, %v", roleFile, err)
	}
	published, err := os.ReadFile(manifestDest)
	if err != nil || string(published) != string(raw) {
		t.Errorf("the boot manifest is the choice's exact bytes: %q, %v", published, err)
	}
}

func TestPublishBootFactsSurvivesAnUnwritableTree(t *testing.T) {
	fakeFactsMachine(t)
	sealed := t.TempDir()
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	factsTree = machine.FactsTree{Dir: filepath.Join(sealed, "facts")}

	// A boot must never stop on a facts write. Every write fails against
	// the read-only tree, and the call still returns.
	publishBootFacts(factsTree, bootFacts{
		role:    api.RoleFollower,
		storage: machine.AllRolesInMemory(),
		time:    timeStatus(nil, nil),
	})
	if _, err := factsTree.Read(); err == nil {
		t.Error("the sealed tree has no root to read")
	}
}

func TestPublishBootFactsCarriesLastCrash(t *testing.T) {
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
		t.Fatal(err)
	}
	if facts.LastCrash == nil || facts.LastCrash.Reason != machine.CrashPanic {
		t.Fatalf("the crash stub rides the facts tree: %+v", facts.LastCrash)
	}
	if !facts.LastCrash.Time.Equal(when) || facts.LastCrash.Records != "/sys/fs/pstore" {
		t.Errorf("the stub round-trips whole: %+v", facts.LastCrash)
	}
	// cat parity: the reason is its own file under lastCrash/.
	reason, err := os.ReadFile(filepath.Join(tree.Dir, "lastCrash", "reason"))
	if err != nil || string(reason) != "Panic\n" {
		t.Errorf("the crash reason is one file: %q, %v", reason, err)
	}
}
