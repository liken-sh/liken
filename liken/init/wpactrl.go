package main

// A client for wpa_supplicant's control interface, which is plain text
// over a UNIX datagram socket. init needs only ATTACH and the events
// that follow it, so it uses this client in place of wpa_cli. The
// events are the only way to tell a refused passphrase from an access
// point that never answered.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// wpaMessageMax is the size of the buffer that reads one datagram. A
// longer datagram is truncated. wpa_supplicant's own client uses the
// same size.
const wpaMessageMax = 4096

// wpaSocketTimeout is how long attach waits for the control socket.
// The supplicant creates the socket a moment after its process starts,
// so the path can be missing when the start returns.
const wpaSocketTimeout = 10 * time.Second

// wpaAttached is the name of an event that the client itself puts on
// the stream each time ATTACH succeeds. The supplicant does not replay
// what happened while no client was attached, such as an association
// during a restart, so a reader sends STATUS when it receives this
// event.
const wpaAttached = "LIKEN-ATTACHED"

// wpaEvent is one message the supplicant pushed to an attached client.
type wpaEvent struct {
	// name is the first word of the message, for example
	// CTRL-EVENT-CONNECTED.
	name string

	// fields are the key=value pairs in the message.
	fields map[string]string

	// text is the message without its priority, for console lines.
	text string
}

// parseWPAEvent parses one message from the socket. It returns false
// when the message has no text.
//
// A message is the priority in angle brackets, then the event name,
// then key=value pairs. The supplicant's global socket also puts an
// IFNAME= prefix before the priority. This client uses the
// per-interface socket, which does not, but the parser accepts both. A
// value can be in double quotes, such as an SSID with a space in it.
func parseWPAEvent(message string) (wpaEvent, bool) {
	text := strings.TrimRight(message, "\r\n")
	if rest, found := strings.CutPrefix(text, "IFNAME="); found {
		_, text, _ = strings.Cut(rest, " ")
	}
	if strings.HasPrefix(text, "<") {
		if end := strings.Index(text, ">"); end > 0 {
			text = text[end+1:]
		}
	}
	if text == "" {
		return wpaEvent{}, false
	}
	name, _, _ := strings.Cut(text, " ")
	return wpaEvent{name: name, fields: parseWPAFields(text), text: text}, true
}

// parseWPAFields returns the key=value pairs of one message. It splits
// on spaces, so it joins the tokens of a double-quoted value back
// together.
func parseWPAFields(text string) map[string]string {
	fields := map[string]string{}
	tokens := strings.Fields(text)
	for i := 0; i < len(tokens); i++ {
		key, value, found := strings.Cut(tokens[i], "=")
		// CTRL-EVENT-CONNECTED wraps its pairs in square brackets
		// ("[id=0 id_str=]"). Without trimming them the first key would
		// be "[id", which no caller looks up.
		key = strings.TrimPrefix(key, "[")
		value = strings.TrimSuffix(value, "]")
		if !found || key == "" {
			continue
		}
		if strings.HasPrefix(value, `"`) {
			for !strings.HasSuffix(value, `"`) || len(value) < 2 {
				if i+1 >= len(tokens) {
					break
				}
				i++
				value += " " + tokens[i]
			}
			value = strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`)
		}
		fields[key] = value
	}
	return fields
}

// deterministicWPAReasons maps each reason of a
// CTRL-EVENT-SSID-TEMP-DISABLED event that a retry cannot fix to the
// message to report. WRONG_KEY means the access point rejected the
// handshake, and NO_PSK_AVAILABLE means the configuration has no key.
// The other reasons, such as CONN_FAILED, come from a failed
// association that a later attempt can complete.
var deterministicWPAReasons = map[string]string{
	"WRONG_KEY": "the access point refused the passphrase (WRONG_KEY). " +
		"Correct the passphrase on the install media, because the running machine cannot change it",
	"NO_PSK_AVAILABLE": "the supplicant has no key for this network (NO_PSK_AVAILABLE)",
}

// classifyWirelessEvent returns the join state that an event proves and
// the message to report, or false when the event proves nothing. A
// scan that finds no network and an access point that goes away do not
// settle the join, because the next attempt can succeed.
func classifyWirelessEvent(event wpaEvent) (machine.WirelessState, string, bool) {
	switch event.name {
	case "CTRL-EVENT-CONNECTED":
		return machine.WirelessConnected, "", true
	case "CTRL-EVENT-SSID-TEMP-DISABLED":
		if message, final := deterministicWPAReasons[event.fields["reason"]]; final {
			return machine.WirelessWrongKey, message, true
		}
	}
	return "", "", false
}

// describeWirelessEvent returns a console line for an event, or the
// empty string for an event that has no line. A machine has no shell,
// so these lines are the only record of what the radio did.
func describeWirelessEvent(ifname string, event wpaEvent) string {
	switch event.name {
	case "CTRL-EVENT-CONNECTED":
		return fmt.Sprintf("liken: wireless: %s associated", ifname)
	case "CTRL-EVENT-DISCONNECTED":
		return fmt.Sprintf("liken: wireless: %s lost the access point (reason %s)",
			ifname, orUnknown(event.fields["reason"]))
	case "CTRL-EVENT-SSID-TEMP-DISABLED":
		return fmt.Sprintf("liken: wireless: %s: the access point refused the join (%s), retrying in %ss",
			ifname, orUnknown(event.fields["reason"]), orUnknown(event.fields["duration"]))
	case "CTRL-EVENT-SCAN-RESULTS":
		return fmt.Sprintf("liken: wireless: %s finished a scan", ifname)
	case "CTRL-EVENT-NETWORK-NOT-FOUND":
		// The supplicant sends this when it has no access point to try
		// for the network. Either none answered the scan, or each one
		// that answered refused the supplicant earlier and is
		// temporarily ignored.
		return fmt.Sprintf("liken: wireless: %s has no access point left to try for its network", ifname)
	case "CTRL-EVENT-AUTH-REJECT":
		// This event gives the access point's address as the word after
		// the name, with no bssid= key.
		bssid := ""
		if words := strings.Fields(event.text); len(words) > 1 {
			bssid = words[1]
		}
		return fmt.Sprintf("liken: wireless: %s: %s refused the authentication (%s)",
			ifname, orUnknown(bssid), statusText(event))
	case "CTRL-EVENT-ASSOC-REJECT":
		return fmt.Sprintf("liken: wireless: %s: %s refused the association (%s)",
			ifname, orUnknown(event.fields["bssid"]), statusText(event))
	}
	return ""
}

// statusText describes the 802.11 status code in a rejection event.
func statusText(event wpaEvent) string {
	code, err := strconv.Atoi(event.fields["status_code"])
	if err != nil {
		return "no status code"
	}
	return machine.StatusCodeText(code)
}

// wpaControl is a client attached to one supplicant's control socket.
// It binds a socket path of its own, because the supplicant replies to
// the sender's address and an unbound datagram socket has none.
type wpaControl struct {
	socket string
	local  string
	out    chan wpaEvent

	// timeout is how long attach waits for the socket to appear. It is a
	// field so a test can shorten it.
	timeout time.Duration

	mu     sync.Mutex
	conn   *net.UnixConn
	closed bool
}

// attachWPAControl connects to the supplicant's control socket and
// subscribes to its events.
func attachWPAControl(socket string) (*wpaControl, error) {
	c := &wpaControl{
		socket: socket,
		local:  filepath.Join(filepath.Dir(filepath.Dir(socket)), "client"),
		// A send to a full channel drops the event. read sends while
		// it holds c.mu, so a blocking send to a slow reader would
		// also block close.
		out:     make(chan wpaEvent, 64),
		timeout: wpaSocketTimeout,
	}
	if err := c.attach(); err != nil {
		return nil, err
	}
	return c, nil
}

func waitForWPASocket(socket string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(socket); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the supplicant did not create %s within %s", socket, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// attach connects to the socket, sends ATTACH, and starts reading
// events. It is also how the client resubscribes after a supplicant
// restart, because a new supplicant process has no record of the
// clients that were attached to the old one.
func (c *wpaControl) attach() error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return fmt.Errorf("the control client for %s is closed", c.socket)
	}
	if err := waitForWPASocket(c.socket, c.timeout); err != nil {
		return err
	}
	_ = os.Remove(c.local)
	conn, err := net.DialUnix("unixgram",
		&net.UnixAddr{Name: c.local, Net: "unixgram"},
		&net.UnixAddr{Name: c.socket, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("dialing %s: %w", c.socket, err)
	}
	if err := attachRequest(conn); err != nil {
		conn.Close()
		_ = os.Remove(c.local)
		return err
	}

	c.mu.Lock()
	old := c.conn
	c.conn = conn
	// The send is under the lock for the reason that read gives.
	if !c.closed {
		select {
		case c.out <- wpaEvent{name: wpaAttached}:
		default:
		}
	}
	c.mu.Unlock()
	if old != nil {
		old.Close()
	}
	go c.read(conn)
	return nil
}

// attachRequest sends ATTACH and checks that the supplicant replies
// OK. The reply is OK or FAIL on one line.
func attachRequest(conn *net.UnixConn) error {
	if _, err := conn.Write([]byte("ATTACH")); err != nil {
		return fmt.Errorf("sending ATTACH: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	// Replies and events arrive on the same socket, and an event can
	// come before the reply to ATTACH. A reply has no priority prefix
	// and an event always has one, so the loop skips messages that
	// start with "<" or "IFNAME=".
	buf := make([]byte, wpaMessageMax)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return fmt.Errorf("reading the answer to ATTACH: %w", err)
		}
		message := string(buf[:n])
		if strings.HasPrefix(message, "<") || strings.HasPrefix(message, "IFNAME=") {
			continue
		}
		if answer := strings.TrimSpace(message); answer != "OK" {
			return fmt.Errorf("the supplicant refused ATTACH and answered %q", answer)
		}
		return conn.SetReadDeadline(time.Time{})
	}
}

// read parses messages from one connection and sends the events to the
// channel. It returns when the connection fails or the control closes.
//
// The send is under the lock because close closes the channel under it,
// and a send on a closed channel panics. Closing the connection does not
// stop a read that has already returned a message, so read also checks
// closed after it takes the lock.
func (c *wpaControl) read(conn *net.UnixConn) {
	buf := make([]byte, wpaMessageMax)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		event, ok := parseWPAEvent(string(buf[:n]))
		if !ok {
			continue
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		select {
		case c.out <- event:
		default:
		}
		c.mu.Unlock()
	}
}

// events returns the channel of events from the supplicant. close
// closes it.
func (c *wpaControl) events() <-chan wpaEvent {
	return c.out
}

// close disconnects, removes the client's socket file, and closes the
// events channel. It is safe to call more than once.
func (c *wpaControl) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.conn != nil {
		c.conn.Close()
	}
	_ = os.Remove(c.local)
	close(c.out)
}
