package main

// This file is the operator's Wayland client. It keeps a read-only
// connection to the compositor that the operator launched, and reads
// two things that the kernel's DRM state does not show: when the
// compositor destroys and creates outputs (the wl_registry global and
// global_remove events), and the mode the compositor runs on each
// output (the wl_output events).
//
// When a monitor sleeps and wakes, weston destroys its output and
// creates it again. The monitor's EDID stays the same, so only the
// registry events show the re-creation.
//
// The client encodes the wire protocol itself, so the image needs no
// Wayland library.
//
// Every compositor restart, including the operator's own, ends the
// connection. The next connection takes the outputs it finds as a
// baseline and reports none of them as re-created.

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

// wl_display is object 1 on every connection.
const displayObject uint32 = 1

// An opcode is the position of the request or event in its
// interface's protocol definition. The wire carries no names.
const (
	displaySync        uint16 = 0
	displayGetRegistry uint16 = 1
	registryBind       uint16 = 0
)

// The client skips every other event by the size in its header.
const (
	displayErrorEvent         uint16 = 0
	displayDeleteIDEvent      uint16 = 1
	registryGlobalEvent       uint16 = 0
	registryGlobalRemoveEvent uint16 = 1
	callbackDoneEvent         uint16 = 0
	outputGeometryEvent       uint16 = 0
	outputModeEvent           uint16 = 1
	outputDoneEvent           uint16 = 2
	outputNameEvent           uint16 = 4
)

// The geometry event sends the position, the physical size, and the
// subpixel order before the monitor's make and model.
const outputGeometryUnusedArgs = 5

// The flag of the mode the output runs now. The other flag marks the
// monitor's preferred mode, which can differ from it.
const outputModeCurrent = 0x1

// Version 4 is the first version of wl_output with the name event,
// which sends the connector's name. When a compositor offers a lower
// version, the client binds that version, and its outputs have no
// connector name. The watch then records no modes, and it reports
// every re-creation as one that needs a compositor restart, because
// it cannot match the new output to the old one.
const (
	outputInterface = "wl_output"
	outputVersion   = 4
)

// From version 2, an output sends a done event after each batch of
// its events. The watch records a mode only at the done event, so it
// records none for an output bound below this version.
const outputDoneVersion uint32 = 2

// Libwayland's connection buffer is 4096 bytes, so a compositor never
// sends a larger message. The limit stops a corrupt size in a header
// from allocating up to 64 KiB.
const maxWaylandMessage = 4096

// After a failed dial, the watch waits compositorDialInterval, and
// doubles the wait after each further failure up to
// compositorDialLimit. The creation of the socket file ends a wait
// early. A dial that the creation started and that failed waits
// listenGrace instead (arrivals.go).
const (
	compositorDialInterval = 250 * time.Millisecond
	compositorDialLimit    = 4 * time.Second
)

var errShortMessage = errors.New("a Wayland message ends inside an argument")

// waylandFields reads the arguments of one message in order. After a
// read fails, every later read returns zero, so a caller reads the
// whole argument list and checks err once at the end.
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

// skip discards count 32-bit arguments.
func (f *waylandFields) skip(count int) {
	for range count {
		f.uint()
	}
}

// text reads a string argument: a length that includes the null
// terminator, then the bytes, then zero padding to a multiple of four
// bytes.
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

// waylandWords builds the arguments of one request, in the encoding
// that waylandFields reads.
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

type waylandEvent struct {
	object uint32
	opcode uint16
	fields waylandFields
}

// panelIdentity is the monitor's make and model and the mode it runs.
// When an output is re-created, the watch asks for a compositor
// restart only when the new output's panelIdentity differs from the
// old one's.
type panelIdentity struct {
	monitor string
	mode    string
}

// outputIdentity is what an output's own events reported. The watch
// keeps it because, by the time an output is removed, the kernel has
// marked the connector disconnected, and sysfs no longer has the
// monitor that was on it.
type outputIdentity struct {
	connector string
	panel     panelIdentity
}

// waylandClient is one connection to the compositor. It allocates
// object ids counting up from wl_display's 1 and never reuses one, so
// it ignores the compositor's delete_id events.
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

// waylandMessage encodes a request: the object id, then a 32-bit word
// with the message size, header included, in its high 16 bits and the
// opcode in its low 16 bits, then the arguments. It returns the bytes
// without writing them because the capture client (captureclient.go)
// sends them together with a file descriptor.
func waylandMessage(object uint32, opcode uint16, words waylandWords) []byte {
	message := make([]byte, 0, 8+len(words.body))
	message = binary.LittleEndian.AppendUint32(message, object)
	message = binary.LittleEndian.AppendUint32(message, uint32(len(words.body)+8)<<16|uint32(opcode))
	return append(message, words.body...)
}

func (c *waylandClient) request(object uint32, opcode uint16, words waylandWords) error {
	_, err := c.socket.Write(waylandMessage(object, opcode, words))
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
		return waylandEvent{}, fmt.Errorf("a Wayland message on object %d has a size of %d bytes, want 8 to %d", object, size, maxWaylandMessage)
	}
	body := make([]byte, size-8)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return waylandEvent{}, err
	}
	return waylandEvent{object: object, opcode: opcode, fields: waylandFields{body: body}}, nil
}

// outputWatch keeps a connection to the compositor, dials again after
// each restart, and records the compositor's outputs. It calls
// outputsChanged once for each output that is added or removed, with
// recreated true when an output came back with a different monitor or
// mode than the one it replaced. When the same monitor comes back at
// the same mode, the canvases already have the right size, and a
// compositor restart would end every client for nothing.
type outputWatch struct {
	socketPath     string
	outputsChanged func(recreated bool)
	// The first wait between dials and the longest one. Tests change
	// them.
	retry      time.Duration
	retryLimit time.Duration
	// connected runs after each new connection, once the card gate is
	// open. While the gate was closed, the passes that read the card
	// left its fields empty, so the operator uses the hook to run them
	// again.
	connected func()

	// names maps each output global to its connector. modes maps each
	// connector to the mode the compositor runs on it. Both cover the
	// current connection only.
	mu      sync.Mutex
	names   map[uint32]string
	modes   map[string]string
	session uint64
	// live is true while a connection is open, and the card gate reads
	// it. Weston opens the card before it listens on its socket, so
	// while live is true the compositor holds DRM master, and the
	// operator must not open the card.
	live bool
	// changed is closed and replaced when names, modes, session, or
	// live changes. A mode switch waits on it for the restarted
	// compositor.
	changed chan struct{}
}

func newOutputWatch(socketPath string, outputsChanged func(recreated bool)) *outputWatch {
	return &outputWatch{
		socketPath:     socketPath,
		outputsChanged: outputsChanged,
		retry:          compositorDialInterval,
		retryLimit:     compositorDialLimit,
		names:          map[uint32]string{},
		modes:          map[string]string{},
		changed:        make(chan struct{}),
	}
}

// wakeWaiters must run with mu held.
func (w *outputWatch) wakeWaiters() {
	close(w.changed)
	w.changed = make(chan struct{})
}

// servedOutputs is a snapshot of the mode on each connector and the
// number of the connection that reported the modes. A mode switch
// compares session numbers so that it does not accept the modes of the
// compositor that the switch restarted.
type servedOutputs struct {
	session uint64
	modes   map[string]string
	// changed is nil when the source has no change signal, and a
	// reader then polls on its fallback timer.
	changed <-chan struct{}
}

func (w *outputWatch) served() servedOutputs {
	w.mu.Lock()
	defer w.mu.Unlock()
	return servedOutputs{session: w.session, modes: maps.Clone(w.modes), changed: w.changed}
}

func (w *outputWatch) recordConnector(global uint32, connector string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.names[global] = connector
}

// recordMode runs at an output's done event, because the events
// before it take effect together. When the batch had no current mode,
// the previous mode stays. An output with no connector name records
// nothing, because modes are keyed by connector.
func (w *outputWatch) recordMode(global uint32, mode string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	connector := w.names[global]
	if connector == "" || mode == "" {
		return
	}
	w.modes[connector] = mode
	w.wakeWaiters()
}

func (w *outputWatch) removeOutput(global uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.modes, w.names[global])
	delete(w.names, global)
	w.wakeWaiters()
}

// connectionEnded clears the outputs and closes the card gate as soon
// as the connection ends, so between two compositors the watch reports
// no modes, not the last modes of the one that exited. The gate closes
// because if the operator opened the card while no compositor held DRM
// master, it would take master from the next compositor.
func (w *outputWatch) connectionEnded() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.names, w.modes = map[uint32]string{}, map[string]string{}
	w.live = false
	w.wakeWaiters()
}

// connectionStarted opens the card gate before it runs the connected
// hook, so a pass that the hook wakes finds the gate open.
func (w *outputWatch) connectionStarted() {
	w.mu.Lock()
	w.names, w.modes = map[uint32]string{}, map[string]string{}
	w.session++
	w.live = true
	w.wakeWaiters()
	w.mu.Unlock()
	if w.connected != nil {
		w.connected()
	}
}

func (w *outputWatch) serving() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.live
}

// westonMode formats a mode event as WIDTHxHEIGHT@REFRESH. The event
// gives the refresh in millihertz, and claims, weston.ini, and the
// kernel use whole hertz, so this rounds to the nearest hertz as the
// kernel rounds vrefresh. A zero refresh gives WIDTHxHEIGHT, and a
// zero size gives an empty string.
func westonMode(width, height, refreshMilliHertz uint32) string {
	if width == 0 || height == 0 {
		return ""
	}
	name := fmt.Sprintf("%dx%d", width, height)
	if refreshMilliHertz == 0 {
		return name
	}
	return fmt.Sprintf("%s@%d", name, westonRefresh(refreshMilliHertz))
}

func westonRefresh(refreshMilliHertz uint32) uint32 {
	return (refreshMilliHertz + 500) / 1000
}

// run dials the compositor again each time a connection ends, until
// ctx ends. When the socket file is created (arrivals.go), it dials
// at once instead of waiting out the delay. It does not log the end of
// a connection, because every restart that the operator makes ends
// one.
func (w *outputWatch) run(ctx context.Context) {
	watch := newArrivals(w.socketPath)
	defer watch.close()
	delay := w.retry
	arrived := false
	for {
		watch.ready()
		session := w.served().session
		_ = w.connection(ctx)
		w.connectionEnded()
		served := w.served().session != session
		delay = nextDialWait(served, arrived, delay, w.retry, w.retryLimit)
		arrived = watch.wait(ctx, delay)
		if ctx.Err() != nil {
			return
		}
	}
}

// connection dials the compositor and reads events until the
// connection ends or ctx is cancelled.
//
// The client sends a sync right after get_registry. The compositor
// sends the sync's done event after every global that already exists,
// so the outputs before the done event are the baseline. A compositor
// that has just started sized its canvases for those outputs, so the
// watch reports none of them as re-created.
func (w *outputWatch) connection(ctx context.Context) error {
	socket, err := net.DialTimeout("unix", w.socketPath, socketDialTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = socket.Close() }()

	// A blocked read does not watch ctx, so closing the socket ends it.
	ended := make(chan struct{})
	defer close(ended)
	go func() {
		select {
		case <-ctx.Done():
			_ = socket.Close()
		case <-ended:
		}
	}()

	w.connectionStarted()
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

	// Outputs added after baselineDone are changes. An output with no
	// connector name cannot be matched to the one it replaces, so a
	// removal and an addition of such outputs, in either order, count
	// as one re-creation.
	var baselineDone, sawRemoval, sawCreation bool
	objectOfGlobal := map[uint32]uint32{}
	globalOfObject := map[uint32]uint32{}
	// The current mode each output sent since its last done event.
	pendingMode := map[uint32]string{}
	identities := map[uint32]outputIdentity{}
	// Outputs added after the baseline that have not sent their first
	// done event. The watch reports such an output at that done event,
	// once it has the output's connector and mode.
	awaitingFirstBatch := map[uint32]bool{}
	// A re-creation of a named output, keyed by connector. Weston can
	// delay destroying an output until a pending page flip finishes,
	// so the new output can be added before the old one is removed.
	// removedPanels holds the panel of a removed output whose
	// replacement has not been added, and addedPanels the panel of a
	// new output whose predecessor has not been removed.
	removedPanels := map[string]panelIdentity{}
	addedPanels := map[string]panelIdentity{}
	reportUnnamed := func() {
		if sawRemoval && sawCreation {
			sawRemoval, sawCreation = false, false
			w.outputsChanged(true)
			return
		}
		w.outputsChanged(false)
	}
	reportReplacement := func(left, back panelIdentity) {
		w.outputsChanged(left != back)
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
			return fmt.Errorf("the compositor sent a protocol error for object %d with code %d: %s", object, code, message)
		case event.object == displayObject && event.opcode == displayDeleteIDEvent:
			// The client never reuses an id.
		case event.object == baseline && event.opcode == callbackDoneEvent:
			baselineDone = true
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
			bindVersion := min(version, outputVersion)
			words = waylandWords{}
			words.putUint(global)
			words.putText(outputInterface)
			words.putUint(bindVersion)
			words.putUint(id)
			if err := client.request(registry, registryBind, words); err != nil {
				return err
			}
			objectOfGlobal[global], globalOfObject[id] = id, global
			// An output bound below outputDoneVersion sends no done
			// event, so the watch reports it at the bind.
			if baselineDone && bindVersion >= outputDoneVersion {
				awaitingFirstBatch[id] = true
				continue
			}
			sawCreation = sawCreation || baselineDone
			reportUnnamed()
		case event.object == registry && event.opcode == registryGlobalRemoveEvent:
			global := event.fields.uint()
			if err := event.fields.err; err != nil {
				return err
			}
			id, ours := objectOfGlobal[global]
			if !ours {
				continue
			}
			delete(objectOfGlobal, global)
			delete(globalOfObject, id)
			delete(pendingMode, id)
			delete(awaitingFirstBatch, id)
			left := identities[id]
			delete(identities, id)
			w.removeOutput(global)
			switch {
			case !baselineDone:
				w.outputsChanged(false)
			case left.connector == "":
				sawRemoval = true
				reportUnnamed()
			default:
				if back, waiting := addedPanels[left.connector]; waiting {
					delete(addedPanels, left.connector)
					reportReplacement(left.panel, back)
					continue
				}
				removedPanels[left.connector] = left.panel
				w.outputsChanged(false)
			}
		default:
			global, ours := globalOfObject[event.object]
			if !ours {
				continue
			}
			switch event.opcode {
			case outputGeometryEvent:
				event.fields.skip(outputGeometryUnusedArgs)
				vendor := event.fields.text()
				model := event.fields.text()
				if err := event.fields.err; err != nil {
					return err
				}
				identity := identities[event.object]
				identity.panel.monitor = vendor + " " + model
				identities[event.object] = identity
			case outputNameEvent:
				connector := event.fields.text()
				if err := event.fields.err; err != nil {
					return err
				}
				w.recordConnector(global, connector)
				identity := identities[event.object]
				identity.connector = connector
				identities[event.object] = identity
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
				pendingMode[event.object] = westonMode(width, height, refresh)
			case outputDoneEvent:
				mode := pendingMode[event.object]
				w.recordMode(global, mode)
				delete(pendingMode, event.object)
				identity := identities[event.object]
				if mode != "" {
					identity.panel.mode = mode
					identities[event.object] = identity
				}
				if !awaitingFirstBatch[event.object] {
					continue
				}
				delete(awaitingFirstBatch, event.object)
				if identity.connector == "" {
					sawCreation = true
					reportUnnamed()
					continue
				}
				if left, waiting := removedPanels[identity.connector]; waiting {
					delete(removedPanels, identity.connector)
					reportReplacement(left, identity.panel)
					continue
				}
				addedPanels[identity.connector] = identity.panel
				w.outputsChanged(false)
			}
		}
	}
}
