package main

// This file handles one receiver's control connection. It parses the
// Denon ASCII protocol on port 23, records the state each line reports,
// and reconnects when the read deadline or a socket error ends the
// connection.
// The protocol reference is https://assets.denon.com/documentmaster/uk/
// avr1713_avr1613_protocol_v860.pdf. The lines this file parses were
// also read from a live AVR-X1700H.

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// The port a Denon answers control on, used when the declared address
// names none.
const denonPort = "23"

// Commands and replies alike are ASCII terminated by a carriage return.
const denonTerminator = '\r'

// The five queries the operator sends on every connect. Their answers
// are the whole of the status.
var denonQueries = []string{"PW?", "MV?", "MU?", "SI?", "MS?"}

// The set commands this operator sends.
const (
	denonPowerOnCommand      = "PWON"
	denonMuteOnCommand       = "MUON"
	denonMuteOffCommand      = "MUOFF"
	denonVolumeCommandPrefix = "MV"
	denonInputCommandPrefix  = "SI"
)

// The two power words the status carries.
const (
	powerOn      = "on"
	powerStandby = "standby"
)

// These values bound one connection. The heartbeat asks the receiver
// for a reply when no other command has gone out. The silence limit
// ends a session after the socket carries no reply, even if the TCP
// socket remains open. A half-open socket can accept writes without
// delivering them, so an open socket alone does not prove reachability.
var (
	denonDialTimeout  = 5 * time.Second
	denonHeartbeat    = 30 * time.Second
	denonSilenceLimit = 75 * time.Second
	denonMinBackoff   = time.Second
	denonMaxBackoff   = 30 * time.Second
)

// The write queue for one connection. A command that would overflow it
// is dropped, because the operator re-asserts nothing.
const denonQueueDepth = 32

// The fields a line can name, which is what a listener switches on.
const (
	denonPowerField     = "power"
	denonInputField     = "input"
	denonVolumeField    = "volume"
	denonVolumeMaxField = "volumeMax"
	denonMuteField      = "mute"
	denonSoundModeField = "soundMode"
	denonReachableField = "reachable"
)

// denonState stores the latest values the receiver reported in its own
// units. Reachable becomes true on a recognized reply and changes when
// the connection fails.
type denonState struct {
	Power     string
	Input     string
	SoundMode string
	Mute      bool
	Volume    int
	VolumeMax int
	Reachable ConditionStatus
}

// The state before the receiver has said anything.
func newDenonState() denonState {
	return denonState{
		Volume:    unknownHalves,
		VolumeMax: unknownHalves,
		Reachable: ConditionUnknown,
	}
}

// denonEvent is one recognized line: the field it named and the whole
// state after it.
type denonEvent struct {
	Field string
	State denonState
}

// applyDenonLine folds one line into the state and answers which field
// it named. A line this operator does not know is ignored, because a
// Denon volunteers dozens of settings nothing here reads.
func applyDenonLine(state denonState, line string) (denonState, string, bool) {
	switch {
	case line == "PWON":
		state.Power = powerOn
		return state, denonPowerField, true
	case line == "PWSTANDBY":
		state.Power = powerStandby
		return state, denonPowerField, true
	case line == denonMuteOnCommand:
		state.Mute = true
		return state, denonMuteField, true
	case line == denonMuteOffCommand:
		state.Mute = false
		return state, denonMuteField, true
	case strings.HasPrefix(line, "MVMAX "):
		halves, ok := parseHalfSteps(strings.TrimSpace(line[len("MVMAX "):]))
		if !ok {
			return state, "", false
		}
		state.VolumeMax = halves
		return state, denonVolumeMaxField, true
	case strings.HasPrefix(line, denonVolumeCommandPrefix):
		halves, ok := parseHalfSteps(line[len(denonVolumeCommandPrefix):])
		if !ok {
			return state, "", false
		}
		state.Volume = halves
		return state, denonVolumeField, true
	case strings.HasPrefix(line, denonInputCommandPrefix) && len(line) > 2:
		state.Input = line[2:]
		return state, denonInputField, true
	case strings.HasPrefix(line, "MS") && len(line) > 2:
		state.SoundMode = line[2:]
		return state, denonSoundModeField, true
	}
	return state, "", false
}

// denonVolumeCommand is the set command for one half-step count.
func denonVolumeCommand(halves int) string {
	return denonVolumeCommandPrefix + halfStepDigits(halves)
}

// denonMuteCommand is the set command for one mute state.
func denonMuteCommand(muted bool) string {
	if muted {
		return denonMuteOnCommand
	}
	return denonMuteOffCommand
}

// denonInputCommand selects one input by the name the receiver carries
// for it.
func denonInputCommand(input string) string {
	return denonInputCommandPrefix + input
}

// denonAddress fills in the control port when the declared address
// names none.
func denonAddress(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, denonPort)
}

// denonClient holds one receiver's connection, the state it reports,
// and the queue of commands waiting to go out. out is the current
// connection's queue, or nil while disconnected.
//
// readings reports the outcome of every command this client sends, for
// equipment_commands_total. It is set directly by the caller that
// wires up metrics, the way reconcile.go wires the listener; a nil
// value is what a test that does not care about metrics builds, and
// reportCommand on a nil *metrics is a defined no-op.
type denonClient struct {
	address  string
	listener func(denonEvent)
	readings *metrics

	mutex sync.Mutex
	state denonState
	out   chan string
}

func newDenonClient(address string, listener func(denonEvent)) *denonClient {
	return &denonClient{
		address:  denonAddress(address),
		listener: listener,
		state:    newDenonState(),
	}
}

// reportCommand tells the wired metrics about one command's outcome.
func (d *denonClient) reportCommand(status string) {
	d.readings.reportCommand(status)
}

// State returns the latest receiver state and can run concurrently with
// the reader and writer goroutines.
func (d *denonClient) State() denonState {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.state
}

// Send queues one command for the current connection. A disconnected
// client has no queue, so it drops the command. Commands are one-shot.
// Replaying one after a reconnect could overwrite a change made at the
// receiver while the connection was down.
func (d *denonClient) Send(command string) {
	d.mutex.Lock()
	out := d.out
	d.mutex.Unlock()
	if out == nil {
		d.reportCommand(commandFailed)
		return
	}
	select {
	case out <- command:
	default:
		d.reportCommand(commandFailed)
	}
}

// Run keeps reconnecting until ctx ends and waits between sessions. A
// session that receives a recognized reply resets the backoff. A
// session that does not receive a recognized reply increases the
// backoff up to denonMaxBackoff.
func (d *denonClient) Run(ctx context.Context) {
	backoff := denonMinBackoff
	for ctx.Err() == nil {
		answered := d.runSession(ctx)
		d.record(ConditionFalse)
		if ctx.Err() != nil {
			return
		}
		if answered {
			backoff = denonMinBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !answered {
			backoff *= 2
			if backoff > denonMaxBackoff {
				backoff = denonMaxBackoff
			}
		}
	}
}

// runSession dials, sends the five queries, and reads until the read
// deadline or a socket error ends the session. It reports whether any
// recognized reply arrived. Run uses that result to reset or increase
// the reconnect backoff.
func (d *denonClient) runSession(parent context.Context) (answered bool) {
	dialer := &net.Dialer{Timeout: denonDialTimeout}
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

	out := make(chan string, denonQueueDepth)
	d.mutex.Lock()
	d.out = out
	d.mutex.Unlock()
	defer func() {
		d.mutex.Lock()
		d.out = nil
		d.mutex.Unlock()
	}()

	var writing sync.WaitGroup
	writing.Add(1)
	go func() {
		defer writing.Done()
		defer cancel()
		d.writeLoop(ctx, conn, out)
	}()

	for _, query := range denonQueries {
		d.Send(query)
	}

	answered = d.readLoop(conn)
	cancel()
	writing.Wait()
	return answered
}

// writeLoop is the only goroutine that writes the socket. It sends a
// heartbeat query after a heartbeat interval with no other write.
func (d *denonClient) writeLoop(ctx context.Context, conn net.Conn, out <-chan string) {
	ticker := time.NewTicker(denonHeartbeat)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case command := <-out:
			if _, err := conn.Write([]byte(command + string(denonTerminator))); err != nil {
				d.reportCommand(commandFailed)
				return
			}
			d.reportCommand(commandOK)
			last = time.Now()
		case <-ticker.C:
			// The heartbeat is a keepalive this client invents, not a
			// command an operator sent for its own reasons, so it never
			// reaches equipment_commands_total: a socket failure here is
			// already reported when the read loop's silence limit or a
			// failed write ends the session and flips Reachable.
			if time.Since(last) < denonHeartbeat {
				continue
			}
			if _, err := conn.Write([]byte("PW?" + string(denonTerminator))); err != nil {
				return
			}
			last = time.Now()
		}
	}
}

// readLoop folds each complete line into the state. It returns when the
// read deadline expires or the socket reports an error.
func (d *denonClient) readLoop(conn net.Conn) (answered bool) {
	reader := bufio.NewReader(conn)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(denonSilenceLimit)); err != nil {
			return answered
		}
		line, err := reader.ReadString(denonTerminator)
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
func (d *denonClient) fold(line string) bool {
	d.mutex.Lock()
	folded, field, known := applyDenonLine(d.state, line)
	if !known {
		d.mutex.Unlock()
		return false
	}
	// A recognized line proves that the receiver answered, so it makes
	// Reachable true.
	folded.Reachable = ConditionTrue
	d.state = folded
	d.mutex.Unlock()

	d.notify(denonEvent{Field: field, State: folded})
	return true
}

// record changes the reachability verdict and notifies the listener.
// This is how a dropped connection reaches the Receiver status.
func (d *denonClient) record(status ConditionStatus) {
	d.mutex.Lock()
	if d.state.Reachable == status {
		d.mutex.Unlock()
		return
	}
	d.state.Reachable = status
	state := d.state
	d.mutex.Unlock()

	d.notify(denonEvent{Field: denonReachableField, State: state})
}

// notify runs on the reading goroutine. A blocking listener therefore
// delays processing of every later line on the connection.
func (d *denonClient) notify(event denonEvent) {
	if d.listener != nil {
		d.listener(event)
	}
}
