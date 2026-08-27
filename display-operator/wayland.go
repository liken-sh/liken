package main

// This file is the operator's own Wayland client: one standing,
// listen-only connection to the compositor it launched. The registry
// on that connection reports every output the compositor destroys or
// creates, and the output events on it report the mode the
// compositor serves. Both replace guesses the operator used to make
// from the kernel's side of the card.
//
// The registry replaces a comparison of monitor identities. A
// monitor that sleeps and wakes changes the kernel mode on its
// connector and never changes its identity, and weston destroys and
// re-creates the output for it, so a comparison of identities misses
// the flap every sleeping monitor produces. The compositor is the
// party that re-creates outputs, so it is the one source that cannot
// miss one.
//
// The client writes the wire protocol itself, with the sizes and
// opcodes below. It reads four interfaces, and a Wayland library is
// a dependency this image does not otherwise carry.
//
// The connection dies with every compositor restart, the operator's
// own restarts included, and a fresh connection treats the outputs
// it finds as a baseline. That is the whole of the guard against a
// restart reporting itself as a re-creation.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"sync"
	"time"
)

// Every connection starts with wl_display as object 1, before
// the client binds anything, so the first request needs no roundtrip.
const displayObject uint32 = 1

// The request opcodes. An opcode is the position of the request
// in its interface's protocol definition, and the wire carries no
// names, so these numbers are the contract.
const (
	displaySync        uint16 = 0
	displayGetRegistry uint16 = 1
	registryBind       uint16 = 0
)

// The event opcodes this client reads. Every other event is
// skipped by the size its own header carries, so an interface's
// remaining events cost nothing to ignore.
const (
	displayErrorEvent         uint16 = 0
	displayDeleteIDEvent      uint16 = 1
	registryGlobalEvent       uint16 = 0
	registryGlobalRemoveEvent uint16 = 1
	callbackDoneEvent         uint16 = 0
	outputModeEvent           uint16 = 1
	outputDoneEvent           uint16 = 2
	outputNameEvent           uint16 = 4
)

// The one flag of the mode event this client reads: the mode
// the output runs now. The other flag marks the monitor's preferred
// mode, which is the panel's taste and not a fact about the screen.
const outputModeCurrent = 0x1

// The interface the watch binds, and the version whose name
// event carries the connector's own name. Weston 14.0.2 advertises
// version 4. A compositor that advertises less is bound at what it
// offers and its outputs are tracked with no name, because the heal
// decision reads no name; only the mode readback loses.
const (
	outputInterface = "wl_output"
	outputVersion   = 4
)

// Libwayland's own connection buffer is 4096 bytes, so no
// message a compositor sends is larger. The bound keeps a corrupt
// header from allocating whatever its four bytes happen to state.
const maxWaylandMessage = 4096

// How long the watch waits before it dials again. A mode
// switch waits for the connection that follows the restart, so this
// interval is part of every switch, and a quarter second adds little
// to the second the restart already costs.
const compositorDialInterval = 250 * time.Millisecond

var errShortMessage = errors.New("a Wayland message ended inside an argument")

// The arguments of one message, read in order. Every read
// after a failure answers a zero value, so a caller reads its whole
// argument list and checks err once at the end.
type waylandFields struct {
	body []byte
	err  error
}

func (f *waylandFields) uint() uint32 {
	if f.err != nil {
		return 0
	}
	if len(f.body) < 4 {
		f.err = errShortMessage
		return 0
	}
	value := binary.LittleEndian.Uint32(f.body)
	f.body = f.body[4:]
	return value
}

// A string argument is a length that counts the null
// terminator, then the bytes, then padding that takes the whole
// argument to a multiple of four.
func (f *waylandFields) text() string {
	length := f.uint()
	if f.err != nil {
		return ""
	}
	padded := (length + 3) &^ 3
	if uint32(len(f.body)) < padded {
		f.err = errShortMessage
		return ""
	}
	text := f.body[:length]
	f.body = f.body[padded:]
	return string(bytes.TrimRight(text, "\x00"))
}

// The arguments of one request, written in order, in the same
// encoding the fields above read.
type waylandWords struct {
	body []byte
}

func (w *waylandWords) putUint(value uint32) {
	w.body = binary.LittleEndian.AppendUint32(w.body, value)
}

func (w *waylandWords) putText(value string) {
	w.putUint(uint32(len(value)) + 1)
	w.body = append(w.body, value...)
	w.body = append(w.body, 0)
	for len(w.body)%4 != 0 {
		w.body = append(w.body, 0)
	}
}

// One event: the object it came from, the opcode inside that
// object's interface, and the arguments still to be read.
type waylandEvent struct {
	object uint32
	opcode uint16
	fields waylandFields
}

// The connection. Object ids a client creates are its own to
// allocate, counting up from wl_display's 1, and this client never
// reuses one, so the compositor's delete_id events need no
// bookkeeping.
type waylandClient struct {
	socket net.Conn
	reader *bufio.Reader
	lastID uint32
}

func newWaylandClient(socket net.Conn) *waylandClient {
	return &waylandClient{socket: socket, reader: bufio.NewReader(socket), lastID: displayObject}
}

func (c *waylandClient) newID() uint32 {
	c.lastID++
	return c.lastID
}

// A message is the object id, then a word that carries the
// whole message's size in its high half and the opcode in its low
// half, then the arguments. The size counts the eight header bytes.
func (c *waylandClient) request(object uint32, opcode uint16, words waylandWords) error {
	message := make([]byte, 0, 8+len(words.body))
	message = binary.LittleEndian.AppendUint32(message, object)
	message = binary.LittleEndian.AppendUint32(message, uint32(len(words.body)+8)<<16|uint32(opcode))
	message = append(message, words.body...)
	_, err := c.socket.Write(message)
	return err
}

func (c *waylandClient) event() (waylandEvent, error) {
	var header [8]byte
	if _, err := io.ReadFull(c.reader, header[:]); err != nil {
		return waylandEvent{}, err
	}
	object := binary.LittleEndian.Uint32(header[:4])
	word := binary.LittleEndian.Uint32(header[4:])
	size, opcode := word>>16, uint16(word)
	if size < 8 || size > maxWaylandMessage {
		return waylandEvent{}, fmt.Errorf("a Wayland message on object %d states a size of %d bytes", object, size)
	}
	body := make([]byte, size-8)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return waylandEvent{}, err
	}
	return waylandEvent{object: object, opcode: opcode, fields: waylandFields{body: body}}, nil
}

// The standing connection to the compositor, and what it
// reports. moved runs for every wl_output global that arrives or
// leaves, and its argument is true when the connection has seen both
// a removal and a creation since this compositor started, in either
// order, which is an output that was re-created.
type outputWatch struct {
	socketPath string
	moved      func(recreated bool)
	retry      time.Duration

	// The connector each live output global names, and the mode
	// the compositor reports on each connector. Both belong to the
	// standing connection, so both start empty on the connection that
	// follows a restart.
	mu      sync.Mutex
	names   map[uint32]string
	modes   map[string]string
	session uint64
}

func newOutputWatch(socketPath string, moved func(recreated bool)) *outputWatch {
	return &outputWatch{
		socketPath: socketPath,
		moved:      moved,
		retry:      compositorDialInterval,
		names:      map[uint32]string{},
		modes:      map[string]string{},
	}
}

// What the compositor reports it serves: the mode on each
// connector it names, and which connection reported it. The session
// number is how a mode switch tells an answer from the compositor
// that started after its restart from an answer the ended compositor
// left behind.
type servedOutputs struct {
	session uint64
	modes   map[string]string
}

func (w *outputWatch) served() servedOutputs {
	w.mu.Lock()
	defer w.mu.Unlock()
	return servedOutputs{session: w.session, modes: maps.Clone(w.modes)}
}

func (w *outputWatch) name(global uint32, connector string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.names[global] = connector
}

// The mode one output serves, recorded when the done event
// closes the batch that stated it. A batch that named no current
// mode leaves the last answer standing, and a batch that arrives
// before the output's name event is dropped, because a mode with no
// connector answers no question the operator asks.
func (w *outputWatch) serves(global uint32, mode string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	connector := w.names[global]
	if connector == "" || mode == "" {
		return
	}
	w.modes[connector] = mode
}

func (w *outputWatch) forget(global uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.modes, w.names[global])
	delete(w.names, global)
}

// A dead compositor serves nothing. Its answers empty the
// moment the connection ends, not when the next one opens, so the
// window between two compositors reports no mode instead of the
// last answer of the one that died.
func (w *outputWatch) closed() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.names, w.modes = map[uint32]string{}, map[string]string{}
}

// A new connection starts from nothing. Everything the ended
// compositor reported about its outputs goes with it, because a dead
// compositor serves no canvases at any mode.
func (w *outputWatch) opened() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.names, w.modes = map[uint32]string{}, map[string]string{}
	w.session++
}

// Weston states a mode as its size in pixels and its refresh
// in millihertz. A claim, weston.ini, and the kernel all state a
// mode as WIDTHxHEIGHT@REFRESH with the refresh in whole hertz, so
// the event is rendered into that one vocabulary, rounded to the
// nearest hertz the way the kernel rounds its own vrefresh.
func westonMode(width, height, refreshMilliHertz uint32) string {
	if width == 0 || height == 0 {
		return ""
	}
	name := fmt.Sprintf("%dx%d", width, height)
	if refreshMilliHertz == 0 {
		return name
	}
	return fmt.Sprintf("%s@%d", name, (refreshMilliHertz+500)/1000)
}

// The loop. One session runs for as long as the compositor
// lives, and the wait between sessions is the price of a compositor
// that is restarting. Nothing here reports a failure, because a
// session ends every time the operator restarts the compositor
// itself, and a log line for every planned restart would say
// nothing.
func (w *outputWatch) run(ctx context.Context) {
	for {
		_ = w.connection(ctx)
		w.closed()
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.retry):
		}
	}
}

// One connection, from the dial to the end of the compositor.
// The sync marks the end of the first burst of globals. A fresh
// compositor lays every canvas out right, so the outputs it
// announces before the sync answers are the baseline and owe
// nothing.
func (w *outputWatch) connection(ctx context.Context) error {
	socket, err := net.DialTimeout("unix", w.socketPath, socketDialTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = socket.Close() }()

	// The read loop below blocks in the kernel, so the way the
	// context ends this connection is by closing the socket under it.
	ended := make(chan struct{})
	defer close(ended)
	go func() {
		select {
		case <-ctx.Done():
			_ = socket.Close()
		case <-ended:
		}
	}()

	w.opened()
	client := newWaylandClient(socket)
	registry := client.newID()
	var words waylandWords
	words.putUint(registry)
	if err := client.request(displayObject, displayGetRegistry, words); err != nil {
		return err
	}
	baseline := client.newID()
	words = waylandWords{}
	words.putUint(baseline)
	if err := client.request(displayObject, displaySync, words); err != nil {
		return err
	}

	// What this connection has seen. live turns on when the
	// first burst ends, and the two marks are the halves of one
	// re-creation, cleared together when the report pairs them.
	var live, removed, created bool
	outputs := map[uint32]uint32{}
	bound := map[uint32]uint32{}
	// The current mode each output has stated since its last
	// done event. The protocol makes a batch of output events atomic
	// at the done event, so a mode is not the output's answer until
	// the done event closes the batch it came in.
	stating := map[uint32]string{}
	report := func() {
		if removed && created {
			removed, created = false, false
			w.moved(true)
			return
		}
		w.moved(false)
	}

	for {
		event, err := client.event()
		if err != nil {
			return err
		}
		switch {
		case event.object == displayObject && event.opcode == displayErrorEvent:
			object := event.fields.uint()
			code := event.fields.uint()
			message := event.fields.text()
			return fmt.Errorf("the compositor refused object %d with code %d: %s", object, code, message)
		case event.object == displayObject && event.opcode == displayDeleteIDEvent:
			// The compositor releases an id the client may
			// reuse. This client counts up and reuses none.
		case event.object == baseline && event.opcode == callbackDoneEvent:
			live = true
		case event.object == registry && event.opcode == registryGlobalEvent:
			global := event.fields.uint()
			name := event.fields.text()
			version := event.fields.uint()
			if err := event.fields.err; err != nil {
				return err
			}
			if name != outputInterface {
				continue
			}
			id := client.newID()
			words = waylandWords{}
			words.putUint(global)
			words.putText(outputInterface)
			words.putUint(min(version, outputVersion))
			words.putUint(id)
			if err := client.request(registry, registryBind, words); err != nil {
				return err
			}
			outputs[global], bound[id] = id, global
			created = created || live
			report()
		case event.object == registry && event.opcode == registryGlobalRemoveEvent:
			global := event.fields.uint()
			if err := event.fields.err; err != nil {
				return err
			}
			id, ours := outputs[global]
			if !ours {
				continue
			}
			delete(outputs, global)
			delete(bound, id)
			delete(stating, id)
			w.forget(global)
			removed = removed || live
			report()
		default:
			global, ours := bound[event.object]
			if !ours {
				continue
			}
			switch event.opcode {
			case outputNameEvent:
				connector := event.fields.text()
				if err := event.fields.err; err != nil {
					return err
				}
				w.name(global, connector)
			case outputModeEvent:
				flags := event.fields.uint()
				width := event.fields.uint()
				height := event.fields.uint()
				refresh := event.fields.uint()
				if err := event.fields.err; err != nil {
					return err
				}
				if flags&outputModeCurrent == 0 {
					continue
				}
				stating[event.object] = westonMode(width, height, refresh)
			case outputDoneEvent:
				w.serves(global, stating[event.object])
				delete(stating, event.object)
			}
		}
	}
}
