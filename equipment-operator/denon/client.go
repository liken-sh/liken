// The client that holds one receiver's control connection open. It
// records the state each recognized line reports, reconnects when the
// read deadline or a socket error ends the connection, and translates
// between the wire and the equipment.Driver contract.

package denon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// These values bound one connection. The heartbeat asks the receiver
// for a reply when no other command has gone out. The silence limit
// ends a session after the socket carries no reply, even if the TCP
// socket remains open. A half-open socket can accept writes without
// delivering them, so an open socket alone does not prove reachability.
var (
	dialTimeout  = 5 * time.Second
	heartbeat    = 30 * time.Second
	silenceLimit = 75 * time.Second
	minBackoff   = time.Second
	maxBackoff   = 30 * time.Second
)

// The write queue for one connection. It holds every connect query and
// a burst of commands. A command that would overflow it is dropped,
// because the operator re-asserts nothing.
const queueDepth = 64

// The three outcomes a command can carry. The root wires Reporter to
// equipment_commands_total, so these strings are the label values that
// counter already counts. They are exported so the root can count a
// timeout its own wait produced.
const (
	CommandOK      = "ok"
	CommandFailed  = "failed"
	CommandTimeout = "timeout"
)

// Client holds one receiver's connection, the state it reports, and
// the queue of commands waiting to go out. out is the current
// connection's queue, or nil while disconnected. A send holds the
// mutex while it enqueues, and the writer nils out under that same
// mutex before it stops draining, so a command is never left in a
// queue nobody drains.
//
// Reporter counts the outcome of every command this client sends, for
// equipment_commands_total. The root sets it when it wires up metrics;
// a nil Reporter is what a test that does not care builds, and
// reportCommand on nil is a no-op.
type Client struct {
	address  string
	listener func(equipment.Event)
	Reporter func(status string)

	mutex sync.Mutex
	state denonState
	out   chan string
}

// NewClient builds a client for one address. The listener reports every
// recognized line as an equipment.Event.
func NewClient(address string, listener func(equipment.Event)) *Client {
	return &Client{
		address:  denonAddress(address),
		listener: listener,
		state:    newDenonState(),
	}
}

// reportCommand tells the wired Reporter about one command's outcome.
func (d *Client) reportCommand(status string) {
	if d.Reporter != nil {
		d.Reporter(status)
	}
}

// State returns the latest receiver state, translated into equipment
// units, and can run concurrently with the reader and writer
// goroutines.
func (d *Client) State() equipment.State {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.equipmentState(d.state)
}

// equipmentState maps the receiver's own units onto the equipment
// contract: the main zone always, and a second or third zone only once
// the receiver has named it.
func (d *Client) equipmentState(s denonState) equipment.State {
	zones := map[string]equipment.ZoneState{
		equipment.MainZone: zoneFor(s.Main),
	}
	if s.Zone2.seen {
		zones[zone2] = zoneFor(s.Zone2)
	}
	if s.Zone3.seen {
		zones[zone3] = zoneFor(s.Zone3)
	}
	return equipment.State{Reachable: s.Reachable, Zones: zones}
}

// zoneFor translates one Denon zone into the equipment contract.
func zoneFor(zone zoneState) equipment.ZoneState {
	return equipment.ZoneState{
		Power:     zone.Power,
		Input:     zone.Input,
		SoundMode: zone.SoundMode,
		Mute:      zone.Mute,
		Volume:    zone.Volume,
		VolumeMax: zone.VolumeMax,
		Sleep:     zone.Sleep,
	}
}

// VolumeResolution is the number of half steps in one display unit.
func (d *Client) VolumeResolution() int {
	return 2
}

// SetPower turns one zone on or to standby. The main zone answers PW,
// and a second or third zone answers its own Z2 or Z3 pair. An unknown
// zone is an error.
func (d *Client) SetPower(zone string, on bool) error {
	switch zone {
	case equipment.MainZone:
		if on {
			return d.send(PowerOnCommand)
		}
		return d.send(powerStandbyCommand)
	case zone2, zone3:
		if on {
			return d.send(zonePrefix(zone) + "ON")
		}
		return d.send(zonePrefix(zone) + "OFF")
	}
	return unknownZone(zone)
}

// SetInput selects one input on one zone. The main zone carries its
// input on SI, and a second or third zone on its own Z2 or Z3 prefix.
// An unknown zone is an error.
func (d *Client) SetInput(zone, input string) error {
	switch zone {
	case equipment.MainZone:
		return d.send(InputCommand(input))
	case zone2, zone3:
		return d.send(zonePrefix(zone) + input)
	}
	return unknownZone(zone)
}

// SetVolume sets one zone's volume, in half steps. The receiver's own
// scale ends at denonScaleTop, so a count above it is held to the top
// rather than sent as a command the receiver would refuse. An unknown
// zone is an error.
func (d *Client) SetVolume(zone string, halves int) error {
	if halves > denonScaleTop {
		halves = denonScaleTop
	}
	if halves < 0 {
		halves = 0
	}
	switch zone {
	case equipment.MainZone:
		return d.send(VolumeCommand(halves))
	case zone2, zone3:
		return d.send(zonePrefix(zone) + "MV" + HalfStepDigits(halves))
	}
	return unknownZone(zone)
}

// SetMute sets one zone's mute. The main zone answers MU, and a second
// or third zone answers its own Z2 or Z3 pair. An unknown zone is an
// error.
func (d *Client) SetMute(zone string, muted bool) error {
	switch zone {
	case equipment.MainZone:
		return d.send(MuteCommand(muted))
	case zone2, zone3:
		command := "MUOFF"
		if muted {
			command = "MUON"
		}
		return d.send(zonePrefix(zone) + command)
	}
	return unknownZone(zone)
}

// SetSoundMode selects one sound mode on the main zone. The protocol
// carries sound mode on the main zone only, so any other zone is an
// error.
func (d *Client) SetSoundMode(zone, mode string) error {
	if zone != equipment.MainZone {
		return fmt.Errorf("sound mode on zone %s: the protocol carries sound mode on the main zone only", zone)
	}
	return d.send(SoundModeCommand(mode))
}

// SetSleep sets one zone's sleep timer, in minutes, where zero is off.
// The receiver's timer runs to 120 minutes. An unknown zone or an
// out-of-range value is an error.
func (d *Client) SetSleep(zone string, minutes int) error {
	if minutes < 0 || minutes > 120 {
		return fmt.Errorf("sleep %d minutes on zone %s", minutes, zone)
	}
	var command string
	switch zone {
	case equipment.MainZone:
		command = sleepCommand(minutes)
	case zone2, zone3:
		command = zonePrefix(zone) + sleepCommand(minutes)
	default:
		return unknownZone(zone)
	}
	return d.send(command)
}

// unknownZone is the error a setter answers when a caller names a zone
// the receiver does not carry.
func unknownZone(zone string) error {
	return fmt.Errorf("unknown zone %q", zone)
}

// zonePrefix is the wire prefix a second or third zone carries, so the
// caller can build the Z2 and Z3 command families from it.
func zonePrefix(zone string) string {
	if zone == zone3 {
		return "Z3"
	}
	return "Z2"
}

// sleepCommand is one zone's sleep command, SLP for a timer and SLPOFF
// for none.
func sleepCommand(minutes int) string {
	if minutes == 0 {
		return "SLPOFF"
	}
	return fmt.Sprintf("SLP%03d", minutes)
}

// send queues one command for the current connection and reports
// whether it was delivered. A disconnected client has no queue, a full
// queue drops the command, and a queue whose writer has stopped during
// teardown is dead, so each reports an error rather than claiming the
// command is on its way. Commands are one-shot; replaying one after a
// reconnect could overwrite a change made at the receiver while the
// connection was down.
func (d *Client) send(command string) error {
	d.mutex.Lock()
	out := d.out
	if out == nil {
		d.mutex.Unlock()
		d.reportCommand(CommandFailed)
		return fmt.Errorf("no connection to send %q", command)
	}
	// The enqueue happens under the mutex, so the writer's shutdown, which
	// nils out under the same mutex, cannot run between the check above and
	// this send. A command that returns nil was accepted while the writer
	// was still accepting; whether it reaches the wire is best effort,
	// because the connection can die first and report the command failed.
	select {
	case out <- command:
		d.mutex.Unlock()
		return nil
	default:
		d.mutex.Unlock()
		d.reportCommand(CommandFailed)
		return fmt.Errorf("the send queue is full, dropped %q", command)
	}
}

// Run keeps reconnecting until ctx ends and waits between sessions. A
// session that receives a recognized reply resets the backoff. A
// session that does not receive a recognized reply increases the
// backoff up to maxBackoff.
func (d *Client) Run(ctx context.Context) {
	backoff := minBackoff
	for ctx.Err() == nil {
		answered := d.runSession(ctx)
		d.record(equipment.ConditionFalse)
		if ctx.Err() != nil {
			return
		}
		if answered {
			backoff = minBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !answered {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// runSession dials, sends the five queries, and reads until the read
// deadline or a socket error ends the session. It reports whether any
// recognized reply arrived. Run uses that result to reset or increase
// the reconnect backoff.
func (d *Client) runSession(parent context.Context) (answered bool) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(parent, "tcp", d.address)
	if err != nil {
		return false
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// The reader blocks until the receiver writes or the socket closes.
	// Closing the socket is what unblocks a reader on a silent receiver.
	defer context.AfterFunc(ctx, func() { conn.Close() })()

	out := make(chan string, queueDepth)
	d.mutex.Lock()
	d.out = out
	d.mutex.Unlock()

	var writing sync.WaitGroup
	writing.Add(1)
	go func() {
		defer writing.Done()
		defer cancel()
		d.writeLoop(ctx, conn, out)
		// Stop accepting new commands before the writer is gone, then
		// drain what was already accepted. The send path holds the mutex
		// while it enqueues, so taking it here means no send occurs between
		// its nil check and its enqueue: a command accepted before this
		// point is drained, and a send after it sees nil and reports
		// failure instead of leaving a command in a queue nobody drains.
		d.mutex.Lock()
		d.out = nil
		d.mutex.Unlock()
		// This drain never delivers: cancel() closes the connection before
		// it runs, so every write here fails and reports CommandFailed,
		// which is correct because the drained commands were never written.
		for {
			select {
			case command := <-out:
				if _, err := conn.Write([]byte(command + string(Terminator))); err != nil {
					d.reportCommand(CommandFailed)
				} else {
					d.reportCommand(CommandOK)
				}
			default:
				return
			}
		}
	}()

	for _, query := range Queries {
		d.send(query)
	}

	answered = d.readLoop(conn)
	cancel()
	writing.Wait()
	return answered
}

// writeLoop is the only goroutine that writes the socket. It sends a
// heartbeat query after a heartbeat interval with no other write.
func (d *Client) writeLoop(ctx context.Context, conn net.Conn, out <-chan string) {
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case command := <-out:
			if _, err := conn.Write([]byte(command + string(Terminator))); err != nil {
				d.reportCommand(CommandFailed)
				return
			}
			d.reportCommand(CommandOK)
			last = time.Now()
		case <-ticker.C:
			// The heartbeat is a keepalive this client invents, not a
			// command an operator sent for its own reasons, so it never
			// reaches equipment_commands_total: a socket failure here is
			// already reported when the read loop's silence limit or a
			// failed write ends the session and flips Reachable.
			if time.Since(last) < heartbeat {
				continue
			}
			if _, err := conn.Write([]byte("PW?" + string(Terminator))); err != nil {
				return
			}
			last = time.Now()
		}
	}
}

// readLoop folds each complete line into the state. It returns when the
// read deadline expires or the socket reports an error.
func (d *Client) readLoop(conn net.Conn) (answered bool) {
	reader := bufio.NewReader(conn)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(silenceLimit)); err != nil {
			return answered
		}
		line, err := reader.ReadString(Terminator)
		if err != nil {
			return answered
		}
		line = strings.Trim(line, "\r\n")
		if line == "" {
			continue
		}
		if d.fold(line) {
			answered = true
		}
	}
}

// fold applies one recognized line under the lock, sends the resulting
// state to the listener, and reports whether the line was recognized.
func (d *Client) fold(line string) bool {
	d.mutex.Lock()
	folded, zone, field, known := applyDenonLine(d.state, line)
	if !known {
		d.mutex.Unlock()
		return false
	}
	// A recognized line proves that the receiver answered, so it makes
	// Reachable true.
	folded.Reachable = equipment.ConditionTrue
	d.state = folded
	state := d.equipmentState(folded)
	d.mutex.Unlock()

	d.notify(equipment.Event{Zone: zone, Field: equipmentField(field), State: state})
	return true
}

// equipmentField maps the field a line named onto the field name the
// equipment contract carries. The volume limit is a volume change.
func equipmentField(field string) string {
	switch field {
	case powerField:
		return equipment.EventPower
	case inputField:
		return equipment.EventInput
	case volumeField, volumeMaxField:
		return equipment.EventVolume
	case muteField:
		return equipment.EventMute
	case soundModeField:
		return equipment.EventSoundMode
	}
	return ""
}

// record changes the reachability verdict and notifies the listener.
// This is how a dropped connection reaches the Receiver status.
func (d *Client) record(status equipment.ConditionStatus) {
	d.mutex.Lock()
	if d.state.Reachable == status {
		d.mutex.Unlock()
		return
	}
	d.state.Reachable = status
	state := d.equipmentState(d.state)
	d.mutex.Unlock()

	d.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventReachable, State: state})
}

// notify runs on the reading goroutine. A blocking listener therefore
// delays processing of every later line on the connection.
func (d *Client) notify(event equipment.Event) {
	if d.listener != nil {
		d.listener(event)
	}
}

// denonAddress fills in the control port when the declared address
// names none.
func denonAddress(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, Port)
}
