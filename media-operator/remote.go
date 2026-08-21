package main

// The reader mode is the standing remote pod's whole process. It reads
// the controller its claim delivered and publishes each raw evdev
// event to the Remote's events topic. It holds no API credentials, and
// the keymap stays off the wire, so one Remote can feed two players
// that map it differently.
//
// The standing pod outlives every sleep of the controller. The claim
// tolerates the disconnected taint with no limit, so a controller a
// person puts down keeps its allocation and the pod keeps running. A
// sleeping controller closes its event nodes, and a waking controller
// opens them again, so the reader runs an outer loop: wait for the
// nodes, publish until they vanish, then wait again. Only the kubelet's
// SIGTERM ends the process. This differs from the old playback-pod
// reader, which exited on a sleep and let the kubelet restart it,
// because that reader held no standing claim to lose.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
// /dev/input while the controller is asleep. A sleeping controller has
// no nodes at all, so an empty directory is ordinary and the reader
// scans again every two seconds until the nodes appear.
var nodePollDelay = 2 * time.Second

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
	eventsTopic := remoteEventsTopic(base, namespace, name)

	// This process is its container's PID 1, and the kernel runs no
	// default action for a signal sent to PID 1, so the kubelet's
	// SIGTERM ends the pod's grace period unanswered unless it is
	// handled here.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// The reader only publishes: no Last Will, no connect callback, and
	// no inbound handler. The Bus manages its own connection and
	// reconnects with backoff, so a broker restart costs a gap in events
	// and not the reader.
	bus := newBus(busAddress, "remote-"+namespace+"-"+name, nil, nil, nil)
	go bus.Run(ctx)

	publishEvents(ctx, bus, eventsTopic)
	os.Exit(0)
}

// publishEvents is the standing pod's outer loop. It waits for the
// controller's nodes, publishes every bindable event until the nodes
// vanish, then waits again. The nodes vanish when the controller sleeps
// and return when it wakes, and the pod keeps running across both, so
// only ctx ending stops this loop.
func publishEvents(ctx context.Context, bus *Bus, topic string) {
	for ctx.Err() == nil {
		nodes, err := awaitNodes(ctx)
		if err != nil {
			return
		}
		readAndPublish(ctx, bus, topic, nodes)
	}
}

// awaitNodes polls for a node that carries a controller's buttons or
// hat axes. The nodes appear when the controller connects, which can be
// minutes after this pod schedules, so an empty directory is ordinary
// and only ctx ends the wait.
func awaitNodes(ctx context.Context) ([]*os.File, error) {
	for {
		nodes := matchingNodes()
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

// matchingNodes opens every event node and keeps the ones that pass the
// capability test. The open is syscall.Open because the ioctl in
// isController needs the raw descriptor before any *os.File exists. A
// node that will not open or answer is skipped silently: the directory
// holds every input device on the machine, and most of them are not
// this claim's to read.
func matchingNodes() []*os.File {
	paths, err := filepath.Glob(inputNodePattern)
	if err != nil {
		return nil
	}
	var nodes []*os.File
	for _, path := range paths {
		descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		if !isController(descriptor) {
			syscall.Close(descriptor)
			continue
		}
		nodes = append(nodes, os.NewFile(uintptr(descriptor), path))
	}
	return nodes
}

func closeNodes(nodes []*os.File) {
	for _, node := range nodes {
		node.Close()
	}
}

// readAndPublish reads the controller's nodes and publishes each
// bindable event to the events topic. It returns when the nodes vanish,
// which is the controller going to sleep, so the outer loop waits for
// the next connect. The process keeps running.
func readAndPublish(ctx context.Context, bus *Bus, topic string, nodes []*os.File) {
	defer closeNodes(nodes)

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
			if !publishable(event) {
				continue
			}
			// A slice of a type whose fields are integers marshals
			// unconditionally, so the error is dropped.
			payload, _ := json.Marshal(remoteEvent{Type: event.Type, Code: event.Code, Value: event.Value})
			// The events are not retained, because a press is an event
			// and not a state, so a subscriber that joins later reads no
			// stale press.
			bus.Publish(topic, payload, false)
		}
	}
}

// publishable keeps the topic to the events a Keymap can bind. A button
// arrives as EV_KEY and every EV_KEY code is bindable, so every key
// event goes out. A d-pad arrives as an EV_ABS hat axis, and only the
// two hat axes in axisCodes are bindable, so the analog sticks, which
// report at 250Hz, stay off the wire. EV_SYN and EV_MSC carry no
// binding and never publish.
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
// because a vanished node means the controller slept and the outer loop
// must wait for the nodes the next connect brings.
func readNodes(ctx context.Context, nodes []*os.File, ended context.CancelFunc) <-chan inputEvent {
	events := make(chan inputEvent, 64)
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
// is not retried and the node is not reopened, because the error means
// the controller disconnected and its nodes are gone; the outer loop
// finds the new ones on the next connect.
func readNode(ctx context.Context, node *os.File, events chan<- inputEvent) {
	buffer := make([]byte, inputEventSize*64)
	for {
		read, err := node.Read(buffer)
		if err != nil {
			return
		}
		for _, event := range parseInputEvents(buffer[:read]) {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}
