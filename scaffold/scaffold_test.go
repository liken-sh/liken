package scaffold

// These tests run the scaffold with scripted answers as input, and
// check the deployment directory it produces for parseable output.
// The answers fixture types one line per question, so each test
// reads like the interview it runs.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// scaffolded runs New against a fresh directory with the given
// scripted answers, returning the directory.
func scaffolded(t *testing.T, answers ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mycluster")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader(strings.Join(answers, "\n") + "\n")
	if err := New(dir, in, io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir
}

// parsedCluster reads the generated cluster.yaml back through the
// machine package's strict parser.
func parsedCluster(t *testing.T, dir string) *cluster.Cluster {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "cluster.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := cluster.ParseCluster(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// parsedMachine reads one generated machine manifest back.
func parsedMachine(t *testing.T, dir, name string) *machine.Machine {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "machines", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := machine.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// defaultAnswers accepts every default. It produces a one-machine
// cluster on one disk with two NICs.
func defaultAnswers() []string {
	return []string{
		"", // cluster name: the directory's
		"", // machines: machine-1
		"", // leaders: machine-1
		"", // subnet: 10.10.0.0/24
		"", // machine-1's address: 10.10.0.1
		"", // uplink: eth0
		"", // cluster NIC: eth1
		"", // disks: /dev/sda
		"", // NTP: the suggested pair
		"", // reboot policy: Manual
		"", // features: none
		"", // releases source: decide later
	}
}

func TestScaffoldsASingleMachineOnDefaults(t *testing.T) {
	dir := scaffolded(t, defaultAnswers()...)

	c := parsedCluster(t, dir)
	if c.Metadata.Name != "mycluster" {
		t.Errorf("cluster name defaults to the directory: %q", c.Metadata.Name)
	}
	if len(c.Spec.Leaders) != 1 || c.Spec.Leaders[0] != "machine-1" {
		t.Errorf("leaders: %v", c.Spec.Leaders)
	}
	if c.Spec.Endpoint != "https://10.10.0.1:6443" {
		t.Errorf("the endpoint derives from the founding leader: %q", c.Spec.Endpoint)
	}
	if c.Spec.Network.NodeCIDR != "10.10.0.0/24" {
		t.Errorf("nodeCIDR: %q", c.Spec.Network.NodeCIDR)
	}

	m := parsedMachine(t, dir, "machine-1")
	if m.Metadata.Name != "machine-1" {
		t.Errorf("machine name: %q", m.Metadata.Name)
	}
	if len(m.Spec.Network.Interfaces) != 2 || m.Spec.Network.Interfaces[1].Address != "10.10.0.1/24" {
		t.Errorf("two NICs with the fixed address on the second: %+v", m.Spec.Network.Interfaces)
	}
	if err := m.Spec.Storage.Validate(); err != nil {
		t.Errorf("the single-disk layout must validate: %v", err)
	}
	if len(m.Spec.Storage.Roles()) != 7 {
		t.Errorf("all seven roles on one disk: %d", len(m.Spec.Storage.Roles()))
	}
}

func TestScaffoldsAThreeLeaderFleet(t *testing.T) {
	dir := scaffolded(t,
		"lab",                   // cluster name
		"big little tiny spare", // machines
		"",                      // leaders: default first three
		"10.20.0.0/24",          // subnet
		"", "", "", "",          // addresses: defaults .1-.4
		"",                             // uplink
		"",                             // cluster NIC
		"/dev/sda /dev/sdb /dev/sdc",   // three disks
		"",                             // NTP
		"Auto",                         // reboot policy
		"metrics-server nfs",           // features
		"https://example.com/releases", // source
	)

	c := parsedCluster(t, dir)
	if len(c.Spec.Leaders) != 3 || c.Spec.Leaders[0] != "big" {
		t.Errorf("three leaders, big founding: %v", c.Spec.Leaders)
	}
	if c.Spec.Endpoint != "https://10.20.0.1:6443" {
		t.Errorf("endpoint: %q", c.Spec.Endpoint)
	}
	if _, ok := c.Spec.Features["metrics-server"]; !ok {
		t.Errorf("features: %v", c.Spec.Features)
	}
	if c.Spec.Releases.Source != "https://example.com/releases" {
		t.Errorf("source: %q", c.Spec.Releases.Source)
	}

	for i, name := range []string{"big", "little", "tiny", "spare"} {
		m := parsedMachine(t, dir, name)
		want := "10.20.0." + string(rune('1'+i)) + "/24"
		if m.Spec.Network.Interfaces[1].Address != want {
			t.Errorf("%s's address: %q, want %q", name, m.Spec.Network.Interfaces[1].Address, want)
		}
		if m.Spec.RebootPolicy != "Auto" {
			t.Errorf("%s's rebootPolicy: %q", name, m.Spec.RebootPolicy)
		}
		if err := m.Spec.Storage.Validate(); err != nil {
			t.Errorf("%s's three-disk layout must validate: %v", name, err)
		}
	}
}

func TestScaffoldsASingleNICMachine(t *testing.T) {
	dir := scaffolded(t,
		"",            // cluster name
		"solo",        // machines
		"",            // leaders
		"",            // subnet
		"",            // address
		"none",        // uplink: machines have one interface
		"enp1s0",      // cluster NIC
		"10.10.0.254", // gateway
		"",            // nameservers: defaults
		"",            // disks
		"",            // NTP
		"",            // reboot policy
		"",            // features
		"",            // source
	)

	m := parsedMachine(t, dir, "solo")
	if len(m.Spec.Network.Interfaces) != 1 {
		t.Fatalf("one interface: %+v", m.Spec.Network.Interfaces)
	}
	iface := m.Spec.Network.Interfaces[0]
	if iface.Name != "enp1s0" || iface.Gateway != "10.10.0.254" || len(iface.Nameservers) != 2 {
		t.Errorf("a single NIC needs its route and resolvers spelled out: %+v", iface)
	}
}

func TestScaffoldReasksUntilAnswersHold(t *testing.T) {
	// This sends wrong answers before right ones: an even leader
	// count, an address outside the subnet, a two-disk answer, and a
	// misspelled policy. Each wrong answer causes a re-ask, not a
	// failure.
	dir := scaffolded(t,
		"",            // cluster name
		"a b c",       // machines
		"a b",         // leaders: even, re-asked
		"a",           // leaders again
		"",            // subnet
		"192.168.9.9", // a's address: outside, re-asked
		"",            // a's address again
		"", "",        // b's and c's addresses
		"",                  // uplink
		"",                  // cluster NIC
		"/dev/sda /dev/sdb", // two disks, re-asked
		"/dev/sda",          // one disk
		"",                  // NTP
		"manual",            // lowercase, re-asked
		"Manual",            // policy
		"",                  // features
		"",                  // source
	)
	if len(parsedCluster(t, dir).Spec.Leaders) != 1 {
		t.Error("the re-asked answers should have landed")
	}
}

func TestScaffoldRefusesAnExistingDeployment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cluster.yaml"), []byte("kind: Cluster"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := New(dir, strings.NewReader(""), io.Discard); err == nil {
		t.Error("scaffolding must not overwrite an existing deployment")
	}
}

func TestScaffoldRefusesAnUnknownFeature(t *testing.T) {
	answers := defaultAnswers()
	answers[10] = "warp-drive"
	dir := filepath.Join(t.TempDir(), "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader(strings.Join(answers, "\n") + "\n")
	if err := New(dir, in, io.Discard); err == nil {
		t.Error("a feature outside the vocabulary must be refused")
	}
}

func TestScaffoldWritesTheGitignore(t *testing.T) {
	dir := scaffolded(t, defaultAnswers()...)
	raw, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "identity/") {
		t.Error("the gitignore must keep the identity out of version control")
	}
}
