package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/machine/supplicanttest"
)

// The near and far access points of one network, with the STATUS
// reply that the supplicant gives when connected to each.
const (
	nearAP       = "04:4a:2c:11:22:33"
	farAP        = "04:4a:2c:44:55:66"
	statusNear   = "bssid=" + nearAP + "\nfreq=5180\nssid=homenet\nwpa_state=COMPLETED\n"
	statusFar    = "bssid=" + farAP + "\nfreq=2437\nssid=homenet\nwpa_state=COMPLETED\n"
	statusNearCh = "bssid=" + nearAP + "\nfreq=5745\nssid=homenet\nwpa_state=COMPLETED\n"
	statusLost   = "wpa_state=SCANNING\n"
)

var (
	boot      = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	watchTime = boot.Add(time.Hour)
)

// watchedRadio starts a fake supplicant for wlan0 in a temporary
// wirelessRunDir, and returns a connection whose radio is attached to
// it and starts in the given state.
func watchedRadio(t *testing.T, current radio) (*connection, *supplicanttest.Supplicant) {
	t.Helper()
	dir := t.TempDir()
	orig := wirelessRunDir
	wirelessRunDir = dir
	t.Cleanup(func() { wirelessRunDir = orig })

	s := supplicanttest.Start(t, dir, "wlan0")
	control, err := attachWPAControl(s.Socket)
	if err != nil {
		t.Fatalf("attaching to the fake supplicant: %v", err)
	}
	t.Cleanup(control.close)
	<-control.events() // the wpaAttached event

	conn := addressed("wlan0", "192.168.1.20/24")
	current.ifname, current.ssid, current.control = "wlan0", "homenet", control
	conn.radio = &current
	return conn, s
}

// onNear is a radio associated with the near access point since boot.
var onNear = radio{
	state: machine.WirelessConnected, bssid: nearAP, frequency: 5180, associatedAt: boot,
}

// event parses one event the way it arrives on the socket.
func event(t *testing.T, message string) wpaEvent {
	t.Helper()
	e, ok := parseWPAEvent(message)
	if !ok {
		t.Fatalf("parseWPAEvent(%q) does not parse, want an event", message)
	}
	return e
}

func TestTheRadioWatchFollowsTheSupplicant(t *testing.T) {
	cases := []struct {
		name    string
		current radio
		status  string
		event   string
		want    machine.WirelessStatus
	}{
		{
			name: "a move to another access point", current: onNear, status: statusFar,
			event: "<3>CTRL-EVENT-CONNECTED - Connection to " + farAP + " completed [id=0 id_str=]",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessConnected,
				BSSID: farAP, Frequency: 2437, Band: "2.4GHz", AssociatedAt: &watchTime,
			},
		},
		{
			name:    "a new channel on the same access point keeps the association",
			current: onNear, status: statusNearCh,
			event: "<3>CTRL-EVENT-CONNECTED - Connection to " + nearAP + " completed [id=0 id_str=]",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessConnected,
				BSSID: nearAP, Frequency: 5745, Band: "5GHz", AssociatedAt: &boot,
			},
		},
		{
			name: "a lost access point", current: onNear, status: statusLost,
			event: "<3>CTRL-EVENT-DISCONNECTED bssid=" + nearAP + " reason=4 locally_generated=1",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessAssociating,
				Message:              "lost " + nearAP + " (reason 4, disassociated because of inactivity, sent by this machine), and the supplicant is joining again",
				LastDisconnectReason: 4,
			},
		},
		{
			name: "a rejoin of the same access point after a loss",
			current: radio{
				state: machine.WirelessAssociating, message: "lost " + nearAP + " (reason 4)", lastDisconnectReason: 4,
			},
			status: statusNear,
			event:  "<3>CTRL-EVENT-CONNECTED - Connection to " + nearAP + " completed [id=0 id_str=]",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessConnected,
				BSSID: nearAP, Frequency: 5180, Band: "5GHz", AssociatedAt: &watchTime,
				LastDisconnectReason: 4,
			},
		},
		{
			name:    "an attach after a restart reads the association again",
			current: radio{state: machine.WirelessAssociating}, status: statusNear,
			event: wpaAttached,
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessConnected,
				BSSID: nearAP, Frequency: 5180, Band: "5GHz", AssociatedAt: &watchTime,
			},
		},
		{
			name:    "a passphrase the access point refuses after the join",
			current: onNear, status: statusLost,
			event: "<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid=\"homenet\" auth_failures=1 duration=10 reason=WRONG_KEY",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessWrongKey,
				Message: deterministicWPAReasons["WRONG_KEY"],
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, s := watchedRadio(t, c.current)
			s.Answer("STATUS", c.status)
			w := &radioWatch{conns: []*connection{conn}, now: func() time.Time { return watchTime }}

			if !w.apply(radioEvent{ifname: "wlan0", event: event(t, c.event)}) {
				t.Fatal("apply reported no change, want a change of the radio status")
			}
			if got := w.conns[0].radio.wirelessStatus(); !reflect.DeepEqual(got, &c.want) {
				t.Errorf("the radio status is\n%+v\nwant\n%+v", got, &c.want)
			}
		})
	}
}

func TestTheRadioWatchIgnoresWhatChangesNothing(t *testing.T) {
	cases := []struct {
		name    string
		current radio
		status  string
		event   string
	}{
		{
			name: "a connect to the access point already in status", current: onNear, status: statusNear,
			event: "<3>CTRL-EVENT-CONNECTED - Connection to " + nearAP + " completed [id=0 id_str=]",
		},
		{
			name:    "a disconnect of a radio that was not connected",
			current: radio{state: machine.WirelessNoCarrier}, status: statusLost,
			event: "<3>CTRL-EVENT-DISCONNECTED bssid=" + nearAP + " reason=15",
		},
		{
			name: "a scan", current: onNear, status: statusNear,
			event: "<3>CTRL-EVENT-SCAN-RESULTS ",
		},
		{
			name: "a signal change", current: onNear, status: statusNear,
			event: "<3>CTRL-EVENT-SIGNAL-CHANGE above=0 signal=-74 noise=9999 txrate=144400",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, s := watchedRadio(t, c.current)
			s.Answer("STATUS", c.status)
			w := &radioWatch{conns: []*connection{conn}, now: func() time.Time { return watchTime }}

			if w.apply(radioEvent{ifname: "wlan0", event: event(t, c.event)}) {
				t.Errorf("apply reported a change to %+v, want no change", w.conns[0].radio.wirelessStatus())
			}
		})
	}
}

func TestTheRadioWatchLeavesTheBootsListAlone(t *testing.T) {
	// Other readers use the boot's list while the watch runs, so the
	// watch must not change the radios in it.
	conn, s := watchedRadio(t, onNear)
	s.Answer("STATUS", statusFar)
	w := &radioWatch{conns: []*connection{conn}, now: func() time.Time { return watchTime }}
	w.apply(radioEvent{ifname: "wlan0", event: event(t, "<3>CTRL-EVENT-CONNECTED - Connection to "+farAP+" completed")})
	if conn.radio.bssid != nearAP {
		t.Errorf("the radio in the boot's list is on %s, want %s", conn.radio.bssid, nearAP)
	}
}

func TestTheRadioWatchPublishesTheBaselineAndEndsWhenTheStreamCloses(t *testing.T) {
	// The radio joined at boot and is on the near access point. The
	// watch sends STATUS once before it reads any event, publishes the
	// association, and ends when the stream closes. The stream closes
	// before the watch starts, as it does when the boot shuts down, and
	// the baseline is still published, because STATUS uses its own
	// socket.
	conn, s := watchedRadio(t, radio{state: machine.WirelessConnected})
	s.Answer("STATUS", statusNear)
	tree := machine.FactsTree{Dir: filepath.Join(t.TempDir(), "facts")}
	conn.radio.control.close()

	if err := watchRadios([]*connection{conn}, nil, nil, tree, nil)(context.Background()); err != nil {
		t.Fatalf("running the radio watch: %v", err)
	}

	status, err := tree.Read()
	if err != nil {
		t.Fatalf("reading the facts: %v", err)
	}
	want := &machine.WirelessStatus{
		SSID: "homenet", State: machine.WirelessConnected,
		BSSID: nearAP, Frequency: 5180, Band: "5GHz",
	}
	got := *status.Network.Interfaces[0].Wireless
	if got.AssociatedAt == nil {
		t.Error("the facts have no associatedAt, want the time the radio associated")
	}
	got.AssociatedAt = nil
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("the wireless facts are\n%+v\nwant\n%+v", &got, want)
	}
}

// lateRadio is a radio whose first join failed, so the boot gave its
// interface no address, with a watch that addresses through address.
func lateRadio(t *testing.T, address func(machine.InterfaceSpec) (*connection, error)) *radioWatch {
	t.Helper()
	conn, s := watchedRadio(t, radio{state: machine.WirelessAssociating})
	conn.addr = nil
	s.Answer("STATUS", statusNear)
	return &radioWatch{
		conns: []*connection{conn}, now: func() time.Time { return watchTime },
		interfaces: []machine.InterfaceSpec{declaredRadio("wlan0", "homenet")},
		address:    address,
	}
}

func TestARadioThatJoinsLateGetsAnAddress(t *testing.T) {
	resolv := useResolvConf(t)
	w := lateRadio(t, func(ifc machine.InterfaceSpec) (*connection, error) {
		return withNameserver(addressed(ifc.Name, "192.168.1.20/24"), "192.168.1.1"), nil
	})

	w.apply(radioEvent{ifname: "wlan0", event: event(t, "<3>CTRL-EVENT-CONNECTED - Connection to "+nearAP+" completed")})

	got := w.conns[0]
	if got.addr == nil || got.addr.String() != "192.168.1.20/24" {
		t.Errorf("the connection address is %v, want 192.168.1.20/24", got.addr)
	}
	if got.radio.state != machine.WirelessConnected || got.radio.bssid != nearAP {
		t.Errorf("the radio is %+v, want Connected on %s", got.radio.wirelessStatus(), nearAP)
	}
	written, err := os.ReadFile(resolv)
	if err != nil {
		t.Fatalf("reading resolv.conf: %v", err)
	}
	if !strings.Contains(string(written), "nameserver 192.168.1.1") {
		t.Errorf("resolv.conf is\n%s\nwant nameserver 192.168.1.1", written)
	}
}

func TestARadioThatJoinsLateAndFailsItsAddressingSaysWhy(t *testing.T) {
	useResolvConf(t)
	w := lateRadio(t, func(machine.InterfaceSpec) (*connection, error) {
		return nil, errors.New("no DHCP offer on wlan0 in 30s")
	})

	w.apply(radioEvent{ifname: "wlan0", event: event(t, "<3>CTRL-EVENT-CONNECTED - Connection to "+nearAP+" completed")})

	got := w.conns[0]
	if got.addr != nil {
		t.Errorf("the connection address is %v, want none because addressing failed", got.addr)
	}
	if got.radio.state != machine.WirelessConnected || got.radio.message != "no DHCP offer on wlan0 in 30s" {
		t.Errorf("the radio is %+v, want Connected with the addressing error as its message", got.radio.wirelessStatus())
	}
}

// pendingRetries replaces time.AfterFunc in a watch. It records each
// delay and keeps the function for the test to call.
type pendingRetries struct {
	delays []time.Duration
	fire   []func()
}

func (p *pendingRetries) after(d time.Duration, f func()) {
	p.delays = append(p.delays, d)
	p.fire = append(p.fire, f)
}

func TestTheRadioWatchCapsTheWaitAfterAnAccessPointRefusesTheJoin(t *testing.T) {
	cases := []struct {
		name  string
		event string
		want  []time.Duration
	}{
		{
			name:  "a refusal with a wait longer than the cap",
			event: `<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=4 duration=60 reason=CONN_FAILED`,
			want:  []time.Duration{wirelessRetryCap},
		},
		{
			name:  "a refusal with a wait inside the cap",
			event: `<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=2 duration=20 reason=CONN_FAILED`,
		},
		{
			name:  "a wrong passphrase",
			event: `<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=4 duration=60 reason=WRONG_KEY`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, s := watchedRadio(t, radio{state: machine.WirelessAssociating})
			s.Answer("STATUS", statusLost)
			retries := &pendingRetries{}
			w := &radioWatch{
				conns: []*connection{conn}, now: func() time.Time { return watchTime },
				events: make(chan radioEvent, 1), after: retries.after,
			}

			w.apply(radioEvent{ifname: "wlan0", event: event(t, c.event)})

			if !reflect.DeepEqual(retries.delays, c.want) {
				t.Errorf("the watch scheduled retries after %v, want %v", retries.delays, c.want)
			}
		})
	}
}

func TestARetrySelectsTheNetworkAgain(t *testing.T) {
	conn, s := watchedRadio(t, radio{state: machine.WirelessAssociating})
	s.Answer("STATUS", statusLost)
	s.Answer("SELECT_NETWORK 0", "OK\n")
	retries := &pendingRetries{}
	w := &radioWatch{
		conns: []*connection{conn}, now: func() time.Time { return watchTime },
		events: make(chan radioEvent, 1), after: retries.after,
	}
	w.apply(radioEvent{ifname: "wlan0", event: event(t,
		`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=4 duration=60 reason=CONN_FAILED`)})

	retries.fire[0]()
	w.apply(<-w.events)

	if asked := s.Asked(); !slices.Contains(asked, "SELECT_NETWORK 0") {
		t.Errorf("the supplicant received %q, want it to include SELECT_NETWORK 0", asked)
	}
}

func TestARetryLeavesAJoinedRadioAlone(t *testing.T) {
	conn, s := watchedRadio(t, onNear)
	w := &radioWatch{conns: []*connection{conn}, now: func() time.Time { return watchTime }}

	w.apply(radioEvent{ifname: "wlan0", event: wpaEvent{name: wpaRetry, fields: map[string]string{"id": "0"}}})

	if asked := s.Asked(); len(asked) != 0 {
		t.Errorf("the supplicant received %q, want no request because the radio is joined", asked)
	}
}
