package main

// The supplicant's control interface is plain text over one UNIX
// datagram socket. liken carries this small client rather than the
// wpa_cli program because the whole of what init needs is ATTACH and
// the events that follow. Those events are the only thing on the
// machine that can tell a refused passphrase apart from an access
// point that never answered.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/machine"
)

// The size of one message. Upstream's own client reads into a buffer
// of this size. An event is a short line, but a datagram longer than
// the buffer loses its tail rather than splitting, which is why the
// buffer is generous.
const wpaMessageMax = 4096

// How long the attach waits for the socket to appear. The supplicant
// creates its socket after it starts, so the process id comes back
// before the path exists. This is a race with a starting process on
// this machine, not a network wait, which is why the allowance is
// short.
const wpaSocketPatience = 10 * time.Second

// wpaEvent is one message the supplicant pushed to an attached client.
type wpaEvent struct {
	// level is the priority the supplicant stamped on the message,
	// from its own debug levels. A monitor receives everything at
	// info and above by default, and info is the level every join
	// event uses.
	level int

	// name is the event's own word, for example CTRL-EVENT-CONNECTED.
	name string

	// fields are the key=value pairs the message carries.
	fields map[string]string

	// text is the whole message after the priority, for the console.
	text string
}

// parseWPAEvent reads one message off the socket.
//
// The wire format, exactly: the supplicant prefixes each message with
// its priority in angle brackets, and the rest is the event's name
// and then key=value pairs. The global socket adds an IFNAME= prefix
// ahead of the priority; the per-interface socket liken uses does
// not. A value may be quoted, which is how an SSID with a space in it
// arrives.
func parseWPAEvent(message string) (wpaEvent, bool) {
	text := strings.TrimRight(message, "\r\n")
	if rest, found := strings.CutPrefix(text, "IFNAME="); found {
		_, text, _ = strings.Cut(rest, " ")
	}
	level := -1
	if strings.HasPrefix(text, "<") {
		if end := strings.Index(text, ">"); end > 0 {
			if _, err := fmt.Sscanf(text[1:end], "%d", &level); err != nil {
				level = -1
			}
			text = text[end+1:]
		}
	}
	if text == "" {
		return wpaEvent{}, false
	}
	name, _, _ := strings.Cut(text, " ")
	return wpaEvent{level: level, name: name, fields: parseWPAFields(text), text: text}, true
}

// parseWPAFields collects the key=value pairs out of one message. A
// value in double quotes may hold spaces, which is how an SSID arrives,
// so the scan joins the tokens of such a value back together.
func parseWPAFields(text string) map[string]string {
	fields := map[string]string{}
	tokens := strings.Fields(text)
	for i := 0; i < len(tokens); i++ {
		key, value, found := strings.Cut(tokens[i], "=")
		// The connected event wraps its pairs in square brackets and
		// every other event does not. A key parsed with a bracket
		// still on it would answer to nothing, so the brackets come
		// off here.
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

// The reasons the supplicant gives for refusing a network, and which
// of them this boot treats as final. The supplicant emits exactly
// four. WRONG_KEY is the access point rejecting the handshake, and
// NO_PSK_AVAILABLE is a configuration with no key in it at all; both
// are decisions. The other two describe an association that failed,
// which the next attempt may well complete, so they stay transient.
var deterministicWPAReasons = map[string]string{
	"WRONG_KEY": "the access point refused the passphrase (WRONG_KEY); " +
		"until rotation exists, a wrong passphrase is fixed by the install media, not in place",
	"NO_PSK_AVAILABLE": "the supplicant has no key for this network (NO_PSK_AVAILABLE)",
}

// judgeWirelessEvent decides what one event means for the join. It
// reports the verdict and whether the event settled the question at
// all. Most events settle nothing: a scan that found nothing and an
// access point that went away are both states that the next attempt may
// leave behind.
func judgeWirelessEvent(event wpaEvent) (machine.WirelessState, string, bool) {
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

// describeWirelessEvent renders one event as a console line, or the
// empty string for an event this boot has nothing to say about. On a
// machine with no shell, these lines are the whole account of what the
// radio did.
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
		return fmt.Sprintf("liken: wireless: %s found no access point for its network", ifname)
	case "CTRL-EVENT-ASSOC-REJECT", "CTRL-EVENT-AUTH-REJECT":
		return fmt.Sprintf("liken: wireless: %s: %s", ifname, event.text)
	}
	return ""
}

// wpaControl is an attached client on one supplicant's control socket.
//
// wpaControl is an attached client on one supplicant's control
// socket. The client binds a path of its own because the socket is a
// datagram socket: the supplicant answers by sending to the address
// the request arrived from, and an unbound socket has no address to
// answer to.
type wpaControl struct {
	socket string
	local  string
	out    chan wpaEvent

	// patience is how long attach waits for the socket to appear. It is
	// a field rather than the constant so a test can drive the restart
	// loop's re-attach without waiting out a real boot's allowance.
	patience time.Duration

	mu     sync.Mutex
	conn   *net.UnixConn
	closed bool
}

// attachWPAControl opens the supplicant's control socket and asks for
// the stream of unsolicited events.
func attachWPAControl(socket string) (*wpaControl, error) {
	c := &wpaControl{
		socket: socket,
		local:  filepath.Join(filepath.Dir(filepath.Dir(socket)), "client"),
		// The channel is buffered and a full channel drops. Nothing
		// reads these events once the boot goes on, but the
		// supplicant keeps reporting for the life of the machine,
		// and a blocking send would stall the reader in init for
		// good.
		out:      make(chan wpaEvent, 64),
		patience: wpaSocketPatience,
	}
	if err := c.attach(); err != nil {
		return nil, err
	}
	return c, nil
}

// waitForWPASocket waits for the supplicant to create its socket.
func waitForWPASocket(socket string, patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		if _, err := os.Stat(socket); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the supplicant did not create %s within %s", socket, patience)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// attach dials the socket, registers this client as a monitor, and
// starts reading. It is also the repair after a restart: a supplicant
// that died took its list of monitors with it, so the new process must
// be asked again.
func (c *wpaControl) attach() error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return fmt.Errorf("the control client for %s is closed", c.socket)
	}
	if err := waitForWPASocket(c.socket, c.patience); err != nil {
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
	c.mu.Unlock()
	if old != nil {
		old.Close()
	}
	go c.read(conn)
	return nil
}

// attachRequest sends ATTACH and reads the answer. The supplicant
// answers OK or FAIL, each on one line.
func attachRequest(conn *net.UnixConn) error {
	if _, err := conn.Write([]byte("ATTACH")); err != nil {
		return fmt.Errorf("sending ATTACH: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	// The answer is looked for rather than simply read. The same
	// socket carries the answers and the events, and this is the one
	// place where the two could arrive in either order. An answer
	// never carries the priority prefix and an event always does,
	// which is how the loop tells them apart.
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
			return fmt.Errorf("the supplicant refused ATTACH: %s", answer)
		}
		return conn.SetReadDeadline(time.Time{})
	}
}

// read carries one connection's messages onto the event channel, and
// ends when that connection closes.
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
		select {
		case c.out <- event:
		default:
		}
	}
}

// events is the stream of messages the supplicant pushes.
func (c *wpaControl) events() <-chan wpaEvent {
	return c.out
}

// close ends the attachment and the stream with it.
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
