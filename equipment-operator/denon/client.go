// Client keeps a TCP connection open to one receiver's control port
// and implements equipment.Driver on it. It applies each line that the
// receiver sends to its copy of the receiver's state, and reconnects
// after a read timeout or a socket error.

package denon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// A half-open TCP connection accepts writes and never delivers a line,
// so an open socket does not prove that the receiver is reachable. The
// client sends a query when it has written nothing for heartbeat, and
// ends the session when no line arrives for silenceLimit.
const (
	dialTimeout  = 5 * time.Second
	heartbeat    = 30 * time.Second
	silenceLimit = 75 * time.Second
	minBackoff   = time.Second
	maxBackoff   = 30 * time.Second
)

// The first full read is the client's read of the receiver's state from the
// replies to Queries after a connect. The receiver sends the replies in
// one burst, so the first full read is complete when no line has arrived for
// firstReadQuiet after the first reply.
const firstReadQuiet = time.Second

// queueDepth is the size of a connection's write queue. It fits every
// entry of Queries and a burst of commands. send returns an error
// for a command that does not fit.
const queueDepth = 64

// The outcomes that Reporter receives for a command. They are the label
// values of equipment_commands_total. The root package reports
// CommandTimeout itself when its own wait for a command expires.
const (
	CommandOK      = "ok"
	CommandFailed  = "failed"
	CommandTimeout = "timeout"
)

// Client is the connection to one receiver, the state that the
// receiver has reported, and the queue of commands to write.
//
// Reporter receives the outcome of each command, for
// equipment_commands_total. It can be nil.
type Client struct {
	address  string
	listener func(equipment.Event)
	Reporter func(status string)
	// Dial opens the control connection and the HTTP connection that
	// reads the receiver's description. When nil, the client dials TCP.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)

	mutex sync.Mutex
	state denonState
	// out is the current connection's write queue, and nil while there
	// is no connection.
	out  chan string
	peer string
	// firstReadDone is true after the first full read completes. Until then,
	// state can hold defaults that the receiver has not reported.
	firstReadDone bool
	// describing is true while a description read runs, so a quick
	// reconnect does not start a second one.
	describing bool
	// descriptionURL overrides the URL that describe reads, for a test.
	// When empty, describe reads the AIOS description from peer.
	descriptionURL string
}

// NewClient returns a client for address, which can omit the port.
// listener receives an equipment.Event for each recognized line, each
// change of Reachable, the model read from the description, and the end
// of the first full read.
func NewClient(address string, listener func(equipment.Event)) *Client {
	return &Client{
		address:  denonAddress(address),
		listener: listener,
		state:    newDenonState(),
	}
}

func (d *Client) reportCommand(status string) {
	if d.Reporter != nil {
		d.Reporter(status)
	}
}

// State returns the latest receiver state. It is safe to call from any
// goroutine.
func (d *Client) State() equipment.State {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.equipmentState(d.state)
}

// equipmentState converts the receiver's state to equipment.State.
// Zone 2 and zone 3 are present only after the receiver has reported
// them.
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
	return equipment.State{Reachable: s.Reachable, Zones: zones, Model: s.model, Manufacturer: s.manufacturer}
}

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

// VolumeResolution returns 2, because the receiver's volume moves in
// half steps of the displayed number.
func (d *Client) VolumeResolution() int {
	return 2
}

// HasStandby returns true, because PWSTANDBY puts the receiver in
// standby.
func (d *Client) HasStandby() bool { return true }

// SetPower turns one zone on or off.
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

// SetInput selects an input on one zone.
func (d *Client) SetInput(zone, input string) error {
	switch zone {
	case equipment.MainZone:
		return d.send(InputCommand(input))
	case zone2, zone3:
		return d.send(zonePrefix(zone) + input)
	}
	return unknownZone(zone)
}

// SetVolume sets one zone's volume in half steps. It clamps the count
// to the receiver's scale, 0 to denonScaleTop, because the receiver
// refuses a value outside it.
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

// SetMute mutes or unmutes one zone.
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

// SetSoundMode selects a sound mode. The protocol has a sound mode
// command for the main zone only, so any other zone returns an error.
func (d *Client) SetSoundMode(zone, mode string) error {
	if zone != equipment.MainZone {
		return fmt.Errorf("the protocol sets the sound mode on the main zone only, not on zone %q", zone)
	}
	return d.send(SoundModeCommand(mode))
}

// SameSoundMode reports whether the reported mode matches the declared
// mode, by the rules of the package function SameSoundMode.
func (d *Client) SameSoundMode(declared, reported string) bool {
	return SameSoundMode(declared, reported)
}

// SetSleep sets one zone's sleep timer in minutes, and 0 turns it off.
// The receiver accepts 0 to 120 minutes, so a value outside that range
// returns an error.
func (d *Client) SetSleep(zone string, minutes int) error {
	if minutes < 0 || minutes > 120 {
		return fmt.Errorf("sleep timer of %d minutes on zone %q is outside the range 0 to 120", minutes, zone)
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

func unknownZone(zone string) error {
	return fmt.Errorf("zone %q is not one of %q, %q, %q", zone, equipment.MainZone, zone2, zone3)
}

func zonePrefix(zone string) string {
	if zone == zone3 {
		return "Z3"
	}
	return "Z2"
}

func sleepCommand(minutes int) string {
	if minutes == 0 {
		return "SLPOFF"
	}
	return fmt.Sprintf("SLP%03d", minutes)
}

// send queues a command on the current connection. It reports
// CommandFailed and returns an error when there is no connection or the
// queue is full. A nil return means only that the command was queued.
// When the connection fails before the write, the writer reports
// CommandFailed. The client does not send the command again after a
// reconnect, because it could undo a change that someone made at the
// receiver while the connection was down.
func (d *Client) send(command string) error {
	d.mutex.Lock()
	out := d.out
	if out == nil {
		d.mutex.Unlock()
		d.reportCommand(CommandFailed)
		return fmt.Errorf("the receiver is not connected, so %q was not sent", command)
	}
	// Queue while holding mutex. runSession sets out to nil under the
	// same mutex before it drains the queue, so a command queued here is
	// always drained.
	select {
	case out <- command:
		d.mutex.Unlock()
		return nil
	default:
		d.mutex.Unlock()
		d.reportCommand(CommandFailed)
		return fmt.Errorf("the send queue is full, so %q was not sent", command)
	}
}

func (d *Client) dialer() func(ctx context.Context, network, address string) (net.Conn, error) {
	if d.Dial != nil {
		return d.Dial
	}
	return (&net.Dialer{Timeout: dialTimeout}).DialContext
}

// Run connects to the receiver, and reconnects until ctx ends. After
// each session it sets Reachable to false. It waits minBackoff before
// the next connect, and the wait doubles, up to maxBackoff, after each
// session in which the receiver sent no recognized line.
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

// runSession dials, queues every entry of Queries, and reads until a
// read fails. It returns whether any recognized line arrived.
func (d *Client) runSession(parent context.Context) (answered bool) {
	// The status reports an IP address, and the declared address can be
	// a hostname.
	if ip := resolveHost(d.address); ip != "" {
		d.recordAddress(ip)
	}
	conn, err := d.dialer()(parent, "tcp", d.address)
	if err != nil {
		return false
	}
	defer conn.Close()
	if remote, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		d.recordAddress(remote.IP.String())
	}
	d.describe(parent)

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// A read blocks until a line arrives or the socket closes, so close
	// the socket when ctx ends to stop the read.
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
		// Set out to nil so that send refuses new commands, then drain
		// the commands already queued, so each one reports an outcome.
		d.mutex.Lock()
		d.out = nil
		d.mutex.Unlock()
		// The writer stopped because ctx ended, which closes the
		// connection, or because a write failed. So these writes
		// normally fail and report CommandFailed.
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

// writeLoop writes queued commands to conn until ctx ends or a write
// fails. When it has written nothing for heartbeat, it writes PW?, so
// that the receiver replies and the read deadline does not expire on a
// working idle connection.
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
			// The heartbeat is not counted in equipment_commands_total,
			// because the operator did not send it.
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

// readLoop applies each line that the receiver sends to the state,
// until a read fails or no line arrives for silenceLimit. It returns
// whether any line was recognized.
func (d *Client) readLoop(conn net.Conn) (answered bool) {
	reader := bufio.NewReader(conn)
	// The first recognized line starts the timer and each later one
	// restarts it, so it fires firstReadQuiet after the last reply of the
	// burst. A receiver that has sent nothing yet has reported no state,
	// so the wait for its first reply has no limit until silenceLimit
	// ends the connection.
	var firstReadTimer *time.Timer
	defer func() {
		if firstReadTimer != nil {
			firstReadTimer.Stop()
		}
	}()
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
			if firstReadTimer == nil {
				firstReadTimer = time.AfterFunc(firstReadQuiet, d.recordFirstReadDone)
			} else {
				firstReadTimer.Reset(firstReadQuiet)
			}
		}
	}
}

// fold applies one line to the state, notifies the listener, and
// returns whether the line was recognized. A recognized line sets
// Reachable to true.
func (d *Client) fold(line string) bool {
	d.mutex.Lock()
	next, zone, field, known := applyDenonLine(d.state, line)
	if !known {
		d.mutex.Unlock()
		return false
	}
	next.Reachable = equipment.ConditionTrue
	d.state = next
	state := d.equipmentState(next)
	d.mutex.Unlock()

	d.notify(equipment.Event{Zone: zone, Field: equipmentField(field), State: state})
	return true
}

// equipmentField returns the equipment event for a changed field, or
// an empty string for a field that has no event.
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

// record sets Reachable, and notifies the listener when the value
// changes, so a dropped connection shows in the Receiver's status.
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

// FirstReadDone reports whether the first full read is complete. The root
// package applies no declared setting until it is, so it does not
// compare the declaration with default values.
func (d *Client) FirstReadDone() bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.firstReadDone
}

// recordFirstReadDone marks the first full read complete, and notifies the listener
// the first time. The first full read ends when no line arrives, so no line
// can report it, and the timer in readLoop calls this.
func (d *Client) recordFirstReadDone() {
	d.mutex.Lock()
	first := !d.firstReadDone
	d.firstReadDone = true
	state := d.equipmentState(d.state)
	d.mutex.Unlock()
	if first {
		d.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventFirstReadDone, State: state})
	}
}

// Address returns the receiver's IP address for the status: the remote
// address of the connection, or else the declared address resolved
// before the dial. When neither is known, it returns the declared host.
func (d *Client) Address() string {
	d.mutex.Lock()
	peer := d.peer
	d.mutex.Unlock()
	if peer != "" {
		return peer
	}
	return hostOf(d.address)
}

func (d *Client) recordAddress(ip string) {
	d.mutex.Lock()
	d.peer = ip
	d.mutex.Unlock()
}

// resolveHost returns the IP address of address, or an empty string
// when the lookup fails.
func resolveHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if net.ParseIP(host) != nil {
		return host
	}
	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return ips[0]
}

func hostOf(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}

// notify calls the listener on the caller's goroutine. A listener that
// blocks delays the read loop.
func (d *Client) notify(event equipment.Event) {
	if d.listener != nil {
		d.listener(event)
	}
}

// denonAddress adds the control port, Port, when address has none.
func denonAddress(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, Port)
}
