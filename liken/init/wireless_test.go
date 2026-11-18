package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

func addressed(name, cidr string) *connection {
	ip, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return &connection{ifname: name, addr: &net.IPNet{IP: ip, Mask: subnet.Mask}}
}

// refusedRadio returns a connection whose access point refused the
// passphrase, a deterministic failure.
func refusedRadio(name, ssid string) *connection {
	return &connection{ifname: name, radio: &radio{
		ifname: name, ssid: ssid, state: machine.WirelessWrongKey,
		message: "the access point refused the passphrase (WRONG_KEY)",
	}}
}

// silentRadio returns a connection whose access point never answered,
// a failure that is not deterministic.
func silentRadio(name, ssid string) *connection {
	return &connection{ifname: name, radio: &radio{
		ifname: name, ssid: ssid, state: machine.WirelessNoCarrier,
		message: "no access point answered",
	}}
}

// routesVia returns a routeLookup that routes every address through
// the named interface.
func routesVia(name string) routeLookup {
	return func(net.IP) (string, error) { return name, nil }
}

func routesNowhere() routeLookup {
	return func(dst net.IP) (string, error) { return "", fmt.Errorf("no route toward %s", dst) }
}

const labEndpoint = "https://10.10.0.1:6443"

func TestRadioToWaitForLetsAWiredMachineBoot(t *testing.T) {
	conns := []*connection{addressed("eth0", "10.10.0.5/24"), refusedRadio("wlan0", "homenet")}
	failed, reason := radioToWaitFor(conns, labEndpoint, routesVia("eth0"))
	if failed != nil {
		t.Errorf("radioToWaitFor chose a radio to wait for (%s), want none when a route reaches the cluster", reason)
	}
}

func TestRadioToWaitForHoldsAMachineWithNoOtherPath(t *testing.T) {
	// The radio is the machine's only interface.
	conns := []*connection{refusedRadio("wlan0", "homenet")}
	failed, reason := radioToWaitFor(conns, labEndpoint, routesVia("eth0"))
	if failed == nil {
		t.Fatal("radioToWaitFor returns no radio, want the radio that is the only interface")
	}
	for _, want := range []string{"wlan0", "homenet", "WRONG_KEY"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason is %q, want it to contain %q", reason, want)
		}
	}
}

func TestRadioToWaitForHoldsWhenNothingRoutesToTheCluster(t *testing.T) {
	conns := []*connection{addressed("eth0", "192.168.9.5/24"), refusedRadio("wlan0", "homenet")}
	failed, reason := radioToWaitFor(conns, labEndpoint, routesNowhere())
	if failed == nil {
		t.Fatal("radioToWaitFor returns no radio, want one because no route reaches the endpoint")
	}
	if !strings.Contains(reason, "10.10.0.1") {
		t.Errorf("the reason is %q, want it to contain the endpoint 10.10.0.1", reason)
	}
}

func TestRadioToWaitForHoldsWhenTheRouteLeavesByAnInterfaceThatNeverCameUp(t *testing.T) {
	conns := []*connection{addressed("eth0", "192.168.9.5/24"), refusedRadio("wlan0", "homenet")}
	failed, reason := radioToWaitFor(conns, labEndpoint, routesVia("eth1"))
	if failed == nil {
		t.Fatal("radioToWaitFor returns no radio, want one because the route leaves by eth1, which has no address")
	}
	if !strings.Contains(reason, "eth1") {
		t.Errorf("the reason is %q, want it to contain eth1", reason)
	}
}

func TestRadioToWaitForLetsALeaderBoot(t *testing.T) {
	// A leader's endpoint is its own address, so the route leaves by
	// loopback.
	conns := []*connection{addressed("eth0", "10.10.0.1/24"), refusedRadio("wlan0", "homenet")}
	if failed, reason := radioToWaitFor(conns, labEndpoint, routesVia("lo")); failed != nil {
		t.Errorf("radioToWaitFor chose a radio to wait for (%s), want none on a leader, whose route to the endpoint leaves by lo", reason)
	}
}

func TestRadioToWaitForNeverHoldsOnAbsence(t *testing.T) {
	// An access point that is off, rebooting, or out of range must not
	// stop a machine from booting.
	conns := []*connection{silentRadio("wlan0", "homenet")}
	if failed, reason := radioToWaitFor(conns, labEndpoint, routesNowhere()); failed != nil {
		t.Errorf("radioToWaitFor chose a radio to wait for (%s), want none for a radio that only finds no access point", reason)
	}
}

func TestRadioToWaitForLetsAMachineWithNoEndpointBoot(t *testing.T) {
	conns := []*connection{addressed("eth0", "10.10.0.5/24"), refusedRadio("wlan0", "homenet")}
	if failed, reason := radioToWaitFor(conns, "", routesNowhere()); failed != nil {
		t.Errorf("radioToWaitFor chose a radio to wait for (%s), want none on a machine with no endpoint", reason)
	}
}

func TestRadioToWaitForLetsAMachineWithAHostNameEndpointBoot(t *testing.T) {
	conns := []*connection{addressed("eth0", "10.10.0.5/24"), refusedRadio("wlan0", "homenet")}
	if failed, reason := radioToWaitFor(conns, "https://cluster.example.com:6443", routesNowhere()); failed != nil {
		t.Errorf("radioToWaitFor chose a radio to wait for (%s), want none when the endpoint is a host name", reason)
	}
}

func TestRadioToWaitForNeverHoldsAMachineWithNoRadio(t *testing.T) {
	conns := []*connection{addressed("eth0", "10.10.0.5/24")}
	if failed, _ := radioToWaitFor(conns, labEndpoint, routesNowhere()); failed != nil {
		t.Error("radioToWaitFor returns a radio, want nil for a machine with no radio")
	}
}

func TestEndpointAddressReadsTheLiteralAddress(t *testing.T) {
	ip, ok := endpointAddress("https://10.10.0.1:6443")
	if !ok || !ip.Equal(net.ParseIP("10.10.0.1")) {
		t.Errorf("endpointAddress returned %v, %v, want an address and true", ip, ok)
	}
}

func TestEndpointAddressRejectsAnEmptyOrNamedEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "https://cluster.example.com:6443", "::::"} {
		if _, ok := endpointAddress(endpoint); ok {
			t.Errorf("endpointAddress(%q) returned an address, want none", endpoint)
		}
	}
}

func TestAnyAddressedIgnoresInterfacesWithoutAnAddress(t *testing.T) {
	if anyAddressed([]*connection{refusedRadio("wlan0", "homenet")}) {
		t.Error("anyAddressed is true for an interface with no address, want false")
	}
	if !anyAddressed([]*connection{refusedRadio("wlan0", "x"), addressed("eth0", "10.10.0.5/24")}) {
		t.Error("anyAddressed is false with an addressed interface, want true")
	}
}

func TestRadioWirelessStatusCopiesSSIDAndState(t *testing.T) {
	r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessConnected}
	got := r.wirelessStatus()
	if got.SSID != "homenet" || got.State != machine.WirelessConnected {
		t.Errorf("wirelessStatus is %+v, want SSID homenet and state Connected", got)
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

// stubControl returns a control client with no socket. pushEvent
// queues events on it.
func stubControl() *wpaControl {
	return &wpaControl{out: make(chan wpaEvent, 8)}
}

// pushEvent parses a supplicant message and queues it as an event on c.
func pushEvent(t *testing.T, c *wpaControl, message string) {
	t.Helper()
	event, ok := parseWPAEvent(message)
	if !ok {
		t.Fatalf("parsing the supplicant message %q: no event", message)
	}
	c.out <- event
}

func TestAwaitAssociationEndsOnTheJoin(t *testing.T) {
	r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessAssociating, control: stubControl()}
	pushEvent(t, r.control, "<3>CTRL-EVENT-SCAN-RESULTS ")
	pushEvent(t, r.control, "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]")

	awaitAssociation(r)
	if r.state != machine.WirelessConnected {
		t.Errorf("state is %q (%q), want Connected", r.state, r.message)
	}
}

func TestAwaitAssociationEndsOnARefusedPassphrase(t *testing.T) {
	r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessAssociating, control: stubControl()}
	pushEvent(t, r.control, `<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=1 duration=10 reason=WRONG_KEY`)

	awaitAssociation(r)
	if r.state != machine.WirelessWrongKey || !r.deterministic() {
		t.Errorf("state is %q, want WrongKey", r.state)
	}
}

func TestAwaitAssociationTimesOutAsNoCarrier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessAssociating, control: stubControl()}
		pushEvent(t, r.control, "<3>CTRL-EVENT-NETWORK-NOT-FOUND ")

		start := time.Now()
		awaitAssociation(r)
		if r.state != machine.WirelessNoCarrier || r.deterministic() {
			t.Errorf("state is %q (%q), want NoCarrier", r.state, r.message)
		}
		if elapsed := time.Since(start); elapsed != associationTimeout {
			t.Errorf("the wait gave up after %s, want its timeout of %s", elapsed, associationTimeout)
		}
	})
}

func TestAwaitAssociationEndsWhenTheSupplicantsStreamCloses(t *testing.T) {
	r := &radio{ifname: "wlan0", ssid: "homenet", state: machine.WirelessAssociating, control: stubControl()}
	close(r.control.out)

	awaitAssociation(r)
	if r.state != machine.WirelessNoCarrier {
		t.Errorf("state is %q, want NoCarrier", r.state)
	}
}

// heldConsole points radioWaitConsole at a temporary file and returns
// its path.
func heldConsole(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "console")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("creating the console file: %v", err)
	}
	orig := radioWaitConsole
	radioWaitConsole = path
	t.Cleanup(func() { radioWaitConsole = orig })
	return path
}

func TestWaitForRadioReturnsWhenTheRadioJoins(t *testing.T) {
	// waitForRadio starts on a refused passphrase, so a second refusal
	// must not end the wait. Only the join does.
	console := heldConsole(t)
	r := &radio{
		ifname: "wlan0", ssid: "homenet", state: machine.WirelessWrongKey,
		message: "the access point refused the passphrase (WRONG_KEY)", control: stubControl(),
	}
	pushEvent(t, r.control, "<3>CTRL-EVENT-SCAN-RESULTS ")
	pushEvent(t, r.control, `<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=2 duration=20 reason=WRONG_KEY`)
	pushEvent(t, r.control, "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]")

	waitForRadio(r, "liken: wireless: wlan0 cannot join homenet")

	if r.state != machine.WirelessConnected {
		t.Errorf("state is %q, want Connected", r.state)
	}
	held, err := os.ReadFile(console)
	if err != nil {
		t.Fatalf("reading the console file: %v", err)
	}
	if !strings.Contains(string(held), "cannot join homenet") {
		t.Errorf("the console file is %q, want it to contain the reason", held)
	}
}

func TestWaitForRadioEndsWhenTheSupplicantsStreamCloses(t *testing.T) {
	heldConsole(t)
	r := &radio{
		ifname: "wlan0", ssid: "homenet", state: machine.WirelessWrongKey,
		message: "refused", control: stubControl(),
	}
	close(r.control.out)
	waitForRadio(r, "liken: wireless: wlan0 cannot join homenet")
}

func TestWriteWirelessConfigIsOwnerReadableOnly(t *testing.T) {
	orig := wirelessRunDir
	wirelessRunDir = filepath.Join(t.TempDir(), "wireless")
	t.Cleanup(func() { wirelessRunDir = orig })

	path, err := writeWirelessConfig("wlan0", "network={\n}\n")
	if err != nil {
		t.Fatalf("writing the supplicant configuration: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the mode of the configuration file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestControlSocketDirIsShortEnoughForAUnixSocket(t *testing.T) {
	// A UNIX socket path must fit in the 108 bytes of sockaddr_un's
	// sun_path on Linux, or the supplicant cannot create its socket.
	const sunPathMax = 108
	socket := filepath.Join(controlSocketDir("wlp0s20f3"), "wlp0s20f3")
	if len(socket) >= sunPathMax {
		t.Errorf("the control socket path %q is %d bytes, want fewer than the %d of sun_path", socket, len(socket), sunPathMax)
	}
}
