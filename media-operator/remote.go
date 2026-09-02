package main

// The reader mode is the standing remote pod's whole process. It
// reads the controller its claim delivered, folds each event through
// the table the operator publishes on this Remote's keys topic, and
// publishes the kernel's name for the control on the events topic. It
// holds no API credentials.
//
// The standing pod outlives every sleep of the controller. The claim
// tolerates the disconnected taint with no limit, so a controller a
// person puts down keeps its allocation and the pod keeps running. The
// node the claim delivers survives a sleep, so a person's press reaches
// the bus with no pod restart. A node can still end, on a pod restart or
// an unprepare, so the reader runs an outer loop: wait for the nodes,
// publish until they vanish, then wait again. Only the kubelet's SIGTERM
// ends the process. The pod publishes no link state, because the
// Peripheral the bluetooth-operator writes is the record of the link.
//
// The pod also publishes what its controller declares, on the retained
// codes topic, at every node open, and the operator reports the codes
// the Keymap leaves unbound on the Remote's status. A Remote in
// discovery keeps every node the claim delivered and logs each event
// the way a Keymap names it, so a person maps unknown hardware from
// the pod log.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"
)

// The claim's CDI spec mounts the controller's event nodes at their
// kernel paths, and nothing tells this process which paths those are.
// So it globs the whole directory, and the capability test in evdev.go
// picks the node that carries the buttons. A variable so a test can
// point the glob at a directory of its own.
var inputNodePattern = "/dev/input/event*"

// nodePollDelay is how long the reader waits between two scans of
// /dev/input while no node is there to read. A pod that starts before its
// claim is prepared finds an empty directory, which is ordinary, so the
// reader scans again every two seconds until the nodes appear.
var nodePollDelay = 2 * time.Second

// reader holds the standing pod's bus client, the topics it publishes,
// and the declared codes it last published. The bus calls onConnect on its
// own goroutine and the node loop publishes on the pod's, so a mutex
// guards the document the two share.
type reader struct {
	bus               *Bus
	eventsTopic       string
	availabilityTopic string
	codesTopic        string

	// The retained topic the operator publishes this Remote's compiled
	// table on, and the fold state that reads it. The pod subscribes to
	// its own table alone.
	keysTopic string
	keys      keyState

	// The run's context, which every synthesised repeat runs under, so
	// no repeat outlives the process.
	repeatCtx context.Context

	// The teaching mode the Remote's spec asks for. Discovery keeps
	// every node the claim delivered and logs each event the way a
	// Keymap names it, so a person reads an unknown controller's codes
	// out of the pod log.
	discovery bool

	// Where the verdict and discovery lines go. It is a field so a test
	// reads what the pod would print.
	log io.Writer

	// The verdict lines the last scan logged. The reader scans every
	// two seconds while it waits for a node, so it logs only a changed
	// picture. Only the node loop touches this field.
	verdicts []string

	mutex sync.Mutex
	// The declared-codes document last published, so a reconnect
	// republishes it. A nil document is the cleared value.
	codes []byte
}

// runReader finds the controller, connects to the bus, and publishes
// each event to the Remote's events topic. Setup that cannot succeed
// ends the process, so a pod the operator built wrong shows the failure
// in kubectl instead of publishing to a malformed topic.
func runReader() {
	namespace := os.Getenv(remoteNamespaceVariable)
	name := os.Getenv(remoteNameVariable)
	busAddress := os.Getenv(busAddressVariable)
	if namespace == "" || name == "" || busAddress == "" {
		fmt.Fprintf(os.Stderr,
			"remote: %s, %s, and %s must all be set; the operator sets them on every standing remote pod\n",
			remoteNamespaceVariable, remoteNameVariable, busAddressVariable)
		os.Exit(1)
	}
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}

	// This process is its container's PID 1, and the kernel runs no
	// default action for a signal sent to PID 1, so the kubelet's
	// SIGTERM ends the pod's grace period unanswered unless it is
	// handled here.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	r := &reader{
		eventsTopic:       remoteEventsTopic(base, namespace, name),
		availabilityTopic: remoteAvailabilityTopic(base, namespace, name),
		codesTopic:        remoteCodesTopic(base, namespace, name),
		keysTopic:         remoteKeysTopic(base, namespace, name),
		// The operator sets the variable only where the Remote asks for
		// discovery, so a pod that reads nothing here runs the ordinary
		// selection rule.
		discovery: os.Getenv(remoteDiscoveryVariable) == discoveryOn,
		log:       os.Stdout,
		// The pod starts on the base, the same rows compiled into this
		// binary, so a controller behaves as an unmapped one from the first
		// press. The retained table arrives moments later and replaces it,
		// and a Keymap's rows take effect then.
		keys: keyState{table: baseKeys},
	}
	// The reader reads one topic off the bus, its own key table. It also
	// names a Last Will on its availability topic, the pattern the
	// playback sidecar uses: the broker publishes offline on any
	// disconnect this pod does not make cleanly, so a pod the kubelet
	// killed leaves no retained document that reads as a live one. The
	// Bus manages its own connection and reconnects with
	// backoff, so a broker restart costs a gap in events and not the
	// reader.
	r.bus = newBus(busAddress, "remote-"+namespace+"-"+name,
		&busWill{Topic: r.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		r.onConnect, r.handle)
	// The table topic is retained, so the current table arrives the
	// instant the pod connects, and the Bus re-sends the filter on every
	// reconnect. The subscription is made once.
	r.bus.Subscribe(r.keysTopic)
	go r.bus.Run(ctx)

	r.publishEvents(ctx)
	os.Exit(0)
}

// handle folds one inbound message. The pod subscribes to one topic,
// its own table, so the topic is the whole dispatch.
func (r *reader) handle(topic string, payload []byte) {
	if topic == r.keysTopic {
		r.setTable(payload)
	}
}

// onConnect refills the broker the moment a session reaches a CONNACK. The
// Bus remembers subscriptions across a reconnect but not publishes, and a
// fresh broker session holds none of the retained state this pod owns, so
// the availability and the declared codes both go out again here. A pod
// holding no nodes republishes the cleared value.
func (r *reader) onConnect(bus *Bus) {
	bus.Publish(r.availabilityTopic, []byte(availabilityOnline), true)
	r.mutex.Lock()
	codes := r.codes
	r.mutex.Unlock()
	bus.Publish(r.codesTopic, codes, true)
}

// publishCodes writes what the nodes declare to the retained codes
// topic and keeps it for a later reconnect. The operator subtracts the
// Keymap's bindings from it and reports the gap on the Remote's
// status.
func (r *reader) publishCodes(codes remoteCodes) {
	// A struct of two integer slices marshals unconditionally, so the
	// error is dropped.
	payload, _ := json.Marshal(codes)
	r.mutex.Lock()
	r.codes = payload
	r.mutex.Unlock()
	r.bus.Publish(r.codesTopic, payload, true)
}

// clearCodes empties the retained codes topic when the nodes vanish.
// The empty payload is the cleared retained value, the same clear the
// operator writes on the keys topic of a Remote that is gone.
func (r *reader) clearCodes() {
	r.mutex.Lock()
	r.codes = nil
	r.mutex.Unlock()
	r.bus.Publish(r.codesTopic, nil, true)
}

// publishEvents is the standing pod's outer loop. It waits for the
// controller's nodes, publishes every bindable event until the nodes
// vanish, then waits again. The nodes can end on an unprepare or when
// the claim's device changes, and the pod keeps running across that, so
// only ctx ending stops this loop. Each batch of nodes publishes the
// codes it declares and clears them when it ends.
func (r *reader) publishEvents(ctx context.Context) {
	for ctx.Err() == nil {
		nodes, err := r.awaitNodes(ctx)
		if err != nil {
			return
		}
		r.publishCodes(declaredCodes(nodes))
		r.readAndPublish(ctx, nodes)
		r.clearCodes()
	}
}

// awaitNodes polls for a node the mode keeps. The nodes appear when the
// claim is prepared, which can be minutes after this pod schedules, so an
// empty directory is ordinary and only ctx ends the wait.
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

// matchingNodes opens every event node and keeps the ones the mode
// keeps: the nodes that pass the capability test, or every node the
// claim delivered in discovery. The open is syscall.Open because the
// ioctls in inspectNode need the raw descriptor before any *os.File
// exists. A node that will not open or answer is skipped.
func (r *reader) matchingNodes() []openNode {
	paths, err := filepath.Glob(inputNodePattern)
	if err != nil {
		return nil
	}
	var nodes []openNode
	var verdicts []string
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
		// Discovery keeps every node the claim delivered, because the
		// point of discovery is to see what the rule rejects.
		keep := r.discovery || controllerBitmaps(node.keys, node.axes)
		verdicts = append(verdicts, node.verdict(keep).line())
		if !keep {
			syscall.Close(descriptor)
			continue
		}
		node.file = os.NewFile(uintptr(descriptor), path)
		nodes = append(nodes, node)
	}
	r.logVerdicts(verdicts)
	return nodes
}

// logVerdicts writes one line per node, and only when the picture
// changed, because the two-second scan of an empty directory would
// otherwise repeat the same report forever.
func (r *reader) logVerdicts(verdicts []string) {
	if slices.Equal(verdicts, r.verdicts) {
		return
	}
	r.verdicts = verdicts
	for _, line := range verdicts {
		fmt.Fprintf(r.log, "remote: %s\n", line)
	}
}

// declaredCodes is what the kept nodes declare, as the union over the
// batch. It is the set the operator subtracts a Keymap's bindings
// from.
func declaredCodes(nodes []openNode) remoteCodes {
	var codes remoteCodes
	for _, node := range nodes {
		codes.Keys = union(codes.Keys, declaredKeyCodes(node.keys))
		codes.Axes = union(codes.Axes, declaredHatAxes(node.axes))
	}
	return codes
}

// union folds one node's codes into the batch's, in code order and
// with no repeat, because two of a controller's nodes can declare one
// code.
func union(held, arriving []uint16) []uint16 {
	for _, code := range arriving {
		if index, found := slices.BinarySearch(held, code); !found {
			held = slices.Insert(held, index, code)
		}
	}
	return held
}

func closeNodes(nodes []openNode) {
	for _, node := range nodes {
		node.file.Close()
	}
}

// readAndPublish reads the controller's nodes and publishes each
// bindable event to the events topic. It returns when the nodes vanish, so
// the outer loop waits for the ones that come back. The process keeps
// running.
func (r *reader) readAndPublish(ctx context.Context, nodes []openNode) {
	defer closeNodes(nodes)
	// Every repeat this batch of nodes starts runs under the run's
	// context, and the nodes vanishing ends them all: a node that ends
	// mid-hold sends no release.
	r.repeatCtx = ctx
	defer r.stopAllRepeats()

	reading, stopReading := context.WithCancel(ctx)
	defer stopReading()
	// A reader blocked in read(2) ends only when its file closes, so the
	// context's end must close the nodes to unblock it.
	defer context.AfterFunc(reading, func() { closeNodes(nodes) })()

	events := readNodes(reading, nodes, stopReading)
	for {
		select {
		case <-reading.Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if !publishable(event.event) {
				continue
			}
			// Discovery logs each raw event, and the fold below is
			// unchanged, so a controller in discovery still drives its
			// unit and still wakes a faded screen.
			if r.discovery {
				for _, line := range discoveryLines(event.node, event.event) {
					fmt.Fprintf(r.log, "remote: %s\n", line)
				}
			}
			r.fold(event.event)
		}
	}
}

// publishable is the gate before the fold. Every EV_KEY event reaches
// it. Of EV_ABS only the two hat axes do, because the analog sticks
// report at 250Hz. EV_SYN and EV_MSC carry no control at all.
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

// readNodes starts one reader per matched node and merges their events
// into one channel. The first reader to end cancels the whole batch,
// because a vanished node means the claim's nodes changed and the outer
// loop must wait for the ones that come back.
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

// readNode delivers one node's events until the node ends. A read error
// is not retried and the node is not reopened, because the error means the
// node is gone; the outer loop finds the new ones when they appear.
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

// nodeEvent is one event and the node it came from, so a discovery
// line names the node beside the code. The label is built once per
// node, not once per event.
type nodeEvent struct {
	node  string
	event inputEvent
}
