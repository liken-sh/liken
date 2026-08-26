package main

// The pass split is a pure decision over function values, so these
// tests run the whole of it, the route question included, with no
// kernel, no radio, and no supplicant: the passes are scripted, the
// raise is a variable, and the resolver file is a temp file. The one
// thing no unit test can produce is a real wedged driver; the raise
// deadline test stands a blocked channel in for it.

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

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// wired is one wired interface as a spec names it.
func wired(name string) machine.InterfaceSpec {
	return machine.InterfaceSpec{Name: name}
}

// declaredRadio is one wireless interface as a spec names it.
func declaredRadio(name, ssid string) machine.InterfaceSpec {
	return machine.InterfaceSpec{
		Name:     name,
		Wireless: &machine.WirelessSpec{SSID: ssid, Security: machine.WirelessWPAPSK},
	}
}

// joinedRadio is an interface whose radio associated and then took an
// address.
func joinedRadio(name, ssid, cidr string) *connection {
	conn := addressed(name, cidr)
	conn.radio = &radio{ifname: name, ssid: ssid, state: machine.WirelessConnected}
	return conn
}

// stuckRadio is an interface whose raise never returned, the verdict
// bringUpRadio hands back when the deadline runs out.
func stuckRadio(name, ssid string) *connection {
	return &connection{ifname: name, radio: &radio{
		ifname: name, ssid: ssid, state: machine.WirelessNotRaised,
		message: raiseStuck{ifname: name, patience: raisePatience}.Error(),
	}}
}

// clusterAt builds a cluster document holding the two facts the pass
// split's gate reads: where the cluster answers, and which subnet a
// node address must fall inside.
func clusterAt(endpoint, nodeCIDR string) *cluster.Cluster {
	return &cluster.Cluster{
		Metadata: api.ObjectMeta{Name: "lab"},
		Spec: cluster.ClusterSpec{
			Endpoint: endpoint,
			Network:  cluster.ClusterNetworkSpec{NodeCIDR: nodeCIDR},
		},
	}
}

// names lists the connections in the order they came back.
func names(conns []*connection) string {
	listed := make([]string, len(conns))
	for i, conn := range conns {
		listed[i] = conn.ifname
	}
	return strings.Join(listed, ",")
}

// scriptedPasses stands in for both passes. It records which
// interfaces each pass was asked for, and answers from a script.
// hold gates the wireless pass, so a test can prove that the boot
// went on while a radio was still joining.
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
	// A machine with no radio must run exactly the boot it ran
	// before this milestone: pass one in spec order, no cipher
	// loads, and nothing behind the boot.
	s := scripted(map[string]*connection{
		"eth0": addressed("eth0", "10.10.0.5/24"),
		"eth1": addressed("eth1", "192.168.9.5/24"),
	})
	conns, pass := s.passes(routesVia("eth0")).run([]machine.InterfaceSpec{wired("eth0"), wired("eth1")}, labCluster())

	if pass != nil {
		t.Error("a wired-only spec must leave no background pass")
	}
	wiredNames, radios, ciphers, _ := s.asked()
	if len(radios) != 0 {
		t.Errorf("pass two ran for %v", radios)
	}
	if ciphers != 0 {
		t.Errorf("the ciphers loaded %d times on a machine with no radio", ciphers)
	}
	if got := strings.Join(wiredNames, ","); got != "eth0,eth1" {
		t.Errorf("pass one ran for %q, and spec order is the order", got)
	}
	if len(conns) != 2 {
		t.Errorf("got %d connections", len(conns))
	}
}

func TestARoutedMachineJoinsItsRadioInTheBackground(t *testing.T) {
	// The 2026-08-26 wedge, restated as a rule: run must return
	// while the radio is still joining. The held channel keeps the
	// scripted radio unsettled until the test releases it, which is
	// the proof that nothing in the boot path waited.
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"),
	})
	s.hold = make(chan struct{})

	conns, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "stonypoint")}, labCluster())

	if pass == nil {
		t.Fatal("a routed machine must background its radio")
	}
	if _, radios, ciphers, _ := s.asked(); len(radios) != 0 || ciphers != 1 {
		t.Errorf("the ciphers loaded %d times, and pass two had already run for %v", ciphers, radios)
	}
	if len(conns) != 2 || conns[1].ifname != "wlan0" || conns[1].addr != nil {
		t.Fatalf("got %d connections; the radio must appear with no address yet", len(conns))
	}
	if got := conns[1].radio.state; got != machine.WirelessAssociating {
		t.Errorf("state = %q", got)
	}

	close(s.hold)
	settled := <-pass.settled
	if settled.addr == nil || settled.radio.state != machine.WirelessConnected {
		t.Errorf("the background pass must report its verdict: %+v", settled.radio)
	}
}

func TestAnUnroutedRadioOnlyMachineJoinsInTheForeground(t *testing.T) {
	// A machine whose only declared path is its radio has nothing
	// to do until the radio joins, so the boot may wait for it, and
	// the join's address must be on the connections the boot goes on
	// with.
	s := scripted(map[string]*connection{
		"wlan0": joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"),
	})
	conns, pass := s.passes(routesNowhere()).run(
		[]machine.InterfaceSpec{declaredRadio("wlan0", "stonypoint")}, labCluster())

	if pass != nil {
		t.Error("a foreground pass leaves nothing running behind the boot")
	}
	_, radios, ciphers, _ := s.asked()
	if len(radios) != 1 || ciphers != 1 {
		t.Fatalf("pass two ran for %v with %d cipher loads", radios, ciphers)
	}
	if len(conns) != 1 || conns[0].addr == nil {
		t.Fatalf("the radio's address must be on the boot's connections: %+v", conns)
	}
}

func TestAnUnroutedRadioOnlyMachineStillParks(t *testing.T) {
	// The park rule survives the restructure unchanged for the one
	// machine that may wait: a deterministic failure with no other
	// path holds the boot, the reason reaches the console, and a
	// join during the hold gets the radio addressed.
	console := heldConsole(t)
	refused := refusedRadio("wlan0", "stonypoint")
	refused.radio.control = stubControl()
	pushEvent(t, refused.radio.control, "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]")

	s := scripted(map[string]*connection{"wlan0": refused})
	s.passes(routesNowhere()).run([]machine.InterfaceSpec{declaredRadio("wlan0", "stonypoint")}, labCluster())

	if _, _, _, readdress := s.asked(); readdress != 1 {
		t.Errorf("a radio that joined during the park must be addressed: %d", readdress)
	}
	held, err := os.ReadFile(console)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(held), "cannot join stonypoint") {
		t.Errorf("the console must carry the reason: %q", held)
	}
}

func TestABackgroundRadioThatCannotBeReachedStillReports(t *testing.T) {
	// A declared radio the machine does not have still answers: the
	// background pass sends a failed connection, because a verdict
	// that never arrives would leave status saying Associating
	// forever.
	s := scripted(map[string]*connection{"eth0": addressed("eth0", "10.10.0.5/24")})
	conns, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "stonypoint")}, labCluster())
	if pass == nil {
		t.Fatal("a routed machine must background its radio")
	}
	settled := <-pass.settled
	if settled.radio.state != machine.WirelessNoCarrier || !strings.Contains(settled.radio.message, "wlan0") {
		t.Errorf("got %+v", settled.radio)
	}
	if len(conns) != 2 {
		t.Errorf("got %d connections", len(conns))
	}
}

func TestANodeCIDRThatOnlyTheRadioAnswersHoldsTheBoot(t *testing.T) {
	// The uplink settled and carries a route toward the endpoint, but
	// the address k3s starts with is inside the nodeCIDR, and only the
	// radio can hold one. k3s must not start without its node IP, so
	// this machine waits for the radio in the foreground.
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "192.168.9.5/24"),
		"wlan0": joinedRadio("wlan0", "stonypoint", "10.10.0.20/24"),
	})
	conns, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "stonypoint")},
		clusterAt(labEndpoint, "10.10.0.0/24"))

	if pass != nil {
		t.Fatal("a machine with no node address must wait for its radio")
	}
	if _, radios, _, _ := s.asked(); len(radios) != 1 {
		t.Errorf("pass two ran for %v in the foreground", radios)
	}
	if ip, _ := nodeAddress(clusterAt(labEndpoint, "10.10.0.0/24"), conns); ip != "10.10.0.20" {
		t.Errorf("the radio must supply the node address, got %q", ip)
	}
}

func TestANodeCIDRAWiredPortAnswersBackgroundsTheRadio(t *testing.T) {
	// Pass one already holds the address k3s starts with, so the
	// radio is additional and the boot goes on without it.
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"),
	})
	_, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "stonypoint")},
		clusterAt(labEndpoint, "10.10.0.0/24"))

	if pass == nil {
		t.Fatal("a machine whose wired port answers the nodeCIDR must background its radio")
	}
	<-pass.settled
}

func TestAClusterWithNoNodeCIDRBackgroundsOnTheRouteAlone(t *testing.T) {
	// With no nodeCIDR, k3s picks the node address itself, so there is
	// no address for the gate to require. The route question decides
	// alone, exactly as it did before the node address joined the gate.
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "192.168.9.5/24"),
		"wlan0": joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"),
	})
	_, pass := s.passes(routesVia("eth0")).run(
		[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "stonypoint")},
		clusterAt(labEndpoint, ""))

	if pass == nil {
		t.Fatal("a cluster with no nodeCIDR keeps the route question alone")
	}
	<-pass.settled
}

func TestTheGateOnAnEndpointNothingCanResolve(t *testing.T) {
	// A machine alone declares no endpoint, and a DNS name needs the
	// network in question. Neither can be checked, so the route
	// question answers yes, and the node address decides on its own.
	for name, endpoint := range map[string]string{
		"a machine alone": "",
		"a DNS name":      "https://cluster.stonypoint.example:6443",
	} {
		s := scripted(map[string]*connection{
			"eth0":  addressed("eth0", "10.10.0.5/24"),
			"wlan0": joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"),
		})
		_, pass := s.passes(routesNowhere()).run(
			[]machine.InterfaceSpec{wired("eth0"), declaredRadio("wlan0", "stonypoint")},
			clusterAt(endpoint, "10.10.0.0/24"))

		if pass == nil {
			t.Fatalf("%s must boot rather than wait", name)
		}
		<-pass.settled
	}
}

func TestAStuckRaiseStopsTheRadiosBehindIt(t *testing.T) {
	// A raise that does not return holds the kernel lock every
	// interface's configuration needs, so the next radio's raise would
	// wait inside the kernel where no deadline reaches it. The pass
	// must stop, and every radio it never tried must say so.
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": stuckRadio("wlan0", "stonypoint"),
		"wlan1": joinedRadio("wlan1", "stonypoint", "192.168.1.21/24"),
	})
	conns, pass := s.passes(routesVia("eth0")).run([]machine.InterfaceSpec{
		wired("eth0"), declaredRadio("wlan0", "stonypoint"), declaredRadio("wlan1", "stonypoint"),
	}, labCluster())
	if pass == nil {
		t.Fatal("a routed machine must background its radios")
	}
	if len(conns) != 3 {
		t.Fatalf("got %d connections", len(conns))
	}

	verdicts := map[string]*radio{}
	for range 2 {
		settled := <-pass.settled
		verdicts[settled.ifname] = settled.radio
	}
	if got := verdicts["wlan0"].state; got != machine.WirelessNotRaised {
		t.Errorf("the wedged radio is %q, want NotRaised", got)
	}
	if got := verdicts["wlan1"].state; got != machine.WirelessNoCarrier {
		t.Errorf("a radio nothing tried is %q, want NoCarrier", got)
	}
	for _, want := range []string{"not attempted", "wlan0"} {
		if !strings.Contains(verdicts["wlan1"].message, want) {
			t.Errorf("the skipped radio's message must carry %q: %q", want, verdicts["wlan1"].message)
		}
	}
	if _, radios, _, _ := s.asked(); strings.Join(radios, ",") != "wlan0" {
		t.Errorf("pass two ran for %v; nothing runs behind a stuck raise", radios)
	}
}

func TestTheVerdictComponentEndsAfterAStuckRaise(t *testing.T) {
	// Two radios, one wedge, and both verdicts arrive, so the
	// component that lands them counts to its total and returns. A
	// component that never returns would hold the pass forever and
	// leave the second radio reporting Associating.
	tree := machine.FactsTree{Dir: filepath.Join(t.TempDir(), "facts")}
	aimResolvConf(t)
	s := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": stuckRadio("wlan0", "stonypoint"),
		"wlan1": joinedRadio("wlan1", "stonypoint", "192.168.1.21/24"),
	})
	_, pass := s.passes(routesVia("eth0")).run([]machine.InterfaceSpec{
		wired("eth0"), declaredRadio("wlan0", "stonypoint"), declaredRadio("wlan1", "stonypoint"),
	}, labCluster())

	done := make(chan error, 1)
	go func() { done <- publishRadioVerdicts(pass, tree, nil)(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the verdict component never counted its second radio")
	}
	if state := readFactFile(t, tree, "network/interfaces/wlan1/wireless/state"); state != string(machine.WirelessNoCarrier) {
		t.Errorf("the skipped radio reaches the facts tree as %q", state)
	}
}

func TestTheRadioComponentJoinsEachRadioOnlyOnce(t *testing.T) {
	// The plane restarts a component that panics. A second run would
	// start a second supplicant on a radio that already has one, and
	// send a second verdict nothing is counting. The component must
	// do nothing the second time.
	s := scripted(map[string]*connection{
		"wlan0": joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"),
	})
	radios := []machine.InterfaceSpec{declaredRadio("wlan0", "stonypoint")}
	pass := &radioPass{settled: make(chan *connection, 4), pending: 1}
	component := s.passes(routesVia("eth0")).joinRadios(pass, radios)

	if err := component(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := component(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(pass.settled) != 1 {
		t.Errorf("the component sent %d verdicts for one radio", len(pass.settled))
	}
	if _, joined, _, _ := s.asked(); strings.Join(joined, ",") != "wlan0" {
		t.Errorf("the component joined %v", joined)
	}
}

func TestTheConnectionsComeBackInSpecOrder(t *testing.T) {
	// resolv.conf keeps the first three nameservers in interface
	// order, and the facts summarize the first addressed interface. A
	// radio the spec named first must appear first, in both passes.
	interfaces := []machine.InterfaceSpec{declaredRadio("wlan0", "stonypoint"), wired("eth0")}

	background := scripted(map[string]*connection{
		"eth0":  addressed("eth0", "10.10.0.5/24"),
		"wlan0": joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"),
	})
	conns, pass := background.passes(routesVia("eth0")).run(interfaces, labCluster())
	if pass == nil {
		t.Fatal("a routed machine must background its radio")
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
		"wlan0": joinedRadio("wlan0", "stonypoint", "10.10.0.20/24"),
	})
	conns, pass = foreground.passes(routesNowhere()).run(interfaces, labCluster())
	if pass != nil {
		t.Fatal("an unrouted machine waits in the foreground")
	}
	if got := names(conns); got != "wlan0,eth0" {
		t.Errorf("the foreground pass returned %q, want the spec's order", got)
	}
}

// stalledRaise points the raise at a call that never returns, which is
// what the rtw88 driver did on 2026-08-26.
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
	// A raise that never returns must yield NotRaised with a message
	// that names the call, because the report is the whole remedy:
	// the stuck thread cannot be stopped, and the message is what a
	// person reads in kubectl get machine.
	stalledRaise(t)
	conn, err := bringUpRadio(declaredRadio("wlan0", "stonypoint"),
		[]interfaceIdentity{{name: "wlan0"}}, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if conn.radio.state != machine.WirelessNotRaised {
		t.Fatalf("state = %q", conn.radio.state)
	}
	// The message names the interface and says what is true: the call
	// did not come back. What holds it is not observable from here, so
	// the message must not name the lock as a fact.
	for _, want := range []string{"wlan0", "did not return"} {
		if !strings.Contains(conn.radio.message, want) {
			t.Errorf("the message must carry %q: %q", want, conn.radio.message)
		}
	}
	if strings.Contains(conn.radio.message, "holds the kernel's rtnl lock") {
		t.Errorf("the message states as fact something it cannot observe: %q", conn.radio.message)
	}
	if conn.addr != nil {
		t.Error("a link that never came up has no address")
	}
}

func TestARaiseThatFailsOutrightIsAnError(t *testing.T) {
	// A refusal the kernel returns is a plain error, not the wedge:
	// it must surface as one and never be dressed as NotRaised.
	origSetUp, origByName := linkSetUp, linkByName
	linkSetUp = func(netlink.Link) error { return fmt.Errorf("operation not permitted") }
	linkByName = func(name string) (netlink.Link, error) {
		return &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: name}}, nil
	}
	t.Cleanup(func() { linkSetUp, linkByName = origSetUp, origByName })

	if _, err := bringUpRadio(declaredRadio("wlan0", "stonypoint"),
		[]interfaceIdentity{{name: "wlan0"}}, time.Second); err == nil {
		t.Fatal("expected the kernel's refusal to surface")
	}
}

// raisesCleanly points the two netlink calls at stand-ins that
// answer at once, for the tests whose subject is what happens after
// the link is up.
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
	// The join is the fact the supplicant reported, and the address
	// step failing does not undo it. Reporting NoCarrier here would
	// tell a person the radio never reached the network, which is the
	// opposite of what happened, and would send them to the access
	// point instead of to the address in the manifest.
	raisesCleanly(t)
	orig := joinRadio
	joinRadio = func(ifc machine.InterfaceSpec, stateRoot string) *radio {
		return &radio{ifname: ifc.Name, ssid: ifc.Wireless.SSID, state: machine.WirelessConnected}
	}
	t.Cleanup(func() { joinRadio = orig })

	ifc := declaredRadio("wlan0", "stonypoint")
	ifc.Address = "not-a-cidr"
	conn, err := bringUpRadio(ifc, []interfaceIdentity{{name: "wlan0"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if conn.radio.state != machine.WirelessConnected {
		t.Errorf("the join's verdict is %q, want Connected", conn.radio.state)
	}
	if conn.addr != nil {
		t.Error("an interface whose addressing failed holds no address")
	}
	if !strings.Contains(conn.radio.message, "not-a-cidr") {
		t.Errorf("the addressing error must reach status: %q", conn.radio.message)
	}
}

// aimResolvConf points the resolver file at a tempdir.
func aimResolvConf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolv.conf")
	orig := resolvConfPath
	resolvConfPath = path
	t.Cleanup(func() { resolvConfPath = orig })
	return path
}

// withNameserver gives a connection one nameserver, the fact that
// resolv.conf is rendered from.
func withNameserver(conn *connection, address string) *connection {
	conn.nameservers = []net.IP{net.ParseIP(address)}
	return conn
}

func TestALateRadioReachesTheFactsTreeAndResolvConf(t *testing.T) {
	// Console parity for pass two: whatever the background join
	// decides must reach the facts tree, and through it the Machine
	// status, and the late nameservers must join resolv.conf. This
	// drives the verdict component directly, with the channel
	// already holding a settled radio.
	tree := machine.FactsTree{Dir: filepath.Join(t.TempDir(), "facts")}
	resolv := aimResolvConf(t)

	pass := &radioPass{
		conns: []*connection{
			withNameserver(addressed("eth0", "10.10.0.5/24"), "10.10.0.1"),
			pendingRadio(declaredRadio("wlan0", "stonypoint")),
		},
		settled: make(chan *connection, 1),
		pending: 1,
	}
	pass.settled <- withNameserver(joinedRadio("wlan0", "stonypoint", "192.168.1.20/24"), "192.168.1.1")

	if err := publishRadioVerdicts(pass, tree, nil)(context.Background()); err != nil {
		t.Fatal(err)
	}

	state := readFactFile(t, tree, "network/interfaces/wlan0/wireless/state")
	if state != string(machine.WirelessConnected) {
		t.Errorf("the facts tree says %q", state)
	}
	if address := readFactFile(t, tree, "network/interfaces/wlan0/address"); address != "192.168.1.20/24" {
		t.Errorf("the facts tree says %q", address)
	}
	rendered, err := os.ReadFile(resolv)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nameserver 10.10.0.1", "nameserver 192.168.1.1"} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("resolv.conf must carry %q: %q", want, rendered)
		}
	}
}

// readFactFile reads one file out of a facts tree.
func readFactFile(t *testing.T, tree machine.FactsTree, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(tree.Dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}
