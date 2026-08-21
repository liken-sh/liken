package main

// The command sidecar is the playback pod's one owner of mpv's IPC
// socket, a native sidecar beside mpv. It subscribes to the Play's
// commands topic, writes each named command to mpv through the JSON IPC
// socket on the emptyDir the two containers share, and reads mpv's
// property changes back off the same socket to publish the status and
// availability. It holds no API credentials and reaches the control
// plane only through the operator's subscription to the bus.
//
// The commands topic is the whole of what drives mpv now. A translator
// turns a controller's raw events into named commands on that topic,
// and any other program on the bus publishes the same commands, so the
// command sidecar answers a phone or a Home Assistant integration the
// same way it answers a gamepad. The report carries no API object: the
// Play it belongs to is named by the topic, not by the body.

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

// reportInterval is the ceiling on how often the command sidecar
// publishes a position while it advances. mpv sends a time-pos event
// several times a second, and one report is a small QoS 0 message, so
// the bus carries a live position at one report a second. The Play
// resource does not move that fast: the operator wakes its reconcile
// loop only on a pause or an item change, and a bare position advance
// waits for the backstop tick. So the bus is the live plane and the
// resource is the throttled one, and a consumer that wants a smooth
// position reads the bus.
var reportInterval = 1 * time.Second

// busFlushGrace is how long the command sidecar holds the bus open
// after it publishes the two closing messages, so the writer goroutine
// drains them before the process exits. The messages are QoS 0 and
// carry no ack, so this window is the only signal the sidecar has that
// they left. The empty status is the one that needs the window: the
// Last Will publishes offline on an unclean exit, but nothing except
// this publish clears the retained report a finished Play would
// otherwise leave.
var busFlushGrace = 500 * time.Millisecond

// commander holds the command sidecar's two sides: the connection to
// mpv that both the reporter and the command handler write, and the
// last-known report the connect callback re-publishes on a reconnect.
// Two goroutines write mpv's socket, so mpvMutex serializes them: a
// command and an observe request must not interleave their bytes. One
// goroutine reads the last report and another writes it, so reportMutex
// covers that pair.
type commander struct {
	statusTopic       string
	availabilityTopic string
	commandsTopic     string

	bus *Bus

	mpvMutex sync.Mutex
	mpv      net.Conn

	reportMutex sync.Mutex
	lastReport  []byte
	haveReport  bool
}

// runCommand connects to the bus, drives mpv's IPC socket, and reports
// the run. It returns when mpv's socket closes or the kubelet's grace
// signal arrives, and exits zero: the command sidecar is a native
// sidecar, and mpv's own exit code is the pod's outcome.
func runCommand() {
	namespace := os.Getenv(playNamespaceVariable)
	name := os.Getenv(playNameVariable)
	busAddress := os.Getenv(busAddressVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}

	// The kernel runs no default action for a signal sent to PID 1, and
	// the command sidecar is its container's PID 1. The signal context
	// ends the report side on the kubelet's SIGTERM, which is the same
	// end mpv's closed socket gives.
	runCtx, stopRun := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopRun()

	cmd := &commander{
		statusTopic:       playStatusTopic(base, namespace, name),
		availabilityTopic: playAvailabilityTopic(base, namespace, name),
		commandsTopic:     playCommandsTopic(base, namespace, name),
	}

	// The bus runs on its own context, not the signal context, so the
	// two closing publishes still have a live connection to leave on
	// after the grace signal ends the report side.
	busCtx, stopBus := context.WithCancel(context.Background())
	cmd.bus = newBus(busAddress, "play-"+namespace+"-"+name,
		&busWill{Topic: cmd.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		cmd.onConnect, cmd.handle)
	// The subscription is made once. The Bus remembers the filter and
	// re-sends it on every reconnect, so a broker restart does not need
	// the command sidecar to subscribe again.
	cmd.bus.Subscribe(cmd.commandsTopic)
	go cmd.bus.Run(busCtx)

	cmd.report(runCtx)

	// The run is over: mpv ended or the kubelet is terminating the pod.
	// Clear the retained status so a finished Play leaves no report that
	// reads as still playing, mark the availability offline, and hold
	// the bus open long enough to send both before the connection ends.
	cmd.bus.Publish(cmd.statusTopic, nil, true)
	cmd.bus.Publish(cmd.availabilityTopic, []byte(availabilityOffline), true)
	time.Sleep(busFlushGrace)
	stopBus()
}

// onConnect refills the broker the moment a session reaches a CONNACK.
// It publishes online, and re-publishes the last-known report, because
// the broker drops its retained set on a restart and a reconnect must
// leave the current status behind again.
func (c *commander) onConnect(bus *Bus) {
	bus.Publish(c.availabilityTopic, []byte(availabilityOnline), true)
	c.reportMutex.Lock()
	payload, have := c.lastReport, c.haveReport
	c.reportMutex.Unlock()
	if have {
		bus.Publish(c.statusTopic, payload, true)
	}
}

// handle turns one commands message into an mpv command. The topic is
// fixed, so the payload alone matters: it decodes to a named command,
// and commandFor turns that name into mpv's own words. A payload that
// does not decode, or an action this build has no command for, writes
// nothing, so a newer program's command degrades to no effect rather
// than a crash.
func (c *commander) handle(topic string, payload []byte) {
	var command mediaCommand
	if err := json.Unmarshal(payload, &command); err != nil {
		return
	}
	mpv := commandFor(command)
	if mpv == nil {
		return
	}
	c.command(mpv)
}

// report is the reporting side of the run. It dials mpv, observes the
// four properties the status is made of, and turns each change into a
// report on the bus. It ends when mpv's socket closes, which is how mpv
// says the run is over, or when the context ends on the grace signal.
func (c *commander) report(ctx context.Context) {
	conn, err := dialMPV(ctx, mpvSocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: no reports for this run: %v\n", err)
		return
	}
	defer conn.Close()
	// The read blocks until mpv writes, which can be minutes apart in a
	// paused film, so closing the socket is what ends the read when the
	// context ends. A running mpv ends the read by closing its own end.
	defer context.AfterFunc(ctx, func() { conn.Close() })()

	// observeProperties writes the socket, and so does the commands
	// handler, so the observe runs under the same mutex the handler
	// takes. The connection is set first, so a command that arrives
	// mid-observe waits on the mutex rather than reaching a nil socket.
	if err := c.attach(conn); err != nil {
		fmt.Fprintf(os.Stderr, "command: no reports for this run: %v\n", err)
		return
	}
	defer c.detach()

	changes := make(chan propertyChange, 16)
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		defer close(changes)
		if err := readEvents(ctx, conn, changes); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "command: mpv's socket: %v\n", err)
		}
	}()

	runReporter(ctx, changes, c.send)
	<-reading
}

// attach sets the mpv connection and observes the properties under one
// lock, so the first writes to the socket cannot interleave with a
// command the handler sends the moment the connection is live.
func (c *commander) attach(conn net.Conn) error {
	c.mpvMutex.Lock()
	defer c.mpvMutex.Unlock()
	c.mpv = conn
	return observeProperties(conn, observedProperties)
}

// detach forgets the connection when the run ends, so a late command
// writes nothing rather than a closed socket.
func (c *commander) detach() {
	c.mpvMutex.Lock()
	c.mpv = nil
	c.mpvMutex.Unlock()
}

// command writes one mpv command under the mutex. A command that
// arrives before mpv is dialed, or after the run ends, finds a nil
// connection and writes nothing.
func (c *commander) command(command []any) {
	c.mpvMutex.Lock()
	defer c.mpvMutex.Unlock()
	if c.mpv == nil {
		return
	}
	if err := sendCommand(c.mpv, command); err != nil {
		fmt.Fprintf(os.Stderr, "command: mpv command: %v\n", err)
	}
}

// send publishes one report to the status topic, retained, and holds it
// as the last-known report. A restarted operator reads the retained
// report back from the broker, and a reconnect re-publishes the held
// report through onConnect, so neither loses a running Play's place.
func (c *commander) send(report playReport) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	c.reportMutex.Lock()
	c.lastReport = payload
	c.haveReport = true
	c.reportMutex.Unlock()
	c.bus.Publish(c.statusTopic, payload, true)
	return nil
}

// playbackState is the run at one moment, as the command sidecar holds
// it. The fields are the report's fields, held between events, because
// each event carries one property and a report carries all of them.
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

// reportSender is how a report leaves the command sidecar. The loop
// takes a function rather than the bus so a test catches the reports
// with no broker at all.
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
				fmt.Fprintf(os.Stderr, "command: report: %v\n", err)
			}
		}
	}
}
