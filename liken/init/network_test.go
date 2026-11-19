package main

// Tests of the network functions that depend only on their arguments.
// networkbringup_test.go tests the netlink and DHCP calls against a
// fake kernel.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/liken/machine"
)

// twoPorts is a machine with two ports, eth0 and eth1.
func twoPorts() []interfaceIdentity {
	return []interfaceIdentity{
		{name: "eth0", mac: net.HardwareAddr{0xe0, 0x51, 0xd8, 0xaa, 0xbb, 0x01}},
		{name: "eth1", mac: net.HardwareAddr{0xe0, 0x51, 0xd8, 0xaa, 0xbb, 0x02}},
	}
}

func TestRequirePortAcceptsAPortTheMachineHas(t *testing.T) {
	if err := requirePort("eth1", twoPorts()); err != nil {
		t.Errorf("requirePort(eth1) = %v, want nil", err)
	}
}

func TestRequirePortListsThePortsTheMachineHasWhenNoneMatches(t *testing.T) {
	err := requirePort("enp3s0", twoPorts())
	if err == nil {
		t.Fatal("requirePort(enp3s0) = nil, want an error because no port has that name")
	}
	for _, want := range []string{"enp3s0", "eth0, eth1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to contain %q", err, want)
		}
	}
}

func TestRequirePortOnAMachineWithNoPortsSaysSo(t *testing.T) {
	err := requirePort("eth0", nil)
	if err == nil {
		t.Fatal("requirePort on a machine with no ports = nil, want an error")
	}
	if !strings.Contains(err.Error(), "no network interface at all") {
		t.Errorf("the error is %q, want it to contain \"no network interface at all\"", err)
	}
}

func TestPresentInterfacesLeavesOutLoopback(t *testing.T) {
	links := []netlink.Link{
		&netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "lo", Flags: net.FlagLoopback}},
		&netlink.Device{LinkAttrs: netlink.LinkAttrs{
			Name:         "eth0",
			HardwareAddr: net.HardwareAddr{0xe0, 0x51, 0xd8, 0xaa, 0xbb, 0x01},
		}},
	}
	present := presentInterfaces(links)
	if len(present) != 1 || present[0].name != "eth0" {
		t.Fatalf("presentInterfaces = %v, want only eth0", present)
	}
}

func TestPickInterfaceTakesTheFirstPortWithAHardwareAddress(t *testing.T) {
	present := []interfaceIdentity{
		{name: "dummy0"},
		{name: "eth0", mac: net.HardwareAddr{0xe0, 0x51, 0xd8, 0xaa, 0xbb, 0x01}},
	}
	got, err := pickInterface(present)
	if err != nil {
		t.Fatalf("pickInterface: %v", err)
	}
	if got != "eth0" {
		t.Errorf("pickInterface = %q, want eth0", got)
	}
}

func TestPickInterfaceFindsNothingOnAMachineWithNoCard(t *testing.T) {
	if _, err := pickInterface(nil); err == nil {
		t.Error("pickInterface(nil) returned no error, want one because no link has a hardware address")
	}
}

func TestJoinIPsReadsAsAList(t *testing.T) {
	ips := []net.IP{net.ParseIP("10.0.2.3"), net.ParseIP("10.0.2.4")}
	if got := joinIPs(ips); got != "10.0.2.3, 10.0.2.4" {
		t.Errorf("joinIPs = %q, want \"10.0.2.3, 10.0.2.4\"", got)
	}
}

// The console is the only place a person reads a machine's addresses,
// so each method prints its own facts.
func TestConnectionReportPrintsEachMethod(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("10.0.2.0/24")
	cases := []struct {
		name string
		conn *connection
		want []string
	}{
		{"a DHCP lease", &connection{
			ifname:      "eth0",
			mac:         net.HardwareAddr{0x52, 0x54, 0x00, 0x4c, 0x4b, 0x01},
			addr:        &net.IPNet{IP: net.ParseIP("10.0.2.15"), Mask: subnet.Mask},
			method:      machine.MethodDHCP,
			gateway:     net.ParseIP("10.0.2.2"),
			server:      net.ParseIP("10.0.2.2"),
			nameservers: []net.IP{net.ParseIP("10.0.2.3")},
			leaseTime:   time.Hour,
		}, []string{
			"liken: eth0 (52:54:00:4c:4b:01) is 10.0.2.15/24 (dhcp)\n",
			"liken:   gateway 10.0.2.2, dhcp server 10.0.2.2, lease 1h0m0s\n",
			"liken:   nameservers 10.0.2.3\n",
		}},
		{"a static address", &connection{
			ifname:  "eth1",
			mac:     net.HardwareAddr{0x52, 0x54, 0x00, 0x4c, 0x4c, 0x01},
			addr:    &net.IPNet{IP: net.ParseIP("10.10.0.1"), Mask: subnet.Mask},
			method:  machine.MethodStatic,
			gateway: net.ParseIP("10.10.0.254"),
		}, []string{
			"liken: eth1 (52:54:00:4c:4c:01) is 10.10.0.1/24 (static)\n",
			"liken:   gateway 10.10.0.254\n",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := capturedStdout(t, c.conn.report)
			if want := strings.Join(c.want, ""); got != want {
				t.Errorf("report printed\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestResolvConfCapsAtThreeNameservers(t *testing.T) {
	conns := []*connection{{nameservers: []net.IP{
		net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"),
		net.ParseIP("10.0.0.3"), net.ParseIP("10.0.0.4"),
		net.ParseIP("10.0.0.5"),
	}}}
	want := "nameserver 10.0.0.1\nnameserver 10.0.0.2\nnameserver 10.0.0.3\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("resolvConf =\n%swant\n%s", got, want)
	}
}

func TestResolvConfKeepsInterfaceOrder(t *testing.T) {
	// Interface order is priority order, so the nameservers of the
	// first interface come first.
	conns := []*connection{
		{nameservers: []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2")}},
		{nameservers: []net.IP{net.ParseIP("10.1.0.1")}},
	}
	want := "nameserver 10.0.0.1\nnameserver 10.0.0.2\nnameserver 10.1.0.1\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("resolvConf =\n%swant\n%s", got, want)
	}
}

func TestResolvConfDropsDuplicates(t *testing.T) {
	// A duplicate would use one of the three places without adding a
	// resolver.
	conns := []*connection{
		{nameservers: []net.IP{net.ParseIP("10.0.0.1")}},
		{nameservers: []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.1.0.1")}},
	}
	want := "nameserver 10.0.0.1\nnameserver 10.1.0.1\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("resolvConf =\n%swant\n%s", got, want)
	}
}

func TestResolvConfWithNoNameserversIsEmpty(t *testing.T) {
	if got := resolvConf([]*connection{{}}); got != "" {
		t.Errorf("resolvConf = %q, want an empty string because there are no nameservers", got)
	}
}

func TestWritingResolvConfReplacesTheWholeFileAtOnce(t *testing.T) {
	// A different file at the path after the rewrite shows that the
	// write renamed a new file into place and did not truncate the old
	// one.
	path := useResolvConf(t)
	first := withNameserver(addressed("eth0", "10.10.0.5/24"), "10.10.0.1")
	if err := writeResolvConf([]*connection{first}); err != nil {
		t.Fatalf("writing resolv.conf the first time: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the file after the first write: %v", err)
	}

	second := withNameserver(joinedRadio("wlan0", "homenet", "192.168.1.20/24"), "192.168.1.1")
	if err := writeResolvConf([]*connection{first, second}); err != nil {
		t.Fatalf("writing resolv.conf the second time: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the file after the second write: %v", err)
	}

	if os.SameFile(before, after) {
		t.Error("the second write reused the first file, want a new file renamed into place so that no reader sees a half-written one")
	}
	if mode := after.Mode().Perm(); mode != 0o644 {
		t.Errorf("resolv.conf has mode %o, want 644 because every process reads it", mode)
	}
	rendered, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading resolv.conf: %v", err)
	}
	want := "nameserver 10.10.0.1\nnameserver 192.168.1.1\n"
	if string(rendered) != want {
		t.Errorf("resolv.conf is %q, want %q", rendered, want)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("listing the directory of resolv.conf: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory has %d entries, want only resolv.conf", len(entries))
	}
}
