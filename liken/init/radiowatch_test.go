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

// The near and far access points of one network, as STATUS answers
// for each.
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

// watchedRadio serves a stand-in supplicant for wlan0 under a
// temporary wirelessRunDir and answers a joined radio whose control is
// attached to it, with the given standing.
func watchedRadio(t *testing.T, standing radio) (*connection, *supplicanttest.Supplicant) {
	t.Helper()
	dir := t.TempDir()
	orig := wirelessRunDir
	wirelessRunDir = dir
	t.Cleanup(func() { wirelessRunDir = orig })

	s := supplicanttest.Start(t, dir, "wlan0")
	control, err := attachWPAControl(s.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(control.close)
	<-control.events() // the attach marker

	conn := addressed("wlan0", "192.168.1.20/24")
	standing.ifname, standing.ssid, standing.control = "wlan0", "homenet", control
	conn.radio = &standing
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
		t.Fatalf("%q does not parse", message)
	}
	return e
}

func TestTheRadioWatchFollowsTheSupplicant(t *testing.T) {
	cases := []struct {
		name     string
		standing radio
		status   string
		event    string
		want     machine.WirelessStatus
	}{
		{
			name: "a move to another access point", standing: onNear, status: statusFar,
			event: "<3>CTRL-EVENT-CONNECTED - Connection to " + farAP + " completed [id=0 id_str=]",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessConnected,
				BSSID: farAP, Frequency: 2437, Band: "2.4GHz", AssociatedAt: &watchTime,
			},
		},
		{
			name:     "a new channel on the same access point keeps the association",
			standing: onNear, status: statusNearCh,
			event: "<3>CTRL-EVENT-CONNECTED - Connection to " + nearAP + " completed [id=0 id_str=]",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessConnected,
				BSSID: nearAP, Frequency: 5745, Band: "5GHz", AssociatedAt: &boot,
			},
		},
		{
			name: "a lost access point", standing: onNear, status: statusLost,
			event: "<3>CTRL-EVENT-DISCONNECTED bssid=" + nearAP + " reason=4 locally_generated=1",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessAssociating,
				Message:              "lost " + nearAP + " (reason 4, disassociated because of inactivity, sent by this machine); the supplicant is rejoining",
				LastDisconnectReason: 4,
			},
		},
		{
			name: "a rejoin of the same access point after a loss",
			standing: radio{
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
			name:     "an attach after a restart reads the association again",
			standing: radio{state: machine.WirelessAssociating}, status: statusNear,
			event: wpaAttached,
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessConnected,
				BSSID: nearAP, Frequency: 5180, Band: "5GHz", AssociatedAt: &watchTime,
			},
		},
		{
			name:     "a passphrase the access point refuses after the join",
			standing: onNear, status: statusLost,
			event: "<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid=\"homenet\" auth_failures=1 duration=10 reason=WRONG_KEY",
			want: machine.WirelessStatus{
				SSID: "homenet", State: machine.WirelessWrongKey,
				Message: deterministicWPAReasons["WRONG_KEY"],
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, s := watchedRadio(t, c.standing)
			s.Answer("STATUS", c.status)
			w := &radioWatch{conns: []*connection{conn}, now: func() time.Time { return watchTime }}

			if !w.apply(radioEvent{ifname: "wlan0", event: event(t, c.event)}) {
				t.Fatal("the event changed nothing")
			}
			if got := w.conns[0].radio.wirelessStatus(); !reflect.DeepEqual(got, &c.want) {
				t.Errorf("status is\n%+v\nwant\n%+v", got, &c.want)
			}
		})
	}
}

func TestTheRadioWatchIgnoresWhatChangesNothing(t *testing.T) {
	cases := []struct {
		name     string
		standing radio
		status   string
		event    string
	}{
		{
			name: "a connect to the access point already in status", standing: onNear, status: statusNear,
			event: "<3>CTRL-EVENT-CONNECTED - Connection to " + nearAP + " completed [id=0 id_str=]",
		},
		{
			name:     "a disconnect of a radio that was not connected",
			standing: radio{state: machine.WirelessNoCarrier}, status: statusLost,
			event: "<3>CTRL-EVENT-DISCONNECTED bssid=" + nearAP + " reason=15",
		},
		{
			name: "a scan", standing: onNear, status: statusNear,
			event: "<3>CTRL-EVENT-SCAN-RESULTS ",
		},
		{
			name: "a signal change", standing: onNear, status: statusNear,
			event: "<3>CTRL-EVENT-SIGNAL-CHANGE above=0 signal=-74 noise=9999 txrate=144400",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, s := watchedRadio(t, c.standing)
			s.Answer("STATUS", c.status)
			w := &radioWatch{conns: []*connection{conn}, now: func() time.Time { return watchTime }}

			if w.apply(radioEvent{ifname: "wlan0", event: event(t, c.event)}) {
				t.Errorf("the event changed the radio to %+v", w.conns[0].radio.wirelessStatus())
			}
		})
	}
}

func TestTheRadioWatchLeavesTheBootsListAlone(t *testing.T) {
	// The boot hands its list to the watch, and other readers of the
	// boot's list must never see a radio change under them.
	conn, s := watchedRadio(t, onNear)
	s.Answer("STATUS", statusFar)
	w := &radioWatch{conns: []*connection{conn}, now: func() time.Time { return watchTime }}
	w.apply(radioEvent{ifname: "wlan0", event: event(t, "<3>CTRL-EVENT-CONNECTED - Connection to "+farAP+" completed")})
	if conn.radio.bssid != nearAP {
		t.Errorf("the boot's radio moved to %s", conn.radio.bssid)
	}
}

func TestTheRadioWatchPublishesTheBaselineAndEndsWhenTheStreamCloses(t *testing.T) {
	// The radio joined at boot, and the supplicant already holds the
	// near access point. The watch reads STATUS once before it reads
	// any event, writes the association, and ends when the stream
	// closes. The stream closes before the watch starts, which is the
	// order a shutdown during the boot gives, and the baseline still
	// lands, because STATUS goes over a socket of its own.
	conn, s := watchedRadio(t, radio{state: machine.WirelessConnected})
	s.Answer("STATUS", statusNear)
	tree := machine.FactsTree{Dir: filepath.Join(t.TempDir(), "facts")}
	conn.radio.control.close()

	if err := watchRadios([]*connection{conn}, nil, nil, tree, nil)(context.Background()); err != nil {
		t.Fatal(err)
	}

	status, err := tree.Read()
	if err != nil {
		t.Fatal(err)
	}
	want := &machine.WirelessStatus{
		SSID: "homenet", State: machine.WirelessConnected,
		BSSID: nearAP, Frequency: 5180, Band: "5GHz",
	}
	got := *status.Network.Interfaces[0].Wireless
	if got.AssociatedAt == nil {
		t.Error("the baseline must record when the radio associated")
	}
	got.AssociatedAt = nil
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("the facts hold\n%+v\nwant\n%+v", &got, want)
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
	resolv := aimResolvConf(t)
	w := lateRadio(t, func(ifc machine.InterfaceSpec) (*connection, error) {
		return withNameserver(addressed(ifc.Name, "192.168.1.20/24"), "192.168.1.1"), nil
	})

	w.apply(radioEvent{ifname: "wlan0", event: event(t, "<3>CTRL-EVENT-CONNECTED - Connection to "+nearAP+" completed")})

	got := w.conns[0]
	if got.addr == nil || got.addr.String() != "192.168.1.20/24" {
		t.Errorf("the radio holds the address %v", got.addr)
	}
	if got.radio.state != machine.WirelessConnected || got.radio.bssid != nearAP {
		t.Errorf("the radio is %+v", got.radio.wirelessStatus())
	}
	written, err := os.ReadFile(resolv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "nameserver 192.168.1.1") {
		t.Errorf("resolv.conf holds\n%s", written)
	}
}

func TestARadioThatJoinsLateAndFailsItsAddressingSaysWhy(t *testing.T) {
	aimResolvConf(t)
	w := lateRadio(t, func(machine.InterfaceSpec) (*connection, error) {
		return nil, errors.New("no DHCP offer on wlan0 in 30s")
	})

	w.apply(radioEvent{ifname: "wlan0", event: event(t, "<3>CTRL-EVENT-CONNECTED - Connection to "+nearAP+" completed")})

	got := w.conns[0]
	if got.addr != nil {
		t.Errorf("the radio holds the address %v", got.addr)
	}
	if got.radio.state != machine.WirelessConnected || got.radio.message != "no DHCP offer on wlan0 in 30s" {
		t.Errorf("the radio is %+v", got.radio.wirelessStatus())
	}
}

// pendingRetries is a watch's stand-in for time.AfterFunc: it records
// each delay, and the test runs the function itself.
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
				t.Errorf("the watch scheduled %v, want %v", retries.delays, c.want)
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
		t.Errorf("the watch asked %q", asked)
	}
}

func TestARetryLeavesAJoinedRadioAlone(t *testing.T) {
	conn, s := watchedRadio(t, onNear)
	w := &radioWatch{conns: []*connection{conn}, now: func() time.Time { return watchTime }}

	w.apply(radioEvent{ifname: "wlan0", event: wpaEvent{name: wpaRetry, fields: map[string]string{"id": "0"}}})

	if asked := s.Asked(); len(asked) != 0 {
		t.Errorf("the watch asked %q of a joined radio", asked)
	}
}
