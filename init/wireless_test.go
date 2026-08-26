package main

// The park decision is the one rule in this milestone that a person
// cannot check by looking at a machine, because it fires only when a
// radio has already failed. So it is a pure function over the
// connections and one route question, and every case the plan names
// has a test here.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
)

// addressed is an interface that came up and holds an address.
func addressed(name, cidr string) *connection {
	ip, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return &connection{ifname: name, addr: &net.IPNet{IP: ip, Mask: subnet.Mask}}
}

// refusedRadio is an interface whose radio failed for a reason that no
// amount of waiting corrects.
func refusedRadio(name, ssid string) *connection {
	return &connection{ifname: name, radio: &radio{
		ifname: name, ssid: ssid, state: machine.WirelessWrongKey,
		message: "the access point refused the passphrase (WRONG_KEY)",
	}}
}

// silentRadio is an interface whose radio heard nothing. The plan's
// rule is that this never parks a boot, however long it lasts.
func silentRadio(name, ssid string) *connection {
	return &connection{ifname: name, radio: &radio{
		ifname: name, ssid: ssid, state: machine.WirelessNoCarrier,
		message: "no access point answered",
	}}
}

// routesVia answers every route question with one interface, which is
// all the park decision asks.
func routesVia(name string) routeLookup {
	return func(net.IP) (string, error) { return name, nil }
}

// routesNowhere is a machine with no route toward the endpoint.
func routesNowhere() routeLookup {
	return func(dst net.IP) (string, error) { return "", fmt.Errorf("no route toward %s", dst) }
}

const labEndpoint = "https://10.10.0.1:6443"

func TestParkDecisionLetsAWiredMachineBoot(t *testing.T) {
	// The drill's machine: the radio refuses, the ethernet port
	// carries the cluster, and the boot goes on degraded.
	conns := []*connection{addressed("eth0", "10.10.0.5/24"), refusedRadio("wlan0", "stonypoint")}
	failed, reason := parkDecision(conns, labEndpoint, routesVia("eth0"))
	if failed != nil {
		t.Errorf("a machine with a route to its cluster must boot: %s", reason)
	}
}

func TestParkDecisionHoldsAMachineWithNoOtherPath(t *testing.T) {
	// The field case: a stick PC whose only interface is the radio.
	conns := []*connection{refusedRadio("wlan0", "stonypoint")}
	failed, reason := parkDecision(conns, labEndpoint, routesVia("eth0"))
	if failed == nil {
		t.Fatal("a machine whose only path refused must wait")
	}
	for _, want := range []string{"wlan0", "stonypoint", "WRONG_KEY", "waits here"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason must carry %q: %q", want, reason)
		}
	}
}

func TestParkDecisionHoldsWhenNothingRoutesToTheCluster(t *testing.T) {
	// An interface came up, and it reaches nothing the cluster is on.
	conns := []*connection{addressed("eth0", "192.168.9.5/24"), refusedRadio("wlan0", "stonypoint")}
	failed, reason := parkDecision(conns, labEndpoint, routesNowhere())
	if failed == nil {
		t.Fatal("no route toward the endpoint must wait")
	}
	if !strings.Contains(reason, "10.10.0.1") {
		t.Errorf("the reason must name the endpoint: %q", reason)
	}
}

func TestParkDecisionHoldsWhenTheRouteLeavesByAnInterfaceThatNeverCameUp(t *testing.T) {
	conns := []*connection{addressed("eth0", "192.168.9.5/24"), refusedRadio("wlan0", "stonypoint")}
	failed, reason := parkDecision(conns, labEndpoint, routesVia("eth1"))
	if failed == nil {
		t.Fatal("a route by an interface with no address must wait")
	}
	if !strings.Contains(reason, "eth1") {
		t.Errorf("the reason must name the interface the route leaves by: %q", reason)
	}
}

func TestParkDecisionLetsALeaderBoot(t *testing.T) {
	// A leader's endpoint is its own address, so the kernel answers
	// loopback. A leader must never wait on a radio it does not need.
	conns := []*connection{addressed("eth0", "10.10.0.1/24"), refusedRadio("wlan0", "stonypoint")}
	if failed, reason := parkDecision(conns, labEndpoint, routesVia("lo")); failed != nil {
		t.Errorf("a leader must boot: %s", reason)
	}
}

func TestParkDecisionNeverHoldsOnAbsence(t *testing.T) {
	// The plan's hardest rule: an access point that is off, rebooting,
	// or out of range must never stop a machine from booting.
	conns := []*connection{silentRadio("wlan0", "stonypoint")}
	if failed, reason := parkDecision(conns, labEndpoint, routesNowhere()); failed != nil {
		t.Errorf("absence must never park: %s", reason)
	}
}

func TestParkDecisionLetsAMachineWithNoEndpointBoot(t *testing.T) {
	// A machine alone is its own cluster. There is no endpoint to
	// route toward, and an interface did come up.
	conns := []*connection{addressed("eth0", "10.10.0.5/24"), refusedRadio("wlan0", "stonypoint")}
	if failed, reason := parkDecision(conns, "", routesNowhere()); failed != nil {
		t.Errorf("a machine with no endpoint must boot: %s", reason)
	}
}

func TestParkDecisionLetsAMachineWithANamedEndpointBoot(t *testing.T) {
	// A name needs DNS, and DNS needs the network in question. The
	// decision refuses to guess, and the bias is to boot.
	conns := []*connection{addressed("eth0", "10.10.0.5/24"), refusedRadio("wlan0", "stonypoint")}
	if failed, reason := parkDecision(conns, "https://cluster.example.com:6443", routesNowhere()); failed != nil {
		t.Errorf("a named endpoint must not park a machine that came up: %s", reason)
	}
}

func TestParkDecisionOnAWiredMachineAsksNothing(t *testing.T) {
	conns := []*connection{addressed("eth0", "10.10.0.5/24")}
	if failed, _ := parkDecision(conns, labEndpoint, routesNowhere()); failed != nil {
		t.Error("a machine with no radio can never park")
	}
}

func TestEndpointAddressReadsTheLiteralAddress(t *testing.T) {
	ip, ok := endpointAddress("https://10.10.0.1:6443")
	if !ok || !ip.Equal(net.ParseIP("10.10.0.1")) {
		t.Errorf("got %v, %v", ip, ok)
	}
}

func TestEndpointAddressRefusesWhatItCannotResolve(t *testing.T) {
	for _, endpoint := range []string{"", "https://cluster.example.com:6443", "::::"} {
		if _, ok := endpointAddress(endpoint); ok {
			t.Errorf("%q names no literal address", endpoint)
		}
	}
}

func TestAnyAddressedSeparatesAPathFromAReport(t *testing.T) {
	if anyAddressed([]*connection{refusedRadio("wlan0", "stonypoint")}) {
		t.Error("an interface with no address is a report, not a path")
	}
	if !anyAddressed([]*connection{refusedRadio("wlan0", "x"), addressed("eth0", "10.10.0.5/24")}) {
		t.Error("an addressed interface is a path")
	}
}

func TestRadioReportsItsStatusForTheFactsTree(t *testing.T) {
	r := &radio{ifname: "wlan0", ssid: "stonypoint", state: machine.WirelessConnected}
	got := r.wirelessStatus()
	if got.SSID != "stonypoint" || got.State != machine.WirelessConnected {
		t.Errorf("got %+v", got)
	}
}

func TestOnlyAWrongKeyIsDeterministic(t *testing.T) {
	for state, want := range map[machine.WirelessState]bool{
		machine.WirelessWrongKey:    true,
		machine.WirelessNoCarrier:   false,
		machine.WirelessAssociating: false,
		machine.WirelessConnected:   false,
	} {
		if got := (&radio{state: state}).deterministic(); got != want {
			t.Errorf("%s: deterministic = %v, want %v", state, got, want)
		}
	}
}

// stubControl is a control client whose events a test pushes by hand,
// so the wait and the park run with no supplicant and no radio.
func stubControl() *wpaControl {
	return &wpaControl{out: make(chan wpaEvent, 8)}
}

// pushEvent puts one of the supplicant's own lines onto a stub client.
func pushEvent(t *testing.T, c *wpaControl, message string) {
	t.Helper()
	event, ok := parseWPAEvent(message)
	if !ok {
		t.Fatalf("%q must parse", message)
	}
	c.out <- event
}

func TestAwaitAssociationEndsOnTheJoin(t *testing.T) {
	r := &radio{ifname: "wlan0", ssid: "stonypoint", state: machine.WirelessAssociating, control: stubControl()}
	pushEvent(t, r.control, "<3>CTRL-EVENT-SCAN-RESULTS ")
	pushEvent(t, r.control, "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]")

	awaitAssociation(r, time.Second)
	if r.state != machine.WirelessConnected {
		t.Errorf("state = %q, message = %q", r.state, r.message)
	}
}

func TestAwaitAssociationEndsOnARefusedPassphrase(t *testing.T) {
	r := &radio{ifname: "wlan0", ssid: "stonypoint", state: machine.WirelessAssociating, control: stubControl()}
	pushEvent(t, r.control, `<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="stonypoint" auth_failures=1 duration=10 reason=WRONG_KEY`)

	awaitAssociation(r, time.Second)
	if r.state != machine.WirelessWrongKey || !r.deterministic() {
		t.Errorf("state = %q", r.state)
	}
}

func TestAwaitAssociationGivesUpWithoutCallingItAWrongKey(t *testing.T) {
	// An access point that never answers is the case the plan says must
	// never park a boot, so this wait must end in NoCarrier.
	r := &radio{ifname: "wlan0", ssid: "stonypoint", state: machine.WirelessAssociating, control: stubControl()}
	pushEvent(t, r.control, "<3>CTRL-EVENT-NETWORK-NOT-FOUND ")

	awaitAssociation(r, 50*time.Millisecond)
	if r.state != machine.WirelessNoCarrier || r.deterministic() {
		t.Errorf("state = %q, message = %q", r.state, r.message)
	}
}

func TestAwaitAssociationEndsWhenTheSupplicantsStreamCloses(t *testing.T) {
	r := &radio{ifname: "wlan0", ssid: "stonypoint", state: machine.WirelessAssociating, control: stubControl()}
	close(r.control.out)

	awaitAssociation(r, time.Second)
	if r.state != machine.WirelessNoCarrier {
		t.Errorf("state = %q", r.state)
	}
}

// heldConsole points a park at a file, so a test can read what a person
// at the machine would have seen.
func heldConsole(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "console")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	orig := parkConsole
	parkConsole = path
	t.Cleanup(func() { parkConsole = orig })
	return path
}

func TestParkReleasesTheBootWhenTheRadioJoins(t *testing.T) {
	// The park is a hold, not a halt: a fix on the network side must
	// resume the boot with nobody at the machine.
	console := heldConsole(t)
	r := &radio{
		ifname: "wlan0", ssid: "stonypoint", state: machine.WirelessWrongKey,
		message: "the access point refused the passphrase (WRONG_KEY)", control: stubControl(),
	}
	pushEvent(t, r.control, "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]")

	park(r, "liken: wireless: wlan0 cannot join stonypoint")

	if r.state != machine.WirelessConnected {
		t.Errorf("state = %q", r.state)
	}
	held, err := os.ReadFile(console)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(held), "cannot join stonypoint") {
		t.Errorf("the console must carry the reason: %q", held)
	}
}

func TestParkEndsWhenTheSupplicantsStreamCloses(t *testing.T) {
	// Nothing else can release this hold, so a stream that ends must,
	// rather than leaving a machine stopped with no way to report why.
	heldConsole(t)
	r := &radio{
		ifname: "wlan0", ssid: "stonypoint", state: machine.WirelessWrongKey,
		message: "refused", control: stubControl(),
	}
	close(r.control.out)
	park(r, "liken: wireless: wlan0 cannot join stonypoint")
}

func TestWriteWirelessConfigKeepsThePassphraseToItself(t *testing.T) {
	// The file holds the network's passphrase, so its mode is part of
	// the design and not an accident of the umask.
	orig := wirelessRunDir
	wirelessRunDir = filepath.Join(t.TempDir(), "wireless")
	t.Cleanup(func() { wirelessRunDir = orig })

	path, err := writeWirelessConfig("wlan0", "network={\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestControlSocketDirIsShortEnoughForAUnixSocket(t *testing.T) {
	// The supplicant refuses a control path longer than the kernel's
	// sun_path, and the refusal is a boot that never joins.
	const sunPathMax = 108
	socket := filepath.Join(controlSocketDir("wlp0s20f3"), "wlp0s20f3")
	if len(socket) >= sunPathMax {
		t.Errorf("%q is %d bytes; a UNIX socket path holds %d", socket, len(socket), sunPathMax)
	}
}
