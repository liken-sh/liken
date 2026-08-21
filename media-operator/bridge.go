package main

// The bridge mode is the playback pod's one bus client, a native
// sidecar beside mpv. It holds no API credentials and reaches mpv
// through the JSON IPC socket on the emptyDir the two containers share.
// It does two jobs across that socket. It subscribes to each bound
// remote's events topic, applies that remote's compiled keymap, and
// writes the named action's command to the socket. And it reads mpv's
// property changes off the same socket and publishes the report to the
// Play's status topic, so the operator reads a running Play's place off
// the bus.
//
// The bridge is the only path the playback pod has to the control
// plane, and it reaches it through the operator's subscription and no
// other way. The report carries no API object: the Play it belongs to
// is named by the topic, not by the body.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// reportInterval is the ceiling on how often the bridge publishes a
// position while it advances. mpv sends a time-pos event several times a
// second, and one report is a small QoS 0 message, so the bus carries a
// live position at one report a second. The Play resource does not move
// that fast: the operator wakes its reconcile loop only on a pause or an
// item change, and a bare position advance rides the backstop tick. So
// the bus is the live plane and the resource is the throttled one, and a
// consumer that wants a smooth position reads the bus.
var reportInterval = 1 * time.Second

// busFlushGrace is how long the bridge holds the bus open after it
// publishes the two closing messages, so the writer goroutine drains
// them before the process exits. The messages are QoS 0 and carry no
// ack, so this window is the only signal the bridge has that they left.
// The empty status is the one that needs the window: the Last Will
// publishes offline on an unclean exit, but nothing except this publish
// clears the retained report a finished Play would otherwise leave.
var busFlushGrace = 500 * time.Millisecond

// bridge holds the two sides of the pod's bus client: the connection to
// mpv that both the reporter and the input handler write, and the
// last-known report the connect callback re-publishes on a reconnect.
// Two goroutines write mpv's socket, so mpvMutex serializes them: a
// command and an observe request must not interleave their bytes. One
// goroutine reads the last report and another writes it, so reportMutex
// covers that pair.
type bridge struct {
	statusTopic       string
	availabilityTopic string
	remotes           []remoteBindings

	bus *Bus

	mpvMutex sync.Mutex
	mpv      net.Conn

	reportMutex sync.Mutex
	lastReport  []byte
	haveReport  bool

	// repeats holds one cancel per held control that repeats, keyed by
	// its evdev code, so the release stops the repeat the press started.
	// repeatCtx is the run's context, so every repeat ends when the
	// bridge does. Only the bus reader goroutine mutates the map, but a
	// repeat goroutine reads through its own cancel, so repeatMu covers
	// it.
	repeatCtx context.Context
	repeatMu  sync.Mutex
	repeats   map[uint16]context.CancelFunc
}

// maxRepeatWindow caps one synthesized repeat. A controller that sleeps
// mid-hold publishes no release, so without this cap the repeat would
// fire until the film ended. A person does not hold a control this long,
// so the cap ends a repeat a lost release left running.
var maxRepeatWindow = 30 * time.Second

// runBridge connects to the bus, drives mpv's IPC socket, and reports
// the run. It returns when mpv's socket closes or the kubelet's grace
// signal arrives, and exits zero: the bridge is a sidecar, and mpv's
// own exit code is the pod's outcome.
func runBridge() {
	namespace := os.Getenv(playNamespaceVariable)
	name := os.Getenv(playNameVariable)
	busAddress := os.Getenv(busAddressVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}

	var remotes []remoteBindings
	if encoded := os.Getenv(remotesVariable); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &remotes); err != nil {
			fmt.Fprintf(os.Stderr, "bridge: %s: %v\n", remotesVariable, err)
			os.Exit(1)
		}
	}

	// The kernel runs no default action for a signal sent to PID 1, and
	// the bridge is its container's PID 1. The signal context ends the
	// report side on the kubelet's SIGTERM, which is the same end mpv's
	// closed socket gives. It is built before the bus, so a repeat that a
	// press starts the moment the bus connects has a context to end on.
	runCtx, stopRun := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopRun()

	br := &bridge{
		statusTopic:       playStatusTopic(base, namespace, name),
		availabilityTopic: playAvailabilityTopic(base, namespace, name),
		remotes:           remotes,
		repeatCtx:         runCtx,
		repeats:           map[uint16]context.CancelFunc{},
	}

	// The bus runs on its own context, not the signal context, so the
	// two closing publishes still have a live connection to leave on
	// after the grace signal ends the report side.
	busCtx, stopBus := context.WithCancel(context.Background())
	br.bus = newBus(busAddress, "play-"+namespace+"-"+name,
		&busWill{Topic: br.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		br.onConnect, br.handle)
	// The subscriptions are made once. The Bus remembers each filter and
	// re-sends it on every reconnect, so a broker restart does not need
	// the bridge to subscribe again.
	for _, remote := range remotes {
		br.bus.Subscribe(remote.EventsTopic)
	}
	go br.bus.Run(busCtx)

	br.report(runCtx)

	// The run is over: mpv ended or the kubelet is terminating the pod.
	// Clear the retained status so a finished Play leaves no report that
	// reads as still playing, mark the availability offline, and hold
	// the bus open long enough to send both before the connection ends.
	br.bus.Publish(br.statusTopic, nil, true)
	br.bus.Publish(br.availabilityTopic, []byte(availabilityOffline), true)
	time.Sleep(busFlushGrace)
	stopBus()
}

// onConnect refills the broker the moment a session reaches a CONNACK.
// It publishes online, and re-publishes the last-known report, because
// the broker drops its retained set on a restart and a reconnect must
// leave the current status behind again.
func (br *bridge) onConnect(bus *Bus) {
	bus.Publish(br.availabilityTopic, []byte(availabilityOnline), true)
	br.reportMutex.Lock()
	payload, have := br.lastReport, br.haveReport
	br.reportMutex.Unlock()
	if have {
		bus.Publish(br.statusTopic, payload, true)
	}
}

// handle turns one remote's button event into an mpv command. An
// inbound message names a remote by its events topic, so the bridge
// matches the topic to a bound remote, decodes the event, and matches
// it against that remote's compiled table. A press that no binding
// names, or an action this build has no command for, writes nothing.
//
// A release stops a repeat the press started. The reader publishes value
// 0 when a button lifts or a hat re-centers, and a binding matches only
// the press, so a release reaches mpv as nothing but this stop.
func (br *bridge) handle(topic string, payload []byte) {
	entry, ok := br.remoteFor(topic)
	if !ok {
		return
	}
	var event remoteEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	if event.Value == 0 {
		br.stopRepeat(event.Code)
		return
	}
	binding, ok := matchBinding(entry.Bindings, inputEvent{Type: event.Type, Code: event.Code, Value: event.Value})
	if !ok {
		return
	}
	command := commandFor(binding)
	if command == nil {
		return
	}
	br.command(command)
	// A binding that repeats fires again while the control is held. The
	// press fired once above, so the repeat carries the same command until
	// the release stops it.
	if binding.RepeatInterval > 0 {
		br.startRepeat(event.Code, command, binding.RepeatDelay, binding.RepeatInterval)
	}
}

// startRepeat runs one held control's repeat. A press of the same code
// while a repeat runs replaces it, because a second press means the first
// release was missed or the direction changed, and one control drives one
// repeat.
func (br *bridge) startRepeat(code uint16, command []any, delayMillis, intervalMillis int) {
	ctx, cancel := context.WithCancel(br.repeatCtx)
	br.repeatMu.Lock()
	if previous, ok := br.repeats[code]; ok {
		previous()
	}
	br.repeats[code] = cancel
	br.repeatMu.Unlock()
	go br.repeatLoop(ctx, command,
		time.Duration(delayMillis)*time.Millisecond,
		time.Duration(intervalMillis)*time.Millisecond)
}

// stopRepeat ends the repeat a release names. A release for a code with
// no repeat is the ordinary case of a control that does not repeat, and
// it does nothing.
func (br *bridge) stopRepeat(code uint16) {
	br.repeatMu.Lock()
	if cancel, ok := br.repeats[code]; ok {
		cancel()
		delete(br.repeats, code)
	}
	br.repeatMu.Unlock()
}

// repeatLoop re-fires one held control's command. The press already fired
// once, so the loop waits the delay, which is what separates a tap from a
// hold, then fires every interval. It ends on the release, on the bridge
// shutting down, or on the safety window. It leaves the map alone: a
// stopRepeat or a replacing startRepeat clears the entry, and a window
// that ends first leaves a cancelled entry the next press or release
// clears.
func (br *bridge) repeatLoop(ctx context.Context, command []any, delay, interval time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	window := time.NewTimer(maxRepeatWindow)
	defer window.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-window.C:
			return
		case <-ticker.C:
			br.command(command)
		}
	}
}

// remoteFor finds the bound remote a message belongs to by its events
// topic. One player may name several remotes, so the bridge holds zero
// or more, and the topic is what tells their events apart.
func (br *bridge) remoteFor(topic string) (remoteBindings, bool) {
	for _, entry := range br.remotes {
		if entry.EventsTopic == topic {
			return entry, true
		}
	}
	return remoteBindings{}, false
}

// report is the reporting side of the run. It dials mpv, observes the
// four properties the status is made of, and turns each change into a
// report on the bus. It ends when mpv's socket closes, which is how mpv
// says the run is over, or when the context ends on the grace signal.
func (br *bridge) report(ctx context.Context) {
	conn, err := dialMPV(ctx, mpvSocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bridge: no reports for this run: %v\n", err)
		return
	}
	defer conn.Close()
	// The read blocks until mpv writes, which can be minutes apart in a
	// paused film, so closing the socket is what ends the read when the
	// context ends. A running mpv ends the read by closing its own end.
	defer context.AfterFunc(ctx, func() { conn.Close() })()

	// observeProperties writes the socket, and so does the input
	// handler, so the observe runs under the same mutex the handler
	// takes. The connection is set first, so a button that arrives
	// mid-observe waits on the mutex rather than reaching a nil socket.
	if err := br.attach(conn); err != nil {
		fmt.Fprintf(os.Stderr, "bridge: no reports for this run: %v\n", err)
		return
	}
	defer br.detach()

	changes := make(chan propertyChange, 16)
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		defer close(changes)
		if err := readEvents(ctx, conn, changes); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "bridge: mpv's socket: %v\n", err)
		}
	}()

	runReporter(ctx, changes, br.send)
	<-reading
}

// attach sets the mpv connection and observes the properties under one
// lock, so the first writes to the socket cannot interleave with a
// command the input handler sends the moment the connection is live.
func (br *bridge) attach(conn net.Conn) error {
	br.mpvMutex.Lock()
	defer br.mpvMutex.Unlock()
	br.mpv = conn
	return observeProperties(conn, observedProperties)
}

// detach forgets the connection when the run ends, so a late command
// writes nothing rather than a closed socket.
func (br *bridge) detach() {
	br.mpvMutex.Lock()
	br.mpv = nil
	br.mpvMutex.Unlock()
}

// command writes one mpv command under the mutex. A command that
// arrives before mpv is dialed, or after the run ends, finds a nil
// connection and writes nothing.
func (br *bridge) command(command []any) {
	br.mpvMutex.Lock()
	defer br.mpvMutex.Unlock()
	if br.mpv == nil {
		return
	}
	if err := sendCommand(br.mpv, command); err != nil {
		fmt.Fprintf(os.Stderr, "bridge: mpv command: %v\n", err)
	}
}

// send publishes one report to the status topic, retained, and holds it
// as the last-known report. A restarted operator reads the retained
// report back from the broker, and a reconnect re-publishes the held
// report through onConnect, so neither loses a running Play's place.
func (br *bridge) send(report playReport) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	br.reportMutex.Lock()
	br.lastReport = payload
	br.haveReport = true
	br.reportMutex.Unlock()
	br.bus.Publish(br.statusTopic, payload, true)
	return nil
}

// playbackState is the run at one moment, as the bridge holds it. The
// fields are the report's fields, held between events, because each
// event carries one property and a report carries all of them.
type playbackState struct {
	paused   bool
	item     int
	position string
	duration string
}

// reportable holds reports back until mpv has said which item plays.
// mpv's playlist-pos counts from zero and reads -1 before anything
// loads; the API counts from one, the way a person counts tracks, so an
// item below one describes no playback at all.
func (s playbackState) reportable() bool {
	return s.item >= 1
}

func (s playbackState) report() playReport {
	return playReport{
		Paused:   s.paused,
		Item:     s.item,
		Position: s.position,
		Duration: s.duration,
	}
}

// apply folds one property change into the state, and its return value
// says whether the change earns a report at once. A pause and an item
// change are the two things a person watching kubectl is waiting to
// see, and both are rare. A position that advances is neither rare nor
// surprising, so it waits for the interval.
func (s *playbackState) apply(change propertyChange) bool {
	if !change.known() {
		return false
	}
	switch change.Name {
	case "pause":
		var paused bool
		if err := json.Unmarshal(change.Data, &paused); err != nil {
			return false
		}
		changed := paused != s.paused
		s.paused = paused
		return changed
	case "playlist-pos":
		var position int
		if err := json.Unmarshal(change.Data, &position); err != nil || position < 0 {
			return false
		}
		item := position + 1
		changed := item != s.item
		s.item = item
		return changed
	case "time-pos":
		var seconds float64
		if err := json.Unmarshal(change.Data, &seconds); err != nil {
			return false
		}
		s.position = formatPosition(seconds)
		return false
	case "duration":
		var seconds float64
		if err := json.Unmarshal(change.Data, &seconds); err != nil {
			return false
		}
		s.duration = formatPosition(seconds)
		return false
	}
	return false
}

// formatPosition writes seconds as H:MM:SS, because the value's one job
// is to be read in kubectl get output. The seconds are floored, not
// rounded: a position that reads 0:00:01 while the first second still
// plays is wrong in the direction a person notices.
func formatPosition(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		seconds = 0
	}
	whole := int(math.Floor(seconds))
	return fmt.Sprintf("%d:%02d:%02d", whole/3600, whole%3600/60, whole%60)
}

// reportSender is how a report leaves the bridge. The loop takes a
// function rather than the bus so a test catches the reports with no
// broker at all.
type reportSender func(playReport) error

// runReporter is the whole reporting rule in one loop: fold the change,
// send it now when it is one of the two that matter, and otherwise send
// no more than one report per interval. A send that fails is logged and
// the run goes on, because the loss is the operator's view of the film
// and not a reason to stop playing.
func runReporter(ctx context.Context, changes <-chan propertyChange, send reportSender) {
	var state playbackState
	var sent time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case change, open := <-changes:
			if !open {
				return
			}
			atOnce := state.apply(change)
			if !state.reportable() {
				continue
			}
			if !atOnce && time.Since(sent) < reportInterval {
				continue
			}
			sent = time.Now()
			if err := send(state.report()); err != nil {
				fmt.Fprintf(os.Stderr, "bridge: report: %v\n", err)
			}
		}
	}
}
