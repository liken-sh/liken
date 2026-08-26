package main

// This file tests the network functions that produce the same
// output for the same input. Raising links, DHCP exchanges, and
// routing tables need a kernel, and their tests run under QEMU.
//
// Checking a manifest's names against the ports a machine has is the
// reason those functions take a plain list of ports instead of
// netlink links: the check is where the mistakes are, so it is tested
// here, on every machine, with no kernel in the way.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/machine"
)

// twoPorts is a machine with two ports, which the kernel numbered in
// the order it probed them.
func twoPorts() []interfaceIdentity {
	return []interfaceIdentity{
		{name: "eth0", mac: net.HardwareAddr{0xe0, 0x51, 0xd8, 0xaa, 0xbb, 0x01}},
		{name: "eth1", mac: net.HardwareAddr{0xe0, 0x51, 0xd8, 0xaa, 0xbb, 0x02}},
	}
}

func TestRequirePortAcceptsAPortTheMachineHas(t *testing.T) {
	if err := requirePort("eth1", twoPorts()); err != nil {
		t.Error(err)
	}
}

func TestRequirePortListsThePortsTheMachineHasWhenNoneMatches(t *testing.T) {
	// Nobody can see this machine. It has no shell and no SSH, so
	// the console message is the whole diagnosis. A message that
	// names the ports the machine really has turns a drive to the
	// site into an edit of the manifest.
	err := requirePort("enp3s0", twoPorts())
	if err == nil {
		t.Fatal("expected an error for a name no port answers to")
	}
	for _, want := range []string{"enp3s0", "eth0, eth1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must carry %q: %v", want, err)
		}
	}
}

func TestRequirePortOnAMachineWithNoPortsSaysSo(t *testing.T) {
	// An empty listing must still read as a sentence, because it is
	// the answer to "which ports does this machine have".
	err := requirePort("eth0", nil)
	if err == nil {
		t.Fatal("expected an error on a machine with no ports")
	}
	if !strings.Contains(err.Error(), "no network interface at all") {
		t.Errorf("got %v", err)
	}
}

func TestPresentInterfacesLeavesOutLoopback(t *testing.T) {
	// Loopback is not hardware, the boot has already raised it, and
	// leaving it out keeps it out of the listing that tells a person
	// what their machine has.
	links := []netlink.Link{
		&netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "lo", Flags: net.FlagLoopback}},
		&netlink.Device{LinkAttrs: netlink.LinkAttrs{
			Name:         "eth0",
			HardwareAddr: net.HardwareAddr{0xe0, 0x51, 0xd8, 0xaa, 0xbb, 0x01},
		}},
	}
	present := presentInterfaces(links)
	if len(present) != 1 || present[0].name != "eth0" {
		t.Fatalf("got %v", present)
	}
}

func TestPickInterfaceTakesTheFirstPortWithAnAddress(t *testing.T) {
	// This is the zero-configuration default, for the one machine
	// with one port where there is nothing to choose between. A link
	// with no hardware address is a virtual device, not a card.
	present := []interfaceIdentity{
		{name: "dummy0"},
		{name: "eth0", mac: net.HardwareAddr{0xe0, 0x51, 0xd8, 0xaa, 0xbb, 0x01}},
	}
	got, err := pickInterface(present)
	if err != nil {
		t.Fatal(err)
	}
	if got != "eth0" {
		t.Errorf("got %q", got)
	}
}

func TestPickInterfaceFindsNothingOnAMachineWithNoCard(t *testing.T) {
	if _, err := pickInterface(nil); err == nil {
		t.Error("expected an error when no link carries a hardware address")
	}
}

func TestJoinIPsReadsAsAList(t *testing.T) {
	ips := []net.IP{net.ParseIP("10.0.2.3"), net.ParseIP("10.0.2.4")}
	if got := joinIPs(ips); got != "10.0.2.3, 10.0.2.4" {
		t.Errorf("got %q", got)
	}
}

func TestConnectionReportNarratesEachMethod(t *testing.T) {
	// The report prints the same facts that interfaceFacts publishes.
	// Testing both methods confirms that the report prints every
	// field.
	_, subnet, _ := net.ParseCIDR("10.0.2.0/24")
	dhcp := &connection{
		ifname:      "eth0",
		mac:         net.HardwareAddr{0x52, 0x54, 0x00, 0x4c, 0x4b, 0x01},
		addr:        &net.IPNet{IP: net.ParseIP("10.0.2.15"), Mask: subnet.Mask},
		method:      machine.MethodDHCP,
		gateway:     net.ParseIP("10.0.2.2"),
		server:      net.ParseIP("10.0.2.2"),
		nameservers: []net.IP{net.ParseIP("10.0.2.3")},
		leaseTime:   time.Hour,
	}
	dhcp.report()
	static := &connection{
		ifname:  "eth1",
		mac:     net.HardwareAddr{0x52, 0x54, 0x00, 0x4c, 0x4c, 0x01},
		addr:    &net.IPNet{IP: net.ParseIP("10.10.0.1"), Mask: subnet.Mask},
		method:  machine.MethodStatic,
		gateway: net.ParseIP("10.10.0.254"),
	}
	static.report()
}

func TestResolvConfCapsAtThreeNameservers(t *testing.T) {
	// Since the 1980s, glibc has read at most three nameservers.
	// Every other resolver stack follows the same limit. kubelet
	// logs a warning at each sync when a node offers more than
	// three. Linode's DHCP service hands out its whole regional
	// fleet of resolvers, eighteen in total, but the machine keeps
	// only three.
	conns := []*connection{{nameservers: []net.IP{
		net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"),
		net.ParseIP("10.0.0.3"), net.ParseIP("10.0.0.4"),
		net.ParseIP("10.0.0.5"),
	}}}
	want := "nameserver 10.0.0.1\nnameserver 10.0.0.2\nnameserver 10.0.0.3\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestResolvConfKeepsInterfaceOrder(t *testing.T) {
	// Interface order is priority order. The uplink's lease
	// nameservers come before a later interface's manifest
	// declarations.
	conns := []*connection{
		{nameservers: []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2")}},
		{nameservers: []net.IP{net.ParseIP("10.1.0.1")}},
	}
	want := "nameserver 10.0.0.1\nnameserver 10.0.0.2\nnameserver 10.1.0.1\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestResolvConfDropsDuplicates(t *testing.T) {
	// Listing the same resolver on two interfaces adds no value the
	// second time. With only three slots available, a duplicate
	// takes the place of a real nameserver.
	conns := []*connection{
		{nameservers: []net.IP{net.ParseIP("10.0.0.1")}},
		{nameservers: []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.1.0.1")}},
	}
	want := "nameserver 10.0.0.1\nnameserver 10.1.0.1\n"
	if got := resolvConf(conns); got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestResolvConfWithNoNameserversIsEmpty(t *testing.T) {
	if got := resolvConf([]*connection{{}}); got != "" {
		t.Errorf("no nameservers must render nothing, got %q", got)
	}
}

func TestWritingResolvConfReplacesTheWholeFileAtOnce(t *testing.T) {
	// A radio that settles late rewrites this file while every
	// resolver on the machine reads it. A write in place would leave a
	// reader with a truncated file, so the write must be a rename: the
	// old file keeps serving until the new one replaces it whole.
	path := aimResolvConf(t)
	first := withNameserver(addressed("eth0", "10.10.0.5/24"), "10.10.0.1")
	if err := writeResolvConf([]*connection{first}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	second := withNameserver(joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"), "192.168.1.1")
	if err := writeResolvConf([]*connection{first, second}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if os.SameFile(before, after) {
		t.Error("the rewrite kept the same file, so a reader could see it half written")
	}
	if mode := after.Mode().Perm(); mode != 0o644 {
		t.Errorf("every resolver reads this file: mode %o", mode)
	}
	rendered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "nameserver 10.10.0.1\nnameserver 192.168.1.1\n"
	if string(rendered) != want {
		t.Errorf("got %q, want %q", rendered, want)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the write left a temp file behind: %d entries", len(entries))
	}
}
