package main

// The event lines in these tests are copied from the supplicant's
// own format strings, because the parser and the two judgements over
// it are the whole of what tells a refused passphrase apart from an
// access point that never answered. The socket itself is exercised
// against a stand-in supplicant, so no radio is needed.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
)

func TestParseWPAEventReadsThePriorityAndTheName(t *testing.T) {
	event, ok := parseWPAEvent("<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0 id_str=]")
	if !ok {
		t.Fatal("the message must parse")
	}
	if event.level != 3 {
		t.Errorf("level = %d, want 3", event.level)
	}
	if event.name != "CTRL-EVENT-CONNECTED" {
		t.Errorf("name = %q", event.name)
	}
	if event.fields["id"] != "0" {
		t.Errorf("id = %q", event.fields["id"])
	}
}

func TestParseWPAEventDropsTheGlobalSocketsInterfacePrefix(t *testing.T) {
	// The per-interface socket sends no such prefix. The global one
	// does, and a client that reads either must not take the prefix for
	// the event's name.
	event, ok := parseWPAEvent("IFNAME=wlan0 <3>CTRL-EVENT-SCAN-RESULTS ")
	if !ok || event.name != "CTRL-EVENT-SCAN-RESULTS" {
		t.Errorf("got %+v", event)
	}
}

func TestParseWPAEventReadsAnEventWithNoPayload(t *testing.T) {
	// The supplicant emits these two as the name and a trailing space.
	for _, message := range []string{"<3>CTRL-EVENT-SCAN-RESULTS ", "<3>CTRL-EVENT-NETWORK-NOT-FOUND "} {
		event, ok := parseWPAEvent(message)
		if !ok || !strings.HasPrefix(message, "<3>"+event.name) {
			t.Errorf("%q parsed as %+v", message, event)
		}
	}
}

func TestParseWPAEventKeepsAQuotedValueWhole(t *testing.T) {
	event, ok := parseWPAEvent(`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="stony point" auth_failures=1 duration=10 reason=WRONG_KEY`)
	if !ok {
		t.Fatal("the message must parse")
	}
	if event.fields["ssid"] != "stony point" {
		t.Errorf("ssid = %q", event.fields["ssid"])
	}
	if event.fields["reason"] != "WRONG_KEY" {
		t.Errorf("reason = %q", event.fields["reason"])
	}
}

func TestParseWPAEventFindsTheReasonPastAnAwkwardName(t *testing.T) {
	// The supplicant printf-encodes the name in this event, so a name
	// holding a quote arrives with escapes in it. The reason is what
	// this boot reads, and it must survive whatever the name looks like.
	event, ok := parseWPAEvent(`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="a \"b\" c" auth_failures=2 duration=20 reason=WRONG_KEY`)
	if !ok || event.fields["reason"] != "WRONG_KEY" {
		t.Errorf("got %+v", event)
	}
}

func TestParseWPAEventToleratesAMessageWithNoPriority(t *testing.T) {
	event, ok := parseWPAEvent("CTRL-EVENT-TERMINATING ")
	if !ok || event.name != "CTRL-EVENT-TERMINATING" || event.level != -1 {
		t.Errorf("got %+v", event)
	}
}

func TestParseWPAEventRefusesAnEmptyMessage(t *testing.T) {
	if _, ok := parseWPAEvent("<3>"); ok {
		t.Error("a message with no text names no event")
	}
}

func TestConnectingSettlesTheJoin(t *testing.T) {
	event, _ := parseWPAEvent("<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0 id_str=]")
	state, _, settled := judgeWirelessEvent(event)
	if !settled || state != machine.WirelessConnected {
		t.Errorf("state = %q, settled = %v", state, settled)
	}
}

func TestARefusedPassphraseSettlesTheJoinAndNamesTheFix(t *testing.T) {
	event, _ := parseWPAEvent(`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="stonypoint" auth_failures=1 duration=10 reason=WRONG_KEY`)
	state, message, settled := judgeWirelessEvent(event)
	if !settled || state != machine.WirelessWrongKey {
		t.Fatalf("state = %q, settled = %v", state, settled)
	}
	for _, want := range []string{"WRONG_KEY", "install media"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message must carry %q: %q", want, message)
		}
	}
}

func TestAConfigurationWithNoKeySettlesTheJoin(t *testing.T) {
	event, _ := parseWPAEvent(`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="stonypoint" auth_failures=1 duration=10 reason=NO_PSK_AVAILABLE`)
	if _, _, settled := judgeWirelessEvent(event); !settled {
		t.Error("a network the supplicant has no key for is a decision, not a wait")
	}
}

func TestAbsenceNeverSettlesTheJoin(t *testing.T) {
	// The plan's hardest rule, at the level of one event: nothing here
	// may end the wait, because every one of these states may be gone
	// by the next attempt.
	for _, message := range []string{
		"<3>CTRL-EVENT-SCAN-RESULTS ",
		"<3>CTRL-EVENT-NETWORK-NOT-FOUND ",
		"<3>CTRL-EVENT-DISCONNECTED bssid=04:4a:2c:11:22:33 reason=3 locally_generated=1",
		`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="stonypoint" auth_failures=4 duration=60 reason=CONN_FAILED`,
		"<3>CTRL-EVENT-ASSOC-REJECT bssid=04:4a:2c:11:22:33 status_code=16 timeout=ASSOC",
		"<3>CTRL-EVENT-AUTH-REJECT 04:4a:2c:11:22:33 auth_type=3 auth_transaction=1 status_code=1",
	} {
		event, ok := parseWPAEvent(message)
		if !ok {
			t.Fatalf("%q must parse", message)
		}
		if _, _, settled := judgeWirelessEvent(event); settled {
			t.Errorf("%q must not settle the join", message)
		}
	}
}

func TestDescribeWirelessEventNamesWhatHappened(t *testing.T) {
	for message, want := range map[string]string{
		"<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]":                  "associated",
		"<3>CTRL-EVENT-DISCONNECTED bssid=04:4a:2c:11:22:33 reason=3":                                 "reason 3",
		"<3>CTRL-EVENT-SCAN-RESULTS ":                                                                 "finished a scan",
		"<3>CTRL-EVENT-NETWORK-NOT-FOUND ":                                                            "found no access point",
		`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="x" auth_failures=1 duration=10 reason=WRONG_KEY`: "retrying in 10s",
	} {
		event, _ := parseWPAEvent(message)
		line := describeWirelessEvent("wlan0", event)
		if !strings.Contains(line, want) || !strings.Contains(line, "wlan0") {
			t.Errorf("%q described as %q, want %q in it", message, line, want)
		}
	}
}

func TestDescribeWirelessEventSaysNothingAboutAnEventItDoesNotName(t *testing.T) {
	event, _ := parseWPAEvent("<3>CTRL-EVENT-BSS-ADDED 3 04:4a:2c:11:22:33")
	if line := describeWirelessEvent("wlan0", event); line != "" {
		t.Errorf("got %q", line)
	}
}

// standInSupplicant answers ATTACH the way the supplicant does and then
// pushes whatever the test tells it to push. It is a datagram socket in
// a temporary directory, so the client under test exercises the real
// transport with no radio and no supplicant.
type standInSupplicant struct {
	socket   string
	conn     *net.UnixConn
	attached chan *net.UnixAddr
}

func newStandInSupplicant(t *testing.T, answer string) *standInSupplicant {
	t.Helper()
	// The socket layout matches the real one: <run>/<ifname>/ctrl/<ifname>.
	dir := filepath.Join(t.TempDir(), "wlan0", "ctrl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "wlan0")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	s := &standInSupplicant{socket: socket, conn: conn, attached: make(chan *net.UnixAddr, 1)}
	t.Cleanup(func() { conn.Close() })

	go func() {
		buf := make([]byte, wpaMessageMax)
		for {
			n, from, err := conn.ReadFromUnix(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) != "ATTACH" {
				continue
			}
			_, _ = conn.WriteToUnix([]byte(answer), from)
			select {
			case s.attached <- from:
			default:
			}
		}
	}()
	return s
}

// push sends one unsolicited event to the attached client.
func (s *standInSupplicant) push(t *testing.T, message string) {
	t.Helper()
	select {
	case peer := <-s.attached:
		if _, err := s.conn.WriteToUnix([]byte(message), peer); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing attached")
	}
}

func TestAttachCarriesTheSupplicantsEvents(t *testing.T) {
	s := newStandInSupplicant(t, "OK\n")
	control, err := attachWPAControl(s.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer control.close()

	s.push(t, "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]")
	select {
	case event := <-control.events():
		if event.name != "CTRL-EVENT-CONNECTED" {
			t.Errorf("got %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
	}
}

func TestAttachRefusedIsReported(t *testing.T) {
	s := newStandInSupplicant(t, "FAIL\n")
	if _, err := attachWPAControl(s.socket); err == nil {
		t.Fatal("a refused ATTACH must be reported")
	}
}

func TestAttachWaitsForTheSocketAndThenGivesUp(t *testing.T) {
	// The supplicant creates its socket after it starts, so the wait
	// exists. A socket that never appears must not hold the boot.
	absent := filepath.Join(t.TempDir(), "wlan0")
	err := waitForWPASocket(absent, 100*time.Millisecond)
	if err == nil {
		t.Fatal("a socket that never appears must be reported")
	}
	if !strings.Contains(err.Error(), absent) {
		t.Errorf("the error must name the socket: %v", err)
	}
}

func TestClosingTheControlEndsTheEventStream(t *testing.T) {
	// This is what releases a parked boot when the machine is going
	// down: the stream ends and the hold returns.
	s := newStandInSupplicant(t, "OK\n")
	control, err := attachWPAControl(s.socket)
	if err != nil {
		t.Fatal(err)
	}
	control.close()
	control.close() // closing twice must be safe: the shutdown may race the park
	if _, open := <-control.events(); open {
		t.Error("the stream must be closed")
	}
}

func TestClosingRemovesTheClientsOwnSocket(t *testing.T) {
	// The supplicant drops a monitor whose socket has gone, so leaving
	// the file behind would leave a dead monitor on its list.
	s := newStandInSupplicant(t, "OK\n")
	control, err := attachWPAControl(s.socket)
	if err != nil {
		t.Fatal(err)
	}
	local := control.local
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("the client binds its own path: %v", err)
	}
	control.close()
	if _, err := os.Stat(local); err == nil {
		t.Error("the client's socket must be removed")
	}
}
