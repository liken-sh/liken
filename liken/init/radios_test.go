package main

// These tests run the two passes with scripted steps, so they need no
// kernel, no radio, and no supplicant. In place of a wedged driver,
// the raise deadline tests use a raise that blocks on a channel.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

func wired(name string) machine.InterfaceSpec {
	return machine.InterfaceSpec{Name: name}
}

func declaredRadio(name, ssid string) machine.InterfaceSpec {
	return machine.InterfaceSpec{
		Name:     name,
		Wireless: &machine.WirelessSpec{SSID: ssid, Security: machine.WirelessWPAPSK},
	}
}

// joinedRadio returns the connection of a radio that associated and
// then got an address.
func joinedRadio(name, ssid, cidr string) *connection {
	conn := addressed(name, cidr)
	conn.radio = &radio{ifname: name, ssid: ssid, state: machine.WirelessConnected}
	return conn
}

// stuckRadio returns the connection that bringUpRadio returns when
// the raise does not return before the deadline.
func stuckRadio(name, ssid string) *connection {
	return &connection{ifname: name, radio: &radio{
		ifname: name, ssid: ssid, state: machine.WirelessNotRaised,
		message: raiseStuck{ifname: name, timeout: raiseTimeout}.Error(),
	}}
}

// clusterAt returns a cluster document with the two fields that
// backgroundable reads: the endpoint and the nodeCIDR.
func clusterAt(endpoint, nodeCIDR string) *cluster.Cluster {
	return &cluster.Cluster{
		Metadata: api.ObjectMeta{Name: "lab"},
		Spec: cluster.ClusterSpec{
			Endpoint: endpoint,
			Network:  cluster.ClusterNetworkSpec{NodeCIDR: nodeCIDR},
		},
	}
}

func names(conns []*connection) string {
	listed := make([]string, len(conns))
	for i, conn := range conns {
		listed[i] = conn.ifname
	}
	return strings.Join(listed, ",")
}

// scriptedPasses replaces the steps of both passes. It records which
// interfaces each step ran for and returns the connection in answers
// for that name. The radio step waits for hold to close, so a test can
// show that the boot went on while a radio was still joining.
type scriptedPasses struct {
	mu        sync.Mutex
	wired     []string
	radios    []string
	ciphers   int
	readdress int

	answers map[string]*connection
	hold    chan struct{}
}

func scripted(answers map[string]*connection) *scriptedPasses {
	held := make(chan struct{})
	close(held)
	return &scriptedPasses{answers: answers, hold: held}
}

func (s *scriptedPasses) answer(ifc machine.InterfaceSpec) (*connection, error) {
	conn, ok := s.answers[ifc.Name]
	if !ok {
		return nil, fmt.Errorf("no interface is named %s", ifc.Name)
	}
	return conn, nil
}

func (s *scriptedPasses) record(names *[]string, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*names = append(*names, name)
}

func (s *scriptedPasses) asked() ([]string, []string, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.wired...), append([]string(nil), s.radios...), s.ciphers, s.readdress
}

func (s *scriptedPasses) passes(route routeLookup) interfacePasses {
	return interfacePasses{
		wired: func(ifc machine.InterfaceSpec) (*connection, error) {
			s.record(&s.wired, ifc.Name)
			return s.answer(ifc)
		},
		radio: func(ifc machine.InterfaceSpec) (*connection, error) {
			<-s.hold
			s.record(&s.radios, ifc.Name)
			return s.answer(ifc)
		},
		route: route,
		ciphers: func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.ciphers++
		},
		readdress: func(conns []*connection, interfaces []machine.InterfaceSpec, r *radio) []*connection {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.readdress++
			return conns
		},
	}
}

func TestAWiredOnlySpecNeverEntersPassTwo(t *testing.T) {
	// With no radio in the spec, run brings up the wired interfaces
	// in spec order, loads no ciphers, and starts no background pass.
	s := scripted(map[string]*connection{
		"eth0": addressed("eth0", "10.10.0.5/24"),
		"eth1": addressed("eth1", "192.168.9.5/24"),
	})
	conns, pass := s.passes(routesVia("eth0")).run([]machine.InterfaceSpec{wired("eth0"), wired("eth1")}, labCluster())

	if pass != nil {
		t.Error("run returned a background pass, want none for a spec with no radio")
	}
	wiredNames, radios, ciphers, _ := s.asked()
	if len(radios) != 0 {
		t.Errorf("pass two ran for %v, want no radio", radios)
	}
	if ciphers != 0 {
		t.Errorf("the ciphers loaded %d times, want 0 on a machine with no radio", ciphers)
	}
	if got := strings.Join(wiredNames, ","); got != "eth0,eth1" {
		t.Errorf("pass one ran for %q, want the spec order", got)
	}
	if len(conns) != 2 {
		t.Errorf("the pass returned %d connections, want 2", len(conns))
	}
}

func TestARoutedMachineJoinsItsRadioInTheBackground(t *testing.T) {
	// run must return while the radio is still joining. The scripted
	// radio cannot finish until the test closes hold.
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": joinedRadio("wlan0", "homenet", "192.168.1.20/24"),
	})
	s.hold = make(chan struct{})

	conns, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "homenet")}, labCluster())

	if pass == nil {
		t.Fatal("run returned no background pass, want one for a machine with a wired route to the cluster")
	}
	if _, radios, ciphers, _ := s.asked(); len(radios) != 0 || ciphers != 1 {
		t.Errorf("the ciphers loaded %d times and pass two ran for %v, want neither before the radio joined", ciphers, radios)
	}
	if len(conns) != 2 || conns[1].ifname != "wlan0" || conns[1].addr != nil {
		t.Fatalf("the connections are %+v, want eth0 and wlan0 with no address on wlan0 yet", conns)
	}
	if got := conns[1].radio.state; got != machine.WirelessAssociating {
		t.Errorf("the radio state is %q, want Associating", got)
	}

	close(s.hold)
	settled := <-pass.settled
	if settled.addr == nil || settled.radio.state != machine.WirelessConnected {
		t.Errorf("the background pass sent %+v, want an address and the state Connected", settled)
	}
}

func TestAnUnroutedRadioOnlyMachineJoinsInTheForeground(t *testing.T) {
	s := scripted(map[string]*connection{
		"wlan0": joinedRadio("wlan0", "homenet", "192.168.1.20/24"),
	})
	conns, pass := s.passes(routesNowhere()).run(
		[]machine.InterfaceSpec{declaredRadio("wlan0", "homenet")}, labCluster())

	if pass != nil {
		t.Error("run returned a background pass after a foreground pass, want none")
	}
	_, radios, ciphers, _ := s.asked()
	if len(radios) != 1 || ciphers != 1 {
		t.Fatalf("pass two ran for %v with %d cipher loads, want one radio and one load", radios, ciphers)
	}
	if len(conns) != 1 || conns[0].addr == nil {
		t.Fatalf("the connections are %+v, want only the radio, with an address", conns)
	}
}

func TestAnUnroutedRadioOnlyMachineStillWaitsForTheRadio(t *testing.T) {
	// A wrong key with no other path to the cluster makes the boot
	// wait in waitForRadio, which prints the reason to the console. The radio
	// joins during the wait, so run must address it.
	console := heldConsole(t)
	refused := refusedRadio("wlan0", "homenet")
	refused.radio.control = stubControl()
	pushEvent(t, refused.radio.control, "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]")

	s := scripted(map[string]*connection{"wlan0": refused})
	s.passes(routesNowhere()).run([]machine.InterfaceSpec{declaredRadio("wlan0", "homenet")}, labCluster())

	if _, _, _, readdress := s.asked(); readdress != 1 {
		t.Errorf("the radio that joined during the wait was addressed %d times, want 1", readdress)
	}
	held, err := os.ReadFile(console)
	if err != nil {
		t.Fatalf("reading the console: %v", err)
	}
	if !strings.Contains(string(held), "cannot join homenet") {
		t.Errorf("the console holds %q, want it to contain \"cannot join homenet\"", held)
	}
}

func TestABackgroundRadioThatCannotBeReachedStillReports(t *testing.T) {
	// A declared radio that the machine does not have must still send
	// a connection on settled, or status shows it as Associating
	// forever.
	s := scripted(map[string]*connection{"eth0": addressed("eth0", "10.10.0.5/24")})
	conns, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "homenet")}, labCluster())
	if pass == nil {
		t.Fatal("run returned no background pass, want one for a machine with a wired route to the cluster")
	}
	settled := <-pass.settled
	if settled.radio.state != machine.WirelessNoCarrier || !strings.Contains(settled.radio.message, "wlan0") {
		t.Errorf("the settled radio is %+v, want NoCarrier with a message that names wlan0", settled.radio)
	}
	if len(conns) != 2 {
		t.Errorf("the pass returned %d connections, want 2", len(conns))
	}
}

func TestANodeCIDRThatOnlyTheRadioMatchesWaitsForTheRadio(t *testing.T) {
	// The wired interface has a route toward the endpoint, but only
	// the radio's address is inside the nodeCIDR.
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "192.168.9.5/24"),
		"wlan0": joinedRadio("wlan0", "homenet", "10.10.0.20/24"),
	})
	conns, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "homenet")},
		clusterAt(labEndpoint, "10.10.0.0/24"))

	if pass != nil {
		t.Fatal("run returned a background pass, want none, so the boot waits for the radio when no wired address is in the nodeCIDR")
	}
	if _, radios, _, _ := s.asked(); len(radios) != 1 {
		t.Errorf("pass two ran for %v in the foreground, want none", radios)
	}
	if ip, _ := nodeAddress(clusterAt(labEndpoint, "10.10.0.0/24"), conns); ip != "10.10.0.20" {
		t.Errorf("the node address is %q, want the address of the radio", ip)
	}
}

func TestANodeCIDRThatAWiredPortMatchesBackgroundsTheRadio(t *testing.T) {
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": joinedRadio("wlan0", "homenet", "192.168.1.20/24"),
	})
	_, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "homenet")},
		clusterAt(labEndpoint, "10.10.0.0/24"))

	if pass == nil {
		t.Fatal("run returned no background pass, want one when a wired address is in the nodeCIDR")
	}
	<-pass.settled
}

func TestAClusterWithNoNodeCIDRBackgroundsOnTheRouteAlone(t *testing.T) {
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "192.168.9.5/24"),
		"wlan0": joinedRadio("wlan0", "homenet", "192.168.1.20/24"),
	})
	_, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "homenet")},
		clusterAt(labEndpoint, ""))

	if pass == nil {
		t.Fatal("run returned no background pass, want one when the cluster declares no nodeCIDR and a wired route exists")
	}
	<-pass.settled
}

func TestAnEndpointWithNoIPAddressBackgroundsOnTheNodeCIDRAlone(t *testing.T) {
	// routeToward counts an empty endpoint and a DNS name as
	// reachable, so only the nodeCIDR check decides, even though the
	// route lookup fails.
	for name, endpoint := range map[string]string{
		"a machine alone": "",
		"a DNS name":      "https://cluster.homenet.example:6443",
	} {
		s := scripted(map[string]*connection{
			"eth0":  addressed("eth0", "10.10.0.5/24"),
			"wlan0": joinedRadio("wlan0", "homenet", "192.168.1.20/24"),
		})
		_, pass := s.passes(routesNowhere()).run(
			[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "homenet")},
			clusterAt(endpoint, "10.10.0.0/24"))

		if pass == nil {
			t.Fatalf("%s: run returned no background pass, want one, so the boot goes on without waiting for the radio", name)
		}
		<-pass.settled
	}
}

func TestAStuckRaiseStopsTheRadiosBehindIt(t *testing.T) {
	// After a stuck raise, joinRadios must try no other radio, and
	// each radio it skipped must report that it was not attempted.
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": stuckRadio("wlan0", "homenet"),
		"wlan1": joinedRadio("wlan1", "homenet", "192.168.1.21/24"),
	})
	conns, pass := s.passes(routesVia("eth0")).run([]machine.InterfaceSpec{
		wired("eth0"), declaredRadio("wlan0", "homenet"), declaredRadio("wlan1", "homenet"),
	}, labCluster())
	if pass == nil {
		t.Fatal("run returned no background pass, want one for a machine with a wired route to the cluster")
	}
	if len(conns) != 3 {
		t.Fatalf("run returned %d connections, want 3", len(conns))
	}

	results := map[string]*radio{}
	for range 2 {
		settled := <-pass.settled
		results[settled.ifname] = settled.radio
	}
	if got := results["wlan0"].state; got != machine.WirelessNotRaised {
		t.Errorf("the wedged radio is %q, want NotRaised", got)
	}
	if got := results["wlan1"].state; got != machine.WirelessNoCarrier {
		t.Errorf("a radio nothing tried is %q, want NoCarrier", got)
	}
	for _, want := range []string{"not attempted", "wlan0"} {
		if !strings.Contains(results["wlan1"].message, want) {
			t.Errorf("the message of the skipped radio is %q, want it to contain %q", results["wlan1"].message, want)
		}
	}
	if _, radios, _, _ := s.asked(); strings.Join(radios, ",") != "wlan0" {
		t.Errorf("pass two ran for %v, want only wlan0 because nothing runs behind a stuck raise", radios)
	}
}

func TestTheRadioWatcherEndsAfterAStuckRaise(t *testing.T) {
	// Both radios must send a connection after one raise is stuck, so
	// watchRadios counts pending down to zero and returns. If it did
	// not, the skipped radio would stay Associating in status.
	tree := machine.FactsTree{Dir: filepath.Join(t.TempDir(), "facts")}
	useResolvConf(t)
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": stuckRadio("wlan0", "homenet"),
		"wlan1": joinedRadio("wlan1", "homenet", "192.168.1.21/24"),
	})
	_, pass := s.passes(routesVia("eth0")).run([]machine.InterfaceSpec{
		wired("eth0"), declaredRadio("wlan0", "homenet"), declaredRadio("wlan1", "homenet"),
	}, labCluster())

	done := make(chan error, 1)
	go func() { done <- watchRadios(nil, pass, nil, tree, nil)(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("running watchRadios: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watchRadios did not return within 10 seconds, want it to return after the second radio settles")
	}
	if state := readFactFile(t, tree, "network/interfaces/wlan1/wireless/state"); state != string(machine.WirelessNoCarrier) {
		t.Errorf("the facts tree has the skipped radio as %q, want NoCarrier", state)
	}
}

func TestTheRadioComponentJoinsEachRadioOnlyOnce(t *testing.T) {
	// The machine plane runs the component again after an error or a
	// panic. A second run must not start another supplicant on a radio
	// that has one, or send a second connection for it.
	s := scripted(map[string]*connection{
		"wlan0": joinedRadio("wlan0", "homenet", "192.168.1.20/24"),
	})
	radios := []machine.InterfaceSpec{declaredRadio("wlan0", "homenet")}
	pass := &radioPass{settled: make(chan *connection, 4), pending: 1}
	component := s.passes(routesVia("eth0")).joinRadios(pass, radios)

	if err := component(context.Background()); err != nil {
		t.Fatalf("running the radio component: %v", err)
	}
	if err := component(context.Background()); err != nil {
		t.Fatalf("running the radio component: %v", err)
	}

	if len(pass.settled) != 1 {
		t.Errorf("the component sent %d verdicts for one radio, want 1", len(pass.settled))
	}
	if _, joined, _, _ := s.asked(); strings.Join(joined, ",") != "wlan0" {
		t.Errorf("the component joined %v, want only wlan0 once", joined)
	}
}

func TestARadioWhoseBringUpPanicsStillReports(t *testing.T) {
	// The watch waits for one result for each radio, so a panic in a
	// bring-up must still send a result, and status must name the
	// panic in place of the pending state.
	passes := interfacePasses{radio: func(ifc machine.InterfaceSpec) (*connection, error) {
		panic("the driver returned a nil station")
	}}
	radios := []machine.InterfaceSpec{declaredRadio("wlan0", "homenet")}
	pass := &radioPass{settled: make(chan *connection, 4), pending: 1}
	component := passes.joinRadios(pass, radios)

	_ = runComponent(context.Background(), component)
	_ = runComponent(context.Background(), component)

	if len(pass.settled) != 1 {
		t.Fatalf("the component sent %d results for one radio, want 1", len(pass.settled))
	}
	got := (<-pass.settled).radio
	if got.state != machine.WirelessNoCarrier || !strings.Contains(got.message, "the driver returned a nil station") {
		t.Errorf("the radio is %s with message %q, want NoCarrier with a message that names the panic", got.state, got.message)
	}
}

func TestTheConnectionsComeBackInSpecOrder(t *testing.T) {
	interfaces := []machine.InterfaceSpec{declaredRadio("wlan0", "homenet"), wired("eth0")}

	background := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": joinedRadio("wlan0", "homenet", "192.168.1.20/24"),
	})
	conns, pass := background.passes(routesVia("eth0")).run(interfaces, labCluster())
	if pass == nil {
		t.Fatal("run returned no background pass, want one for a machine with a wired route to the cluster")
	}
	if got := names(conns); got != "wlan0,eth0" {
		t.Errorf("the background pass returned %q, want the spec's order", got)
	}
	if got := names(pass.conns); got != "wlan0,eth0" {
		t.Errorf("the pass reports %q, want the spec's order", got)
	}
	<-pass.settled

	foreground := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "192.168.9.5/24"),
		"wlan0": joinedRadio("wlan0", "homenet", "10.10.0.20/24"),
	})
	conns, pass = foreground.passes(routesNowhere()).run(interfaces, labCluster())
	if pass != nil {
		t.Fatal("run returned a background pass, want none for a machine with no route to the cluster")
	}
	if got := names(conns); got != "wlan0,eth0" {
		t.Errorf("the foreground pass returned %q, want the spec's order", got)
	}
}

// stalledRaise replaces the netlink calls with a link lookup that
// succeeds and a raise that blocks until the test ends.
func stalledRaise(t *testing.T) {
	t.Helper()
	stall := make(chan struct{})
	origSetUp, origByName := linkSetUp, linkByName
	linkSetUp = func(netlink.Link) error {
		<-stall
		return nil
	}
	linkByName = func(name string) (netlink.Link, error) {
		return &netlink.Device{LinkAttrs: netlink.LinkAttrs{
			Name:         name,
			HardwareAddr: net.HardwareAddr{0x04, 0x4a, 0x2c, 0x11, 0x22, 0x33},
		}}, nil
	}
	t.Cleanup(func() {
		close(stall)
		linkSetUp, linkByName = origSetUp, origByName
	})
}

func TestARaiseThatDoesNotReturnGetsItsOwnState(t *testing.T) {
	stalledRaise(t)
	conn, err := bringUpRadio(declaredRadio("wlan0", "homenet"),
		[]interfaceIdentity{{name: "wlan0"}}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("bringing up the radio: %v", err)
	}
	if conn.radio.state != machine.WirelessNotRaised {
		t.Fatalf("the radio state is %q, want NotRaised", conn.radio.state)
	}
	// init cannot see which lock the stuck thread holds, so the
	// message must not name one.
	for _, want := range []string{"wlan0", "did not return"} {
		if !strings.Contains(conn.radio.message, want) {
			t.Errorf("the message is %q, want it to contain %q", conn.radio.message, want)
		}
	}
	if strings.Contains(conn.radio.message, "holds the kernel's rtnl lock") {
		t.Errorf("the message is %q, want no claim about the lock, which init cannot observe", conn.radio.message)
	}
	if conn.addr != nil {
		t.Errorf("the connection has the address %v, want none for a link that never came up", conn.addr)
	}
}

func TestARaiseThatFailsOutrightIsAnError(t *testing.T) {
	origSetUp, origByName := linkSetUp, linkByName
	linkSetUp = func(netlink.Link) error { return fmt.Errorf("operation not permitted") }
	linkByName = func(name string) (netlink.Link, error) {
		return &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: name}}, nil
	}
	t.Cleanup(func() { linkSetUp, linkByName = origSetUp, origByName })

	if _, err := bringUpRadio(declaredRadio("wlan0", "homenet"),
		[]interfaceIdentity{{name: "wlan0"}}, time.Second); err == nil {
		t.Fatal("bringUpRadio returned no error, want the kernel's refusal")
	}
}

// raisesCleanly replaces the netlink calls with ones that succeed at
// once, for tests of what happens after the link is up.
func raisesCleanly(t *testing.T) {
	t.Helper()
	origSetUp, origByName := linkSetUp, linkByName
	linkSetUp = func(netlink.Link) error { return nil }
	linkByName = func(name string) (netlink.Link, error) {
		return &netlink.Device{LinkAttrs: netlink.LinkAttrs{
			Name:         name,
			HardwareAddr: net.HardwareAddr{0x04, 0x4a, 0x2c, 0x11, 0x22, 0x33},
		}}, nil
	}
	t.Cleanup(func() { linkSetUp, linkByName = origSetUp, origByName })
}

func TestARadioThatJoinedButCouldNotBeAddressedKeepsItsJoin(t *testing.T) {
	// NoCarrier would send a person to check the access point instead
	// of the address in the manifest.
	raisesCleanly(t)
	orig := joinRadio
	joinRadio = func(ifc machine.InterfaceSpec, stateRoot string) *radio {
		return &radio{ifname: ifc.Name, ssid: ifc.Wireless.SSID, state: machine.WirelessConnected}
	}
	t.Cleanup(func() { joinRadio = orig })

	ifc := declaredRadio("wlan0", "homenet")
	ifc.Address = "not-a-cidr"
	conn, err := bringUpRadio(ifc, []interfaceIdentity{{name: "wlan0"}}, time.Second)
	if err != nil {
		t.Fatalf("bringing up the radio: %v", err)
	}

	if conn.radio.state != machine.WirelessConnected {
		t.Errorf("the state is %q, want Connected", conn.radio.state)
	}
	if conn.addr != nil {
		t.Errorf("the connection has the address %v, want none after addressing failed", conn.addr)
	}
	if !strings.Contains(conn.radio.message, "not-a-cidr") {
		t.Errorf("the message is %q, want it to contain not-a-cidr", conn.radio.message)
	}
}

// useResolvConf points resolvConfPath at a temporary file and returns
// its path.
func useResolvConf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolv.conf")
	orig := resolvConfPath
	resolvConfPath = path
	t.Cleanup(func() { resolvConfPath = orig })
	return path
}

func withNameserver(conn *connection, address string) *connection {
	conn.nameservers = []net.IP{net.ParseIP(address)}
	return conn
}

func TestALateRadioReachesTheFactsTreeAndResolvConf(t *testing.T) {
	// A radio that joins in the background must reach the facts tree,
	// which the Machine status is built from, and its nameservers must
	// be added to resolv.conf.
	tree := machine.FactsTree{Dir: filepath.Join(t.TempDir(), "facts")}
	resolv := useResolvConf(t)

	pass := &radioPass{
		conns: []*connection{
			withNameserver(addressed("eth0", "10.10.0.5/24"), "10.10.0.1"),
			pendingRadio(declaredRadio("wlan0", "homenet")),
		},
		settled: make(chan *connection, 1),
		pending: 1,
	}
	pass.settled <- withNameserver(joinedRadio("wlan0", "homenet", "192.168.1.20/24"), "192.168.1.1")

	if err := watchRadios(nil, pass, nil, tree, nil)(context.Background()); err != nil {
		t.Fatalf("running watchRadios: %v", err)
	}

	state := readFactFile(t, tree, "network/interfaces/wlan0/wireless/state")
	if state != string(machine.WirelessConnected) {
		t.Errorf("the facts tree has state %q, want Connected", state)
	}
	if address := readFactFile(t, tree, "network/interfaces/wlan0/address"); address != "192.168.1.20/24" {
		t.Errorf("the facts tree has address %q, want 192.168.1.20/24", address)
	}
	rendered, err := os.ReadFile(resolv)
	if err != nil {
		t.Fatalf("reading resolv.conf: %v", err)
	}
	for _, want := range []string{"nameserver 10.10.0.1", "nameserver 192.168.1.1"} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("resolv.conf is %q, want it to contain %q", rendered, want)
		}
	}
}

func readFactFile(t *testing.T, tree machine.FactsTree, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(tree.Dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("reading the fact %s: %v", name, err)
	}
	return strings.TrimSpace(string(raw))
}
