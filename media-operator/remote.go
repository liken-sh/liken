package main

// In reader mode the binary is the whole process of a Remote's pod. It
// reads the controller's input event nodes, translates each event with
// the key table that the operator publishes on the Remote's keys topic,
// and publishes the key on the Remote's events topic. The pod has no
// Kubernetes API credentials.
//
// The pod's claim tolerates the bluetooth.liken.sh/disconnected taint
// with no time limit, so the pod keeps running while the controller
// sleeps, and the first press after it wakes reaches the bus without a
// pod restart. A node can still stop returning events, so the reader
// loops: it waits for nodes, publishes their events until one closes,
// and waits again. The pod publishes no link state, because the
// `Peripheral` that bluetooth-operator writes records the link.
//
// Each time it opens nodes, the reader publishes the key and axis codes
// that the controller declares, retained, on the codes topic. The
// operator removes the codes that the Keymap binds and reports the rest
// in the Remote's status. In discovery mode the reader keeps every node
// and logs each event with the names a Keymap uses, so a person can map
// an unknown controller from the pod log.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// inputNodePattern matches every event node in the container. The
// claim's CDI spec mounts the controller's nodes at their kernel paths,
// and the pod does not receive the paths, so matchingNodes opens every
// node and keeps the ones that declare bindable codes.
var inputNodePattern = "/dev/input/event*"

// nodePollDelay is the time between scans of /dev/input while no node
// can be read. An inotify watch would report nothing, because the
// container runtime creates the claim's device nodes when the container
// starts, and the files do not change after that. What changes is the
// kernel device behind a node. When bluetooth-operator restarts, the
// virtual input devices behind the nodes go away, and a node returns
// ENODEV until the operator creates a device with its number again. A
// uevent socket could report that, but a scan is one glob and a few
// opens, and it runs only while no node can be read.
var nodePollDelay = 2 * time.Second

type reader struct {
	bus               *Bus
	eventsTopic       string
	availabilityTopic string
	codesTopic        string

	// keysTopic is the retained topic where the operator publishes this
	// Remote's compiled key table. keys holds that table, the keys that
	// are pressed now, and their running repeats.
	keysTopic string
	keys      keyState

	// repeatCtx is the context that the reader's generated key repeats
	// run under, so no repeat outlives the process.
	repeatCtx context.Context

	discovery bool

	// log receives the scan and discovery lines. A test replaces it to
	// read them.
	log io.Writer

	// scanLines are the lines the last scan logged. Only the node loop
	// uses this field, so it needs no lock.
	scanLines []string

	// mutex guards codes, because the bus calls onConnect on its own
	// goroutine while the node loop publishes on another.
	mutex sync.Mutex
	// codes is the last codes document published, so that onConnect
	// can publish it again. Nil clears the retained message.
	codes []byte
}

// runReader exits with an error when a required environment variable is
// missing, so a pod built wrong fails where kubectl shows it, and does
// not publish to a malformed topic.
func runReader() {
	namespace := os.Getenv(remoteNamespaceVariable)
	name := os.Getenv(remoteNameVariable)
	busAddress := os.Getenv(busAddressVariable)
	if namespace == "" || name == "" || busAddress == "" {
		fmt.Fprintf(os.Stderr,
			"remote: %s, %s, and %s must all be set. The operator sets them in every Remote pod\n",
			remoteNamespaceVariable, remoteNameVariable, busAddressVariable)
		os.Exit(1)
	}
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}

	// This process is PID 1 of its container, and the kernel ignores a
	// signal to PID 1 that has no handler. Without this handler the
	// kubelet's SIGTERM does nothing, and the pod runs until its grace
	// period ends.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	r := &reader{
		eventsTopic:       remoteEventsTopic(base, namespace, name),
		availabilityTopic: remoteAvailabilityTopic(base, namespace, name),
		codesTopic:        remoteCodesTopic(base, namespace, name),
		keysTopic:         remoteKeysTopic(base, namespace, name),
		// The operator sets this variable only for a Remote in
		// discovery mode.
		discovery: os.Getenv(remoteDiscoveryVariable) == discoveryOn,
		log:       os.Stdout,
		// The base table compiled into this binary makes the
		// controller work before the operator's retained table, which
		// includes the Keymap's rows, arrives and replaces it.
		keys: keyState{table: baseKeys},
	}
	// When the pod's session ends without a clean disconnect, the
	// broker publishes the Last Will, a retained offline message on the
	// availability topic, so a killed pod does not stay online in the
	// retained message. The Bus reconnects with backoff, so a broker
	// restart loses events while the broker is down but does not stop
	// the reader.
	r.bus = newBus(busAddress, "remote-"+namespace+"-"+name,
		&busWill{Topic: r.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		r.onConnect, r.handle)
	// The keys topic is retained, so the current table arrives as soon
	// as the pod connects. The Bus subscribes again on each reconnect,
	// so this call is needed once.
	r.bus.Subscribe(r.keysTopic)
	go r.bus.Run(ctx)

	r.publishEvents(ctx)
	os.Exit(0)
}

// handle receives a message from the bus. The pod subscribes only to
// its keys topic, so every message is a new key table.
func (r *reader) handle(topic string, payload []byte) {
	if topic == r.keysTopic {
		r.setTable(payload)
	}
}

// onConnect runs on each connect, because a restarted broker can have
// lost the retained messages, and the Bus does not publish them again.
// While no nodes are open, codes is nil, and the publish clears the
// retained codes.
func (r *reader) onConnect(bus *Bus) {
	bus.Publish(r.availabilityTopic, []byte(availabilityOnline), true)
	r.mutex.Lock()
	codes := r.codes
	r.mutex.Unlock()
	bus.Publish(r.codesTopic, codes, true)
}

// publishCodes publishes the declared codes, retained, and keeps them
// for onConnect to publish again.
func (r *reader) publishCodes(codes remoteCodes) {
	// Marshaling two slices of integers cannot fail.
	payload, _ := json.Marshal(codes)
	r.mutex.Lock()
	r.codes = payload
	r.mutex.Unlock()
	r.bus.Publish(r.codesTopic, payload, true)
}

// clearCodes publishes an empty retained payload, which deletes the
// broker's retained message on the codes topic.
func (r *reader) clearCodes() {
	r.mutex.Lock()
	r.codes = nil
	r.mutex.Unlock()
	r.bus.Publish(r.codesTopic, nil, true)
}

// publishEvents waits for the controller's nodes, publishes their
// events until a node closes, and then waits again. Only ctx ends the
// loop, because the pod keeps running while it has no nodes.
func (r *reader) publishEvents(ctx context.Context) {
	for ctx.Err() == nil {
		nodes, err := r.awaitNodes(ctx)
		if err != nil {
			return
		}
		r.serve(ctx, nodes)
	}
}

// serve publishes the codes the nodes declare, publishes their events
// until a node closes, and then clears the codes. It logs a line when
// the controller connects and when it leaves, because a person caused
// both.
func (r *reader) serve(ctx context.Context, nodes []openNode) {
	codes := declaredCodes(nodes)
	logLine(r.log, "remote: controller connected on %s: %s, %s",
		nodeLabels(nodes), countOf(len(codes.Keys), "key code", "key codes"),
		countOf(len(codes.Axes), "hat axis", "hat axes"))
	r.publishCodes(codes)
	r.readAndPublish(ctx, nodes)
	r.clearCodes()
	// A cancelled ctx means the pod is stopping, not that the
	// controller left.
	if ctx.Err() == nil {
		logLine(r.log, "remote: controller disconnected from %s: its input nodes closed", nodeLabels(nodes))
	}
}

func nodeLabels(nodes []openNode) string {
	labels := make([]string, len(nodes))
	for index, node := range nodes {
		labels[index] = node.label()
	}
	return strings.Join(labels, ", ")
}

// awaitNodes scans every nodePollDelay until matchingNodes returns a
// node or ctx ends. nodePollDelay explains why it polls.
func (r *reader) awaitNodes(ctx context.Context) ([]openNode, error) {
	for {
		nodes := r.matchingNodes()
		if len(nodes) > 0 {
			return nodes, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(nodePollDelay):
		}
	}
}

// matchingNodes opens every event node and returns the ones that
// declare a key code or a hat axis, or every node in discovery mode, so
// that a person can see the nodes the test rejects. It uses
// syscall.Open because the ioctls in inspectNode need a raw descriptor
// before an *os.File exists. It skips a node that does not open or does
// not respond to the ioctls.
func (r *reader) matchingNodes() []openNode {
	paths, err := filepath.Glob(inputNodePattern)
	if err != nil {
		return nil
	}
	var nodes []openNode
	var lines []string
	for _, path := range paths {
		descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		node, answered := inspectNode(descriptor, path)
		if !answered {
			syscall.Close(descriptor)
			continue
		}
		keep := r.discovery || declaresKeyOrHat(node.keys, node.axes)
		lines = append(lines, node.verdict(keep).line())
		if !keep {
			syscall.Close(descriptor)
			continue
		}
		if err := r.restrictNode(descriptor); err != nil {
			fmt.Fprintf(r.log, "remote: the kernel refused the event mask of %s, so the pod reads every event type the node reports: %v\n",
				node.label(), err)
		}
		node.file = os.NewFile(uintptr(descriptor), path)
		nodes = append(nodes, node)
	}
	r.logScan(lines)
	return nodes
}

// restrictNode limits the node to the event types the pod publishes. In
// discovery mode it limits nothing, because discovery shows a person
// every code the device emits.
func (r *reader) restrictNode(descriptor int) error {
	if r.discovery {
		return nil
	}
	return restrictToPublishable(descriptor)
}

// logScan logs the lines of a scan only when they differ from the last
// scan's, because awaitNodes scans every nodePollDelay while it waits.
func (r *reader) logScan(lines []string) {
	if slices.Equal(lines, r.scanLines) {
		return
	}
	r.scanLines = lines
	for _, line := range lines {
		fmt.Fprintf(r.log, "remote: %s\n", line)
	}
}

// declaredCodes returns the codes that all the nodes declare together.
func declaredCodes(nodes []openNode) remoteCodes {
	var codes remoteCodes
	for _, node := range nodes {
		codes.Keys = union(codes.Keys, declaredKeyCodes(node.keys))
		codes.Axes = union(codes.Axes, declaredHatAxes(node.axes))
	}
	return codes
}

// union inserts each code of added into the sorted slice codes, unless
// it is already there, because two nodes of one controller can declare
// the same code.
func union(codes, added []uint16) []uint16 {
	for _, code := range added {
		if index, found := slices.BinarySearch(codes, code); !found {
			codes = slices.Insert(codes, index, code)
		}
	}
	return codes
}

func closeNodes(nodes []openNode) {
	for _, node := range nodes {
		node.file.Close()
	}
}

// readAndPublish publishes each bindable event of the nodes until one
// node closes or ctx ends, and then closes every node.
func (r *reader) readAndPublish(ctx context.Context, nodes []openNode) {
	defer closeNodes(nodes)
	r.repeatCtx = ctx
	// A node that closes while a key is pressed sends no release, so
	// stop every repeat on return.
	defer r.stopAllRepeats()

	reading, stopReading := context.WithCancel(ctx)
	defer stopReading()
	// A blocked read(2) returns only when its file closes, so
	// cancelling reading closes the nodes.
	defer context.AfterFunc(reading, func() { closeNodes(nodes) })()

	// Range over the channel until readNodes closes it, even after
	// reading is cancelled. The node that ends cancels reading, and its
	// last events can still be in the channel. A select on the
	// cancellation could take it first and drop a press or release that
	// was already read. The cancellation closes every node, so every
	// node's goroutine returns and the channel closes.
	for event := range readNodes(reading, nodes, stopReading) {
		if !publishable(event.event) {
			continue
		}
		// Discovery mode logs each event and still translates and
		// publishes it, so the controller works while a person maps it.
		if r.discovery {
			for _, line := range discoveryLines(event.node, event.event) {
				fmt.Fprintf(r.log, "remote: %s\n", line)
			}
		}
		if line := r.fold(event.event); line != "" {
			logLine(r.log, "remote: %s: %s", event.node, line)
		}
	}
}

// publishable reports whether a Keymap can bind the event: an EV_KEY
// event, or an EV_ABS event of a hat axis in axisCodes. The masks in
// evdev.go apply the same rule in the kernel.
func publishable(event inputEvent) bool {
	switch event.Type {
	case evKey:
		return true
	case evAbs:
		for _, code := range axisCodes {
			if code == event.Code {
				return true
			}
		}
	}
	return false
}

// readNodes reads every node in its own goroutine and merges the events
// into one channel, which it closes when every goroutine has returned.
// The first goroutine to return calls ended, which stops the others,
// because a closed node means the controller's nodes changed and
// publishEvents must scan again.
func readNodes(ctx context.Context, nodes []openNode, ended context.CancelFunc) <-chan nodeEvent {
	events := make(chan nodeEvent, 64)
	var reading sync.WaitGroup
	for _, node := range nodes {
		reading.Add(1)
		go func() {
			defer reading.Done()
			defer ended()
			readNode(ctx, node, events)
		}()
	}
	go func() {
		reading.Wait()
		close(events)
	}()
	return events
}

// readNode sends the node's events to the channel until a read fails or
// ctx ends. It does not retry, because a read error means the node is
// gone.
func readNode(ctx context.Context, node openNode, events chan<- nodeEvent) {
	label := node.label()
	buffer := make([]byte, inputEventSize*64)
	for {
		read, err := node.file.Read(buffer)
		if err != nil {
			return
		}
		for _, event := range parseInputEvents(buffer[:read]) {
			select {
			case events <- nodeEvent{node: label, event: event}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// nodeEvent is an input event with the label of the node it came from.
type nodeEvent struct {
	node  string
	event inputEvent
}
