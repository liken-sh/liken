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
	"image"
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

	// The items' presentation blocks in playlist order, baked into the
	// pod. Index i is item i's block text, which the sidecar forwards to
	// the display as the playlist reaches each item.
	presentations []json.RawMessage

	mpvMutex sync.Mutex
	mpv      net.Conn

	reportMutex sync.Mutex
	lastReport  playReport
	haveReport  bool

	// ended is set once any of the three endings has happened. It is held
	// rather than sent and forgotten, so every later report of this run
	// carries the mark too and a reconnect re-publishes it. It sits under
	// reportMutex because the ending arrives on the message goroutine or
	// on the run's own goroutine, and the reporter reads it on each send.
	ended bool

	// Where the bridge writes decoded bgra. It is the shared art volume in the
	// pod, and a local directory under the harness.
	artDir string

	// artItem is the item the shared art volume holds art for. artCache maps a
	// decoded size to its blob. Both sit under artMutex, because the reporter
	// goroutine swaps the item while the art goroutine reads and writes the
	// cache.
	artMutex sync.Mutex
	artItem  int
	artCache map[string]artBlob

	// The one decoded sheet the bridge holds. A scrub within one sheet crops
	// every tile from this image, so it reads no new file.
	trickSheet    image.Image
	trickSheetKey string

	// The last tile written for the current item. A request that maps to the
	// same tile replies with this blob and crops nothing, so the overlay does
	// not churn.
	trickHave bool
	trickItem int
	trickIdx  int
	trickBlob artBlob

	// The prior tile file, kept one step longer than the current one. mpv places
	// a tile on a later turn than the reply that named it, so that file must
	// still exist when mpv maps it. The bridge removes a tile only once a second,
	// newer tile has replaced it.
	trickHavePrev bool
	trickPrev     artBlob
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
		presentations:     parsePresentations(os.Getenv(presentationsVariable)),
		artDir:            artMountPath,
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
	// report published the ending report first, so a subscriber that is
	// listening has already read the mark. Clear the retained status so a
	// finished Play leaves no report that reads as still playing, mark the
	// availability offline, and hold the bus open long enough to send both
	// before the connection ends.
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
	payload, have := c.marshalLastReport()
	c.reportMutex.Unlock()
	if have {
		bus.Publish(c.statusTopic, payload, true)
	}
}

// marshalLastReport encodes the last-known report, and reports whether
// there is one. The caller holds reportMutex, so the payload and the
// state it came from cannot diverge. A report that will not marshal is
// logged and treated as no report at all, because there is nothing to put
// on the bus in its place.
func (c *commander) marshalLastReport() ([]byte, bool) {
	if !c.haveReport {
		return nil, false
	}
	payload, err := json.Marshal(c.lastReport)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: report: %v\n", err)
		return nil, false
	}
	return payload, true
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
	// A seek or a chapter jump carries no-osd, so the sidecar summons the
	// display to draw the new position. mpv shows the feedback for every
	// other command, so feedbackFor returns nil and the sidecar sends
	// nothing more.
	if feedback := feedbackFor(command); feedback != nil {
		c.command(feedback)
	}
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

	c.drive(ctx, conn, c.send)

	// drive returns for one of two reasons, and each is an ending: mpv
	// reached the end of the last item and closed its socket, or the
	// kubelet's SIGTERM ended the context. So the mark goes out here, with
	// the position the last report carried, before runCommand clears the
	// retained status and marks the pod offline.
	c.endRun()
}

// drive is the socket loop the reporter and the standalone art server share.
// It observes the properties, reads the events, folds each into a report
// through send, forwards the current item's block, and answers each logo
// request. The reporter passes its bus-backed send. The art server passes a
// send that reports nothing, so the same decode code runs with or without a
// bus.
func (c *commander) drive(ctx context.Context, conn net.Conn, send reportSender) {
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
	messages := make(chan clientMessage, 16)
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		defer close(changes)
		defer close(messages)
		if err := readEvents(ctx, conn, changes, messages); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "command: mpv's socket: %v\n", err)
		}
	}()

	// The message goroutine answers the display's exit press and its logo
	// requests off the same socket the reporter reads. It ends when the
	// socket closes and the reader closes the messages channel.
	go c.serveMessages(messages)

	runReporter(ctx, changes, send, c.present)
	<-reading
}

// serveMessages answers the two things the display broadcasts: the exit
// press and each art request. It runs beside the reporter, so a slow
// decode or a network fetch never holds up the position reports.
func (c *commander) serveMessages(messages <-chan clientMessage) {
	for message := range messages {
		if isExitMessage(message.Args) {
			c.exit()
			continue
		}
		c.serveArt(message.Args)
	}
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

// parsePresentations reads the baked array into an indexed slice. An
// unset or malformed value leaves no blocks, so every item forwards the
// empty object and the display falls back to the file.
func parsePresentations(value string) []json.RawMessage {
	if value == "" {
		return nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal([]byte(value), &blocks); err != nil {
		return nil
	}
	return blocks
}

// present hands the current item's block to the display over the mpv
// socket.
//
// It clears the previous item's art first, so the shared volume holds only
// what the current item can show.
func (c *commander) present(item int) {
	c.swapArt(item)
	c.command(presentationCommand(c.blockForItem(item)))
}

// swapArt drops the previous item's decoded art when the playlist reaches a
// new item, so one item's logo never lingers on the next and the shared volume
// holds only the current item's blobs.
func (c *commander) swapArt(item int) {
	c.artMutex.Lock()
	defer c.artMutex.Unlock()
	if item == c.artItem {
		return
	}
	for _, blob := range c.artCache {
		os.Remove(blob.path)
	}
	c.artCache = nil
	if c.trickHave {
		os.Remove(c.trickBlob.path)
	}
	if c.trickHavePrev && c.trickPrev.path != c.trickBlob.path {
		os.Remove(c.trickPrev.path)
	}
	c.trickHave = false
	c.trickHavePrev = false
	c.trickSheet = nil
	c.trickSheetKey = ""
	c.artItem = item
}

// blockForItem returns one item's block, or the empty object when the
// item falls outside the baked list. The item counts from one, so its
// index is one less.
func (c *commander) blockForItem(item int) json.RawMessage {
	index := item - 1
	if index < 0 || index >= len(c.presentations) {
		return json.RawMessage(emptyPresentation)
	}
	return c.presentations[index]
}

// send publishes one report to the status topic, retained, and holds it
// as the last-known report. A restarted operator reads the retained
// report back from the broker, and a reconnect re-publishes the held
// report through onConnect, so neither loses a running Play's place.
func (c *commander) send(report playReport) error {
	c.reportMutex.Lock()
	// The reporter folds mpv's property changes alone and carries no
	// ending, so the mark is stamped here. Every report after the ending
	// carries it, and a subscriber that reads any one of them reads the
	// same ending.
	if c.ended {
		report.Ended = true
	}
	payload, err := json.Marshal(report)
	if err == nil {
		c.lastReport = report
		c.haveReport = true
	}
	c.reportMutex.Unlock()
	if err != nil {
		return err
	}
	c.bus.Publish(c.statusTopic, payload, true)
	return nil
}

// endRun marks the run over and publishes the mark at once, retained,
// beside the numbers the last report carried. Every ending calls it: the
// exit press, mpv reaching the end of the last item, and the kubelet's
// SIGTERM.
//
// The operator turns the mark into the Player's idle status and the
// re-present that draws the idle screen again. The pod takes seconds to
// terminate, and an ending read from the pod's own death would leave a
// dead film on the screen for every one of them.
//
// A run that never reported publishes nothing, the same rule the reporter
// follows: mpv has not said which item plays, so there are no numbers to
// carry, and the pod's own death is what ends such a run. The art server
// runs this same code with no bus, and it publishes nothing either.
func (c *commander) endRun() {
	if c.bus == nil {
		return
	}
	c.reportMutex.Lock()
	c.ended = true
	c.lastReport.Ended = true
	payload, have := c.marshalLastReport()
	c.reportMutex.Unlock()
	if !have {
		return
	}
	c.bus.Publish(c.statusTopic, payload, true)
}

// exit ends the run at a person's press. The display broadcasts the exit
// message when a person presses back at the bare video, and the sidecar
// answers it here rather than letting the display quit mpv itself,
// because the order is the whole point: the ending reaches the bus
// first, and only then does quit reach mpv. So the operator holds the
// mark while the film is still on the display, and how far mpv gets
// through its shutdown before its socket closes changes nothing.
//
// The exit code is zero, so the pod ends Completed, the outcome a film
// that ran to its end gives, and not Error.
func (c *commander) exit() {
	c.endRun()
	c.command(exitCommand())
}

// playbackState is the run at one moment, as the command sidecar holds
// it. The fields are the report's fields, held between events, because
// each event carries one property and a report carries all of them.
type playbackState struct {
	paused   bool
	item     int
	position string
	duration string

	// The language of the audio track and the subtitle track mpv chose. Each stays
	// set once mpv reports it.
	audioLanguage    string
	subtitleLanguage string
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
		Paused:           s.paused,
		Item:             s.item,
		Position:         s.position,
		Duration:         s.duration,
		AudioLanguage:    s.audioLanguage,
		SubtitleLanguage: s.subtitleLanguage,
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
	case audioLanguageProperty:
		var lang string
		if err := json.Unmarshal(change.Data, &lang); err != nil {
			return false
		}
		changed := lang != s.audioLanguage
		s.audioLanguage = lang
		return changed
	case subtitleLanguageProperty:
		var lang string
		if err := json.Unmarshal(change.Data, &lang); err != nil {
			return false
		}
		changed := lang != s.subtitleLanguage
		s.subtitleLanguage = lang
		return changed
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

// itemPresenter is how a forwarded block leaves the loop, a function
// like reportSender, so a test catches the forward with no mpv socket.
type itemPresenter func(item int)

// runReporter is the whole reporting rule in one loop: fold the change,
// send it now when it is one of the two that matter, and otherwise send
// no more than one report per interval. A send that fails is logged and
// the run goes on, because the loss is the operator's view of the film
// and not a reason to stop playing.
func runReporter(ctx context.Context, changes <-chan propertyChange, send reportSender, present itemPresenter) {
	var state playbackState
	var sent time.Time
	var presented int
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
			// Forward the block on the first item and on every advance,
			// keyed on the item and not the throttled position, so the
			// display swaps its presentation the moment the playlist
			// reaches a new item.
			if state.item != presented {
				presented = state.item
				present(state.item)
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
