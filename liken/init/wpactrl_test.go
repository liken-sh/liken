package main

// The event lines in these tests copy the supplicant's own format
// strings, so the parser is tested on the messages that a real
// supplicant sends.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

func TestParseWPAEventStripsThePriorityAndReadsTheName(t *testing.T) {
	event, ok := parseWPAEvent("<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0 id_str=]")
	if !ok {
		t.Fatal("parseWPAEvent refused the message, want an event")
	}
	if event.name != "CTRL-EVENT-CONNECTED" {
		t.Errorf("name is %q, want the event name", event.name)
	}
	if event.fields["id"] != "0" {
		t.Errorf("field id is %q, want the id of the network", event.fields["id"])
	}
}

func TestParseWPAEventDropsTheGlobalSocketsInterfacePrefix(t *testing.T) {
	// The global control socket adds this prefix and the per-interface
	// socket does not. The parser must not read the prefix as the event
	// name.
	event, ok := parseWPAEvent("IFNAME=wlan0 <3>CTRL-EVENT-SCAN-RESULTS ")
	if !ok || event.name != "CTRL-EVENT-SCAN-RESULTS" {
		t.Errorf("event is %+v, want CTRL-EVENT-SCAN-RESULTS", event)
	}
}

func TestParseWPAEventReadsAnEventWithNoPayload(t *testing.T) {
	// The supplicant sends these two events as the name and a trailing
	// space.
	for _, message := range []string{"<3>CTRL-EVENT-SCAN-RESULTS ", "<3>CTRL-EVENT-NETWORK-NOT-FOUND "} {
		event, ok := parseWPAEvent(message)
		if !ok || !strings.HasPrefix(message, "<3>"+event.name) {
			t.Errorf("%q is parsed as %+v, want the event named in it", message, event)
		}
	}
}

func TestParseWPAEventKeepsAQuotedValueWhole(t *testing.T) {
	event, ok := parseWPAEvent(`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="corner cafe" auth_failures=1 duration=10 reason=WRONG_KEY`)
	if !ok {
		t.Fatal("parseWPAEvent refused the message, want an event")
	}
	if event.fields["ssid"] != "corner cafe" {
		t.Errorf("field ssid is %q, want the network name", event.fields["ssid"])
	}
	if event.fields["reason"] != "WRONG_KEY" {
		t.Errorf("field reason is %q, want the reason in the event", event.fields["reason"])
	}
}

func TestParseWPAEventFindsTheReasonPastAnAwkwardName(t *testing.T) {
	// The supplicant escapes the SSID in this event, so an SSID with a
	// quote in it arrives with backslashes. The reason field must still
	// parse.
	event, ok := parseWPAEvent(`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="a \"b\" c" auth_failures=2 duration=20 reason=WRONG_KEY`)
	if !ok || event.fields["reason"] != "WRONG_KEY" {
		t.Errorf("event is %+v, want reason WRONG_KEY", event)
	}
}

func TestParseWPAEventToleratesAMessageWithNoPriority(t *testing.T) {
	event, ok := parseWPAEvent("CTRL-EVENT-TERMINATING ")
	if !ok || event.name != "CTRL-EVENT-TERMINATING" {
		t.Errorf("event is %+v, want CTRL-EVENT-TERMINATING", event)
	}
}

func TestParseWPAEventRefusesAnEmptyMessage(t *testing.T) {
	if _, ok := parseWPAEvent("<3>"); ok {
		t.Error("parseWPAEvent accepted a message with no text, want it refused")
	}
}

func TestAConnectedEventSettlesTheJoin(t *testing.T) {
	event, _ := parseWPAEvent("<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0 id_str=]")
	state, _, settled := classifyWirelessEvent(event)
	if !settled || state != machine.WirelessConnected {
		t.Errorf("state is %q and settled is %v, want %q and true", state, settled, machine.WirelessConnected)
	}
}

func TestAWrongKeyEventSettlesTheJoinAndPointsToTheInstallMedia(t *testing.T) {
	event, _ := parseWPAEvent(`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=1 duration=10 reason=WRONG_KEY`)
	state, message, settled := classifyWirelessEvent(event)
	if !settled || state != machine.WirelessWrongKey {
		t.Fatalf("state is %q and settled is %v, want %q and true", state, settled, machine.WirelessWrongKey)
	}
	for _, want := range []string{"WRONG_KEY", "install media"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message is %q, want it to contain %q", message, want)
		}
	}
}

func TestAConfigurationWithNoKeySettlesTheJoin(t *testing.T) {
	event, _ := parseWPAEvent(`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=1 duration=10 reason=NO_PSK_AVAILABLE`)
	if _, _, settled := classifyWirelessEvent(event); !settled {
		t.Error("NO_PSK_AVAILABLE does not settle the join, want it settled, because retrying cannot fix it")
	}
}

func TestEventsThatALaterAttemptCanFixDoNotSettleTheJoin(t *testing.T) {
	for _, message := range []string{
		"<3>CTRL-EVENT-SCAN-RESULTS ",
		"<3>CTRL-EVENT-NETWORK-NOT-FOUND ",
		"<3>CTRL-EVENT-DISCONNECTED bssid=04:4a:2c:11:22:33 reason=3 locally_generated=1",
		`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="homenet" auth_failures=4 duration=60 reason=CONN_FAILED`,
		"<3>CTRL-EVENT-ASSOC-REJECT bssid=04:4a:2c:11:22:33 status_code=16 timeout=ASSOC",
		"<3>CTRL-EVENT-AUTH-REJECT 04:4a:2c:11:22:33 auth_type=3 auth_transaction=1 status_code=1",
	} {
		event, ok := parseWPAEvent(message)
		if !ok {
			t.Fatalf("parseWPAEvent refused %q, want an event", message)
		}
		if _, _, settled := classifyWirelessEvent(event); settled {
			t.Errorf("%q settles the join, want it not to", message)
		}
	}
}

func TestDescribeWirelessEventReturnsALineForEachJoinEvent(t *testing.T) {
	for message, want := range map[string]string{
		"<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]":                  "associated",
		"<3>CTRL-EVENT-DISCONNECTED bssid=04:4a:2c:11:22:33 reason=3":                                 "reason 3",
		"<3>CTRL-EVENT-SCAN-RESULTS ":                                                                 "finished a scan",
		"<3>CTRL-EVENT-NETWORK-NOT-FOUND ":                                                            "no access point left to try",
		"<3>CTRL-EVENT-AUTH-REJECT 04:4a:2c:11:22:33 auth_type=3 auth_transaction=1 status_code=37":   "04:4a:2c:11:22:33 refused the authentication (status 37, the request was declined)",
		"<3>CTRL-EVENT-ASSOC-REJECT bssid=04:4a:2c:11:22:33 status_code=17":                           "04:4a:2c:11:22:33 refused the association (status 17, the access point cannot handle another station)",
		`<3>CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid="x" auth_failures=1 duration=10 reason=WRONG_KEY`: "retrying in 10s",
	} {
		event, _ := parseWPAEvent(message)
		line := describeWirelessEvent("wlan0", event)
		if !strings.Contains(line, want) || !strings.Contains(line, "wlan0") {
			t.Errorf("%q is described as %q, want it to contain %q and wlan0", message, line, want)
		}
	}
}

func TestDescribeWirelessEventReturnsNothingForAnUnlistedEvent(t *testing.T) {
	event, _ := parseWPAEvent("<3>CTRL-EVENT-BSS-ADDED 3 04:4a:2c:11:22:33")
	if line := describeWirelessEvent("wlan0", event); line != "" {
		t.Errorf("the line is %q, want none", line)
	}
}

// fakeSupplicant replies to ATTACH with a fixed answer and then sends
// the messages that a test passes to push. It listens on a real
// datagram socket in a temporary directory.
type fakeSupplicant struct {
	socket   string
	conn     *net.UnixConn
	attached chan *net.UnixAddr
}

func newFakeSupplicant(t *testing.T, answer string) *fakeSupplicant {
	t.Helper()
	// The path has the same layout as the real socket,
	// <run>/<ifname>/ctrl/<ifname>, because the client derives its own
	// socket path from it.
	dir := filepath.Join(t.TempDir(), "wlan0", "ctrl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the socket directory: %v", err)
	}
	socket := filepath.Join(dir, "wlan0")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listening on the fake supplicant socket: %v", err)
	}
	s := &fakeSupplicant{socket: socket, conn: conn, attached: make(chan *net.UnixAddr, 1)}
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

// push sends one message to the client that attached.
func (s *fakeSupplicant) push(t *testing.T, message string) {
	t.Helper()
	select {
	case peer := <-s.attached:
		if _, err := s.conn.WriteToUnix([]byte(message), peer); err != nil {
			t.Fatalf("sending a message to the client: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the supplicant received no ATTACH within 2 seconds, want one")
	}
}

func TestAttachDeliversTheSupplicantsEvents(t *testing.T) {
	s := newFakeSupplicant(t, "OK\n")
	control, err := attachWPAControl(s.socket)
	if err != nil {
		t.Fatalf("attaching to the supplicant: %v", err)
	}
	defer control.close()

	s.push(t, "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]")
	// The client puts its own attach event on the stream first.
	for _, want := range []string{wpaAttached, "CTRL-EVENT-CONNECTED"} {
		select {
		case event := <-control.events():
			if event.name != want {
				t.Errorf("event is %+v, want %s", event, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("the event %s never arrived", want)
		}
	}
}

func TestAttachReturnsAnErrorWhenTheSupplicantReturnsFAIL(t *testing.T) {
	s := newFakeSupplicant(t, "FAIL\n")
	if _, err := attachWPAControl(s.socket); err == nil {
		t.Fatal("attachWPAControl returned no error, want one for the FAIL answer")
	}
}

func TestWaitForWPASocketReturnsAnErrorAfterTheTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A supplicant that failed to start must not block the boot.
		absent := filepath.Join(t.TempDir(), "wlan0")
		start := time.Now()
		err := waitForWPASocket(absent, wpaSocketTimeout)
		if err == nil {
			t.Fatal("waitForWPASocket returned no error, want one for a socket that never appears")
		}
		// The wait checks every 50ms, and fails at the first check after
		// the timeout has passed.
		if elapsed := time.Since(start); elapsed != wpaSocketTimeout+50*time.Millisecond {
			t.Errorf("the wait gave up after %s, want the first look past %s", elapsed, wpaSocketTimeout)
		}
		if !strings.Contains(err.Error(), absent) {
			t.Errorf("the error is %v, want it to name %s", err, absent)
		}
	})
}

func TestCloseClosesTheEventChannel(t *testing.T) {
	s := newFakeSupplicant(t, "OK\n")
	control, err := attachWPAControl(s.socket)
	if err != nil {
		t.Fatalf("attaching to the supplicant: %v", err)
	}
	control.close()
	control.close() // a second close does nothing
	// The attach event was already in the channel, and a closed channel
	// still delivers its buffered values before it reports the close.
	if event := <-control.events(); event.name != wpaAttached {
		t.Errorf("event is %+v before the close, want the attach event", event)
	}
	if _, open := <-control.events(); open {
		t.Error("the event stream is open, want it closed")
	}
}

// close can run while the read goroutine has an event to send. A send
// on a closed channel panics, and a panic in init stops the machine.
// The race detector fails this test if the send and the close can run
// at the same time.
func TestCloseDuringEventTrafficDoesNotPanic(t *testing.T) {
	s := newFakeSupplicant(t, "OK\n")
	control, err := attachWPAControl(s.socket)
	if err != nil {
		t.Fatalf("attaching to the supplicant: %v", err)
	}
	peer := <-s.attached
	reported := make(chan struct{})
	go func() {
		defer close(reported)
		for {
			if _, err := s.conn.WriteToUnix([]byte("<3>CTRL-EVENT-SCAN-RESULTS"), peer); err != nil {
				return
			}
		}
	}()
	// A consumer drains the channel, so the sends succeed until the
	// close.
	drained, taken := make(chan struct{}), make(chan struct{}, 1)
	go func() {
		defer close(drained)
		for range control.events() {
			select {
			case taken <- struct{}{}:
			default:
			}
		}
	}()
	<-taken
	<-taken

	control.close()

	<-drained
	s.conn.Close()
	<-reported
}

func TestCloseRemovesTheClientsSocketFile(t *testing.T) {
	// wpa_supplicant drops an attached client whose socket file is
	// gone. A file left behind keeps the client on its list.
	s := newFakeSupplicant(t, "OK\n")
	control, err := attachWPAControl(s.socket)
	if err != nil {
		t.Fatalf("attaching to the supplicant: %v", err)
	}
	local := control.local
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("checking the path of the client socket: %v", err)
	}
	control.close()
	if _, err := os.Stat(local); err == nil {
		t.Error("the client's socket file exists, want it removed")
	}
}
