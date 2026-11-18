package main

// mpv's JSON IPC socket carries newline-delimited JSON objects in
// both directions: commands to mpv, and replies and events from it.
// The command sidecar uses it instead of parsing mpv's terminal
// output, which is written for a person and can change format.
//
// The sidecar observes properties instead of polling them. After an
// observe_property command, mpv sends the current value once and then
// one event for each change, so the sidecar needs no timer.

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

// mpvSocketPath is a variable so a test can move the socket. It is also
// the value of mpv's --input-ipc-server argument, so the sidecar and mpv
// always use the same path.
var mpvSocketPath = ipcSocketPath

// mpvDialDelay is the wait between dial attempts. mpv creates its socket
// shortly after it starts, so the first attempt often fails.
const mpvDialDelay = time.Second

// The properties that the Play's status comes from. The current-tracks
// entries are the languages of the audio and subtitle tracks that mpv
// selected.
const (
	audioLanguageProperty    = "current-tracks/audio/lang"
	subtitleLanguageProperty = "current-tracks/sub/lang"

	// The properties of the decode metrics. The command sidecar exports
	// them as its own Prometheus series and does not send them to the
	// operator in a play report. codecProperty and hardwareProperty set
	// the labels of media_decode_info, droppedFramesProperty sets
	// media_dropped_frames_total, and delayProperty sets
	// media_av_delay_seconds.
	//
	// mpv's manual defines no video-codec property. current-tracks/video
	// is the selected entry of track-list, and its codec is a short name
	// such as h264, which suits a metric label.
	codecProperty         = "current-tracks/video/codec"
	hardwareProperty      = "hwdec-current"
	droppedFramesProperty = "frame-drop-count"
	delayProperty         = "avsync"
)

var observedProperties = []string{
	"pause", "playlist-pos", "time-pos", "duration",
	audioLanguageProperty, subtitleLanguageProperty,
	codecProperty, hardwareProperty, droppedFramesProperty, delayProperty,
}

// The events that readEvents passes on. A client-message event carries a
// script-message that a client broadcast, which is how the display sends a
// request to the command sidecar. mpv sends file-loaded after it opens a
// file and selects its tracks, and the sidecar chooses the subtitle track
// then.
const (
	propertyChangeEvent = "property-change"
	clientMessageEvent  = "client-message"
	fileLoadedEvent     = "file-loaded"
)

// mpvCommand is one command for mpv: the command name followed by its
// arguments. When RequestID is set, mpv copies it into the reply.
// mpvrequests.go sets it for the commands that need a reply.
type mpvCommand struct {
	Command   []any `json:"command"`
	RequestID int   `json:"request_id,omitempty"`
}

// mpvMessage is one line that mpv writes. Replies and events share the
// socket, so each line leaves some of these fields empty.
type mpvMessage struct {
	Event string          `json:"event"`
	ID    int             `json:"id"`
	Name  string          `json:"name"`
	Data  json.RawMessage `json:"data"`
	Args  []string        `json:"args"`

	// A reply has no event. It has the request_id of the command and an
	// error string, which is "success" when the command worked.
	RequestID int    `json:"request_id"`
	Error     string `json:"error"`
}

// clientMessage is a script-message that a client broadcast. The first
// argument is the message name, which tells the display's requests from
// the messages of other scripts.
type clientMessage struct {
	Args []string
}

// propertyChange is the new value of one observed property. Data stays
// raw JSON because each property has its own type, and the code that
// handles the property decodes it.
type propertyChange struct {
	Name string
	Data json.RawMessage
}

// known reports whether the change has a value. mpv sends null for a
// property that has no value yet, such as time-pos before the first
// frame. Decoding null into a number gives zero, which would report a
// position that the player never had.
func (c propertyChange) known() bool {
	return string(c.Data) != "null" && len(c.Data) > 0
}

// dialMPV retries until it connects or the context ends. The command
// sidecar is a native sidecar, which the kubelet starts before mpv. A slow
// image pull or a display claim that is not ready can delay mpv for a long
// time, so dialMPV has no deadline of its own.
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

// observeProperties sends an observe_property command for each name. mpv
// requires an id for each observation. The ids only need to be distinct,
// because readEvents matches events by property name.
func observeProperties(writer io.Writer, names []string) error {
	encoder := json.NewEncoder(writer)
	for index, name := range names {
		command := mpvCommand{Command: []any{"observe_property", index + 1, name}}
		if err := encoder.Encode(command); err != nil {
			return fmt.Errorf("observing the mpv property %s: %w", name, err)
		}
	}
	return nil
}

// maxEventLine is the longest line of mpv output that readEvents accepts.
const maxEventLine = 1 << 20

// readEvents reads mpv's output and sends each observed property change,
// client message, file-loaded event, and reply with a request id to its
// channel. It skips every other line, including lines that are not valid
// JSON, so an event that a new mpv version adds does no harm. A nil replies
// or loads channel skips those messages. readEvents returns when the
// socket closes, which means that mpv exited.
func readEvents(
	ctx context.Context, reader io.Reader, changes chan<- propertyChange,
	messages chan<- clientMessage, replies chan<- mpvReply, loads chan<- struct{},
) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxEventLine)
	for scanner.Scan() {
		var message mpvMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		switch message.Event {
		case "":
			if message.RequestID == 0 || replies == nil {
				continue
			}
			select {
			case replies <- mpvReply{ID: message.RequestID, Error: message.Error, Data: replyData(message.Data)}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case propertyChangeEvent:
			if !slices.Contains(observedProperties, message.Name) {
				continue
			}
			select {
			case changes <- propertyChange{Name: message.Name, Data: message.Data}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case fileLoadedEvent:
			if loads == nil {
				continue
			}
			select {
			case loads <- struct{}{}:
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

// replyData returns a reply's data as JSON text, so an mpvReply can be
// compared with ==. mpv writes null when a command returns no value, and
// replyData returns the empty string for it.
func replyData(data json.RawMessage) string {
	if string(data) == "null" {
		return ""
	}
	return string(data)
}
