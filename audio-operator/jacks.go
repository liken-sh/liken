package main

// The event source: the card's jack input nodes.
//
// ALSA registers an input device for each jack it can sense, and an
// HDA controller with HDMI outputs has one for each display pin. The
// raw claim delivers those nodes with the card, because a jack
// reports the state of an output the same claim plays through. A
// monitor that arrives or leaves is a switch event on one of them, so
// the operator reads them instead of asking the card again on a
// timer.
//
// The event says only that something changed. Every pass re-reads the
// ELD elements and PipeWire's graph, because a mirror built from
// event payloads drifts out of step with the hardware and a re-read
// cannot.

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// inputDir holds the evdev nodes. Every node the operator can see
// there belongs to the claimed card, because CDI delivers the claim's
// nodes and no others. It is a variable so the tests can point it at
// a directory they control.
var inputDir = "/dev/input"

// jackRescanInterval is how often the watcher looks for nodes it has
// not opened yet. A card registers its jacks when the driver binds,
// which is before this pod starts, so the rescan is what covers a
// node that appears later.
const jackRescanInterval = 60 * time.Second

// inputEventSize is the size of one struct input_event on a 64-bit
// kernel: two 8-byte time fields, the type, the code, and the value.
const inputEventSize = 24

// evSwitch is the evdev event type that carries a jack's state. Every
// other type on these nodes, including the synchronization events
// that end each packet, says nothing about a monitor.
const evSwitch = 0x05

// The switch codes an audio jack reports. ALSA maps a video output
// jack, which is what an HDMI or DisplayPort pin registers, to
// SW_VIDEOOUT_INSERT, and the analog jacks to the headphone and line
// codes.
var switchNames = map[uint16]string{
	0x02: "SW_HEADPHONE_INSERT",
	0x06: "SW_LINEOUT_INSERT",
	0x07: "SW_JACK_PHYSICAL_INSERT",
	0x08: "SW_VIDEOOUT_INSERT",
}

// jackEvent is one switch change on one node.
type jackEvent struct {
	Node  string
	Code  uint16
	Value int32
}

// String names the event the way the kernel's own headers do.
func (e jackEvent) String() string {
	name, known := switchNames[e.Code]
	if !known {
		name = fmt.Sprintf("switch %d", e.Code)
	}
	return fmt.Sprintf("%s: %s = %d", filepath.Base(e.Node), name, e.Value)
}

// watchJacks opens every jack node and reports the switch events they
// carry. The channel closes when the context ends.
func watchJacks(ctx context.Context) (<-chan jackEvent, error) {
	events := make(chan jackEvent, 16)
	watcher := &jackWatcher{events: events}
	if err := watcher.scan(ctx); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// A card whose claim delivered no input node has no jack the
		// kernel can sense, which is an older codec or a card with the
		// jack detection turned off. That is a card this operator still
		// publishes, so it is not a failure to start over: the backstop
		// tick becomes the only wake, and a monitor takes up to one tick
		// to appear in the slice.
		fmt.Fprintf(os.Stderr, "%s does not exist, so no jack event will arrive; the backstop tick every %s is the only wake\n",
			inputDir, backstopInterval)
		go func() {
			<-ctx.Done()
			close(events)
		}()
		return events, nil
	}
	go func() {
		tick := time.NewTicker(jackRescanInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				watcher.close()
				watcher.wait()
				close(events)
				return
			case <-tick.C:
				if err := watcher.scan(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "looking for new jack nodes: %v\n", err)
				}
			}
		}
	}()
	return events, nil
}

// jackWatcher holds the open nodes, so that the rescan opens each one
// once and the shutdown closes all of them.
type jackWatcher struct {
	events chan<- jackEvent

	mutex   sync.Mutex
	open    map[string]*os.File
	readers sync.WaitGroup
}

// scan opens every jack node that is not open yet.
func (w *jackWatcher) scan(ctx context.Context) error {
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "event") {
			continue
		}
		path := filepath.Join(inputDir, entry.Name())

		w.mutex.Lock()
		_, already := w.open[path]
		if already {
			w.mutex.Unlock()
			continue
		}
		node, err := openEventNode(path)
		if err != nil {
			w.mutex.Unlock()
			fmt.Fprintf(os.Stderr, "opening %s: %v\n", path, err)
			continue
		}
		if w.open == nil {
			w.open = map[string]*os.File{}
		}
		w.open[path] = node
		w.mutex.Unlock()

		w.readers.Add(1)
		go func() {
			defer w.readers.Done()
			w.read(ctx, node)
		}()
	}
	return nil
}

// read carries one node's switch events onto the channel. It returns
// when the node is closed, which is how the shutdown ends it.
func (w *jackWatcher) read(ctx context.Context, node *os.File) {
	buffer := make([]byte, inputEventSize)
	for {
		if _, err := node.Read(buffer); err != nil {
			if ctx.Err() == nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "reading %s: %v\n", node.Name(), err)
			}
			return
		}
		kind := binary.NativeEndian.Uint16(buffer[16:18])
		if kind != evSwitch {
			continue
		}
		event := jackEvent{
			Node:  node.Name(),
			Code:  binary.NativeEndian.Uint16(buffer[18:20]),
			Value: int32(binary.NativeEndian.Uint32(buffer[20:24])),
		}
		select {
		case w.events <- event:
		case <-ctx.Done():
			return
		}
	}
}

func (w *jackWatcher) close() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	for _, node := range w.open {
		_ = node.Close()
	}
	w.open = nil
}

func (w *jackWatcher) wait() { w.readers.Wait() }

// openEventNode opens one evdev node so that a close can end a read
// that is waiting for an event. The descriptor is non-blocking, which
// is what makes the Go runtime poll it instead of blocking a thread
// in the kernel, and a poll is what a Close interrupts.
func openEventNode(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(descriptor), path), nil
}
