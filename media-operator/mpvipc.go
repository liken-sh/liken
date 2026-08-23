package main

// mpv's JSON IPC socket carries newline-delimited JSON objects in
// both directions: commands in, replies and events out. It is the
// one interface mpv offers a program, and the supervisor drives it
// instead of parsing mpv's terminal output, which is written for a
// person and changes shape freely.
//
// The supervisor observes properties instead of polling them. mpv
// sends the current value once at observe time and then one event
// per change, so the socket carries the whole story with no timer of
// the supervisor's own.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"time"
)

// mpvSocketPath is a variable rather than a constant because the
// same string appears in mpv's --input-ipc-server argument: one
// value is what makes the two ends meet, and a test moves both at
// once.
var mpvSocketPath = ipcSocketPath

// mpv creates the socket a moment after it starts, so a dial retries.
// mpvDialDelay is the pause between tries, a variable so a test drives
// the retry in milliseconds.
var mpvDialDelay = time.Second

// observedProperties are the four properties the Play's status is
// made of. The supervisor asks for no others, because every event
// mpv sends costs a decode and nothing else reaches the status.
var observedProperties = []string{"pause", "playlist-pos", "time-pos", "duration"}

// propertyChangeEvent carries an observed property's new value.
// clientMessageEvent carries a script-message any client broadcast, which is
// how the display asks the bridge to decode a logo. mpv sends many other
// events through the same socket, and readEvents drops them.
const (
	propertyChangeEvent = "property-change"
	clientMessageEvent  = "client-message"
)

// mpvCommand is the shape mpv accepts: an array of the command name
// followed by its arguments.
type mpvCommand struct {
	Command []any `json:"command"`
}

// mpvMessage is the shape of one line mpv writes. Command replies
// and events share the socket, so every field here is absent from
// some of the lines that arrive.
type mpvMessage struct {
	Event string          `json:"event"`
	ID    int             `json:"id"`
	Name  string          `json:"name"`
	Data  json.RawMessage `json:"data"`
	Args  []string        `json:"args"`
}

// clientMessage is one script-message the display broadcast, as its arguments
// reach the bridge over the socket. The first argument names the request, so
// the bridge tells its own requests from any other script's traffic.
type clientMessage struct {
	Args []string
}

// propertyChange is one observed property's new value. The data
// stays raw because each property has its own type, and only the
// fold that applies the change knows which one to decode.
type propertyChange struct {
	Name string
	Data json.RawMessage
}

// known drops a null datum before anything decodes it. mpv writes
// null for a property it has no value for yet, such as time-pos
// before the first frame, and a null decoded into a number would
// report a position of zero that the player never held.
func (c propertyChange) known() bool {
	return string(c.Data) != "null" && len(c.Data) > 0
}

// dialMPV retries until it connects or the context ends. The command
// sidecar is a native sidecar the kubelet starts before mpv, so the
// socket can be minutes away, and the wait has no deadline of its own:
// nothing but mpv appearing or the kubelet's SIGTERM ends it. Waiting is
// the whole job when mpv is not up yet, so a wall-clock limit would only
// quit early on a slow image pull or a display claim that is not ready.
func dialMPV(ctx context.Context, path string) (net.Conn, error) {
	for ctx.Err() == nil {
		connection, err := net.Dial("unix", path)
		if err == nil {
			return connection, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(mpvDialDelay):
		}
	}
	return nil, ctx.Err()
}

// observeProperties gives each property its own observe id because
// mpv requires one, but the supervisor keys off the name in each
// event rather than the id, so the ids only have to be distinct.
func observeProperties(writer io.Writer, names []string) error {
	encoder := json.NewEncoder(writer)
	for index, name := range names {
		command := mpvCommand{Command: []any{"observe_property", index + 1, name}}
		if err := encoder.Encode(command); err != nil {
			return fmt.Errorf("observe %s: %w", name, err)
		}
	}
	return nil
}

// maxEventLine bounds one line of mpv's output, far above anything
// the four observed properties produce.
const maxEventLine = 1 << 20

// readEvents delivers the observed property changes and the client messages,
// and drops everything else: replies, other events, and any line that does not
// decode. mpv's protocol grows new events, and one the supervisor cannot read
// is no reason to stop reporting the ones it can. It ends when the socket
// closes, which is how mpv says the run is over.
func readEvents(ctx context.Context, reader io.Reader, changes chan<- propertyChange, messages chan<- clientMessage) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxEventLine)
	for scanner.Scan() {
		var message mpvMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		switch message.Event {
		case propertyChangeEvent:
			if !slices.Contains(observedProperties, message.Name) {
				continue
			}
			select {
			case changes <- propertyChange{Name: message.Name, Data: message.Data}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case clientMessageEvent:
			select {
			case messages <- clientMessage{Args: message.Args}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return scanner.Err()
}
