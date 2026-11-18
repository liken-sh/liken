package main

// The command sidecar runs beside mpv in the playback pod, and it is the
// only program that writes mpv's JSON IPC socket, which both containers
// mount from an emptyDir. It turns messages from the bus into mpv
// commands, and it turns mpv's property changes into status and
// availability messages for the Play. It has no API credentials, so the
// operator reads those messages from the bus and writes the Play.
//
// The sidecar subscribes to the events topic of each Remote of the
// Player, and acts on a press only while that Remote's focus mark is
// this Player's name. keybindings.go maps the kernel's key names to
// commands. The sidecar also subscribes to the Play's commands topic,
// where any program can publish, so a phone or a Home Assistant
// integration controls playback the same way a remote does.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// reportInterval is the shortest time between two reports that change
// only the position. mpv sends time-pos several times a second, and the
// Play needs far fewer. The operator writes a position change to the
// Play less often still, so a program that needs a smooth position
// reads the status topic.
const reportInterval = 1 * time.Second

// busFlushGrace is how long runCommand keeps the bus connection open
// after its closing publishes, so the writer goroutine sends them before
// the process exits. They are QoS 0, so no acknowledgment tells the
// sidecar that they have left. The cleared status needs the wait most,
// because the Last Will marks the pod offline after an unclean exit, but
// nothing except this publish deletes the retained report of a finished
// Play.
var busFlushGrace = 500 * time.Millisecond

// exitGrace is how long exit waits between publishing the ending and
// sending mpv the quit. mpv keeps drawing the film during that time, so
// the compositor has a live surface to fade out. The operator labels the
// pod when it reads the ending, and display-operator starts the fade
// when its pod watch sees the label. The time depends on the compositor
// and not on the Player, so it is a constant and not a Player setting.
const exitGrace = 500 * time.Millisecond

// commander is the state of the command sidecar. The reporter and the
// command handler both write mpv's socket, so mpvMutex keeps their
// lines from interleaving.
type commander struct {
	statusTopic       string
	availabilityTopic string
	commandsTopic     string

	// The Player's commands topic. The sidecar publishes playNext there
	// when the up-next offer is selected, and home on a home command. It
	// is empty when the pod has no Player, and then the sidecar publishes
	// neither.
	playerCommandsTopic string

	// The Play's next block, or nil when the Play has none. The display
	// draws the up-next offer from it, and playNext carries its request.
	next json.RawMessage

	// The Player's volume topic. It is empty for a Player with no sinks,
	// and then the sidecar draws no volume indicator.
	volumeTopic string

	// The last level from the volume topic, retained or live, and whether
	// one has arrived. showVolume compares each live level with it, so a
	// level that differs only in its indicator field draws nothing.
	lastLevel relayLevel
	haveLevel bool

	// The Player's Remotes, keyed by events topic, and the Player's name.
	// A press acts only while its Remote's focus mark equals playerName.
	remotes    map[string]playRemote
	playerName string

	// The last focus mark of each Remote, keyed by its events topic. The
	// press handler reads it while the bus reader writes it.
	focusMu sync.Mutex
	marks   map[string]string

	bus *Bus

	// The presentation block of each item, in playlist order.
	presentations []json.RawMessage

	mpvMutex sync.Mutex
	mpv      net.Conn

	// The request ids of the commands that a person caused, and the log
	// line that each writes when mpv replies. mpvMutex guards them,
	// because the id is assigned as the command is written.
	requestSeq int
	requests   map[int]*mpvRequest

	// The reads that wait for a reply from mpv, keyed by request id, each
	// with its own channel. mpvMutex guards them.
	queries map[int]chan mpvReply

	// The preferences that subtitlechoice.go reads. It is nil when the
	// pod's mode chooses no subtitle track.
	subtitles *subtitlePreferences

	// One line for each operation that a person caused. A test replaces
	// it to read the lines.
	log io.Writer

	// The repeat count of each held key, keyed by events topic and key
	// name. A repeat writes no log line, so the release writes one line
	// with the count. focusMu guards it, because the press handler
	// already holds that lock.
	repeatCounts map[string]int

	// The pod's UID, on every report and availability message. The
	// operator accepts a message only from the run's current pod, and a
	// replacement pod can have the same name.
	podUID string

	// reportMutex guards the last report and ended. The reporter writes
	// them, and onConnect and endRun read them from other goroutines.
	reportMutex sync.Mutex
	lastReport  playReport
	haveReport  bool

	// ended is set when the run ends. Every later report carries it, and
	// onConnect publishes it again after a reconnect.
	ended bool

	// How long exit waits between the ending and the quit. A playback pod
	// sets it to exitGrace. The block server and most tests leave it zero.
	grace time.Duration

	// The playlist item that is playing, counted from one, or zero before
	// the first. The reporter goroutine writes it and the message
	// goroutine reads it.
	itemMutex sync.Mutex
	item      int

	// metrics is nil in the block server and in tests that do not check
	// metrics.
	metrics *commandMetrics
}

// runCommand connects to the bus, drives mpv's IPC socket, and reports
// the run. It returns when mpv closes its socket or the pod receives
// SIGTERM.
func runCommand() {
	namespace := os.Getenv(playNamespaceVariable)
	name := os.Getenv(playNameVariable)
	busAddress := os.Getenv(busAddressVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}

	// The sidecar is PID 1 in its container, and the kernel ignores a
	// signal that PID 1 has no handler for. So the sidecar handles
	// SIGTERM itself, publishes the ending, and quits mpv before the pod's
	// termination grace period ends. The kubelet also sends SIGTERM to mpv,
	// which handles it on its own.
	runCtx, stopRun := context.WithCancel(context.Background())
	defer stopRun()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)

	// deploy/monitoring/podmonitor-screens.yaml scrapes the metrics port
	// that the operator sets on the command container. When the address
	// is unset, as on a workstation, no listener starts.
	metrics := newCommandMetrics(os.Getenv(mediaVersionVariable))
	metrics.serve(os.Getenv(metricsAddressVariable))

	cmd := &commander{
		statusTopic:       playStatusTopic(base, namespace, name),
		podUID:            os.Getenv(podUIDVariable),
		metrics:           metrics,
		availabilityTopic: playAvailabilityTopic(base, namespace, name),
		commandsTopic:     playCommandsTopic(base, namespace, name),
		volumeTopic:       os.Getenv(playerVolumeTopicVariable),
		presentations:     parsePresentations(os.Getenv(presentationsVariable)),
		subtitles:         parseSubtitlePreferences(os.Getenv(subtitlePreferencesVariable)),
		next:              parseNext(os.Getenv(nextVariable)),
		grace:             exitGrace,
		playerName:        os.Getenv(playerNameVariable),
		remotes: playRemoteMap(
			os.Getenv(remoteEventsTopicsVariable),
			os.Getenv(remoteFocusTopicsVariable)),
		marks: map[string]string{},
		log:   os.Stdout,
	}
	if cmd.playerName != "" {
		cmd.playerCommandsTopic = playerCommandsTopic(base, namespace, cmd.playerName)
	}

	// The bus has its own context and not runCtx, so the connection stays
	// open for the closing publishes after SIGTERM cancels runCtx.
	busCtx, stopBus := context.WithCancel(context.Background())
	cmd.bus = newBus(busAddress, "play-"+namespace+"-"+name, cmd.will(), cmd.onConnect, cmd.handle)
	cmd.bus.retainedHandler = cmd.handleRetained
	// The Bus subscribes again after every reconnect.
	cmd.bus.Subscribe(cmd.commandsTopic)
	if cmd.volumeTopic != "" {
		cmd.bus.Subscribe(cmd.volumeTopic)
	}
	cmd.subscribeRemotes(cmd.bus)
	go cmd.bus.Run(busCtx)

	go cmd.exitOnSignal(runCtx, signals, stopRun)

	cmd.report(runCtx)

	// The run has ended. Delete the retained status, so a
	// finished Play does not read as playing, and mark the pod offline.
	cmd.bus.Publish(cmd.statusTopic, nil, true)
	cmd.bus.Publish(cmd.availabilityTopic, playAvailability(availabilityOffline, cmd.podUID), true)
	time.Sleep(busFlushGrace)
	stopBus()
}

// will is the Last Will that the broker publishes when this pod's
// session ends without a clean disconnect.
func (c *commander) will() *busWill {
	return &busWill{
		Topic:    c.availabilityTopic,
		Payload:  playAvailability(availabilityOffline, c.podUID),
		Retained: true,
	}
}

// onConnect runs on each new broker session. It publishes the pod as
// online and publishes the last report again, because a restarted broker
// can have lost its retained messages.
func (c *commander) onConnect(bus *Bus) {
	bus.Publish(c.availabilityTopic, playAvailability(availabilityOnline, c.podUID), true)
	c.reportMutex.Lock()
	payload, have := c.marshalLastReport()
	c.reportMutex.Unlock()
	if have {
		bus.Publish(c.statusTopic, payload, true)
	}
}

// marshalLastReport encodes the last report, and returns false when
// there is none or it does not encode. The caller holds reportMutex.
func (c *commander) marshalLastReport() ([]byte, bool) {
	if !c.haveReport {
		return nil, false
	}
	payload, err := json.Marshal(c.lastReport)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: encoding the last report: %v\n", err)
		return nil, false
	}
	return payload, true
}

// handleRetained handles a retained message that the broker sends at
// subscribe time. A retained level is the volume from before this pod
// started, not a change, so the sidecar records it and draws nothing.
// Every other retained message, such as a focus mark, is handled like a
// live one.
func (c *commander) handleRetained(topic string, payload []byte) {
	if c.volumeTopic != "" && topic == c.volumeTopic {
		c.recordVolume(payload)
		return
	}
	c.handle(topic, payload)
}

// handle routes one message by its topic. Any topic that is not the
// volume topic or a Remote's topic is the commands topic. A payload that
// does not decode is logged and writes nothing to mpv.
func (c *commander) handle(topic string, payload []byte) {
	if c.volumeTopic != "" && topic == c.volumeTopic {
		c.showVolume(payload)
		return
	}
	if c.handleRemote(topic, payload) {
		return
	}
	trigger := "a message on " + topic
	var command mediaCommand
	if err := json.Unmarshal(payload, &command); err != nil {
		logLine(c.log, "command: %s ignored, because it does not decode: %v", trigger, err)
		return
	}
	c.apply(trigger, command, false)
}

// apply runs one command, from a Remote's press or from the commands
// topic. An action that this build has no mpv command for is logged and
// ignored, so a command from a newer program does no harm. A seek or a
// chapter step is sent with no-osd, so mpv draws no position of its own,
// and apply sends a second command that makes the display draw it.
//
// trigger says what caused the command, for its log line. quiet is set
// for the repeat of a held key, which writes no log line, because the
// press wrote one and the release writes the repeat count.
func (c *commander) apply(trigger string, command mediaCommand, quiet bool) {
	switch command.Action {
	case actionHome:
		c.home(trigger, command.Remote)
		return
	case actionStop:
		c.stop(trigger)
		return
	}
	mpv := commandFor(command)
	if mpv == nil {
		logLine(c.log, "command: %s ignored, because this build has no command for the action %q", trigger, command.Action)
		return
	}
	if quiet {
		c.command(repeatOf(command, mpv))
	} else {
		c.request(fmt.Sprintf("command: %s: %s", trigger, describeCommand(command)), mpv)
	}
	if feedback := feedbackFor(command); feedback != nil {
		c.command(feedback)
	}
}

// repeatWord marks a navigation command that a held key repeats. The
// display acts on a repeat like a press but does not log it, so a held
// arrow key writes one log line in the display as in the sidecar.
const repeatWord = "repeat"

// repeatOf adds repeatWord to a navigation command. Other commands go to
// mpv itself, which refuses an extra argument.
func repeatOf(command mediaCommand, mpv []any) []any {
	if isNavigation(command.Action) {
		return append(mpv, repeatWord)
	}
	return mpv
}

// isNavigation reports whether the display script, and not mpv, handles
// the action.
func isNavigation(action string) bool {
	switch action {
	case actionUp, actionDown, actionLeft, actionRight, actionSelect, actionBack:
		return true
	}
	return false
}

func (c *commander) report(ctx context.Context) {
	conn, err := dialMPV(ctx, mpvSocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: no reports for this run: %v\n", err)
		return
	}
	defer conn.Close()
	c.reportOver(ctx, conn)
}

// reportOver reports mpv's state from conn until mpv closes the socket at
// the end of the playlist, or until ctx ends on SIGTERM. Then it ends the
// run. A test calls it with one end of a pipe.
func (c *commander) reportOver(ctx context.Context, conn net.Conn) {
	// A read blocks until mpv writes, which can take minutes while the
	// film is paused, so only closing the socket ends the read when ctx
	// ends.
	defer context.AfterFunc(ctx, func() { conn.Close() })()

	c.drive(ctx, conn, c.send)
	// When the sidecar's own exit closed the socket, that exit already
	// logged why the run ended.
	if ctx.Err() == nil && !c.runEnded() {
		logLine(c.log, "command: mpv closed its socket, so the run ends")
	}

	// exit publishes the ending with the position of the last report. It
	// does not wait a second grace after an earlier exit. drive has
	// already cleared the mpv connection, so the quit goes nowhere.
	c.exit()
}

// drive runs the loop on mpv's socket, for the reporter and for the
// block server. It passes each report to send, and forwards the current
// item's block to the display. The block server passes a send that does
// nothing, because it has no bus.
func (c *commander) drive(ctx context.Context, conn net.Conn, send reportSender) {
	if err := c.attach(conn); err != nil {
		fmt.Fprintf(os.Stderr, "command: no reports for this run: %v\n", err)
		return
	}
	logLine(c.log, "command: connected to mpv's socket")
	defer c.detach()
	// The decode metrics describe the mpv on this connection, so they
	// are cleared when it closes.
	if c.metrics != nil {
		defer c.metrics.clearDecode()
	}
	changes := make(chan propertyChange, 16)
	messages := make(chan clientMessage, 16)
	replies := make(chan mpvReply, 16)
	loads := make(chan struct{}, 4)
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		defer close(changes)
		defer close(messages)
		defer close(replies)
		defer close(loads)
		if err := readEvents(ctx, conn, changes, messages, replies, loads); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "command: reading mpv's socket: %v\n", err)
		}
	}()

	// Each kind of event has its own goroutine, so no handler blocks the
	// reader. serveLoads waits for replies that serveReplies passes to
	// it, so the two cannot share a goroutine.
	go c.serveMessages(messages)
	go c.serveReplies(replies)
	go c.serveLoads(loads)

	runReporter(ctx, changes, send, c.present, c.metrics, c.log)
	<-reading
}

// serveMessages handles the messages that the display broadcasts: the
// exit press, a select on the up-next offer, and a request for the
// current block.
func (c *commander) serveMessages(messages <-chan clientMessage) {
	for message := range messages {
		c.serveMessage(message.Args)
	}
}

func (c *commander) serveMessage(args []string) {
	if isExitMessage(args) {
		logLine(c.log, "command: the display asked to end the run, because back was pressed at the bare film")
		c.exit()
		return
	}
	if isNextRequest(args) {
		c.publishNext()
		return
	}
	if isPresentationRequest(args) {
		c.resendPresentation()
	}
}

// resendPresentation sends the display the block of the item that is
// playing. Before the first item it sends nothing, because present sends
// that block when mpv reports the item.
func (c *commander) resendPresentation() {
	c.itemMutex.Lock()
	item := c.item
	c.itemMutex.Unlock()
	if item < 1 {
		return
	}
	c.command(presentationCommand(c.blockForItem(item)))
	// The display's one request at load asks for the up-next offer too.
	c.sendNext()
}

// attach sets the mpv connection and writes the observe requests under
// one lock, so a command sent as soon as the connection is set cannot
// interleave with them.
func (c *commander) attach(conn net.Conn) error {
	c.mpvMutex.Lock()
	defer c.mpvMutex.Unlock()
	c.mpv = conn
	return observeProperties(conn, observedProperties)
}

// detach clears the connection, so a late command writes nothing. It
// writes now each log line that waits for a reply from mpv, because the
// reply will not arrive.
func (c *commander) detach() {
	c.mpvMutex.Lock()
	c.mpv = nil
	c.abandonRequests()
	c.abandonQueries()
	c.mpvMutex.Unlock()
}

// command writes one command to mpv, or nothing when no connection is
// set.
func (c *commander) command(command []any) {
	c.mpvMutex.Lock()
	defer c.mpvMutex.Unlock()
	if c.mpv == nil {
		return
	}
	if err := sendCommand(c.mpv, command); err != nil {
		fmt.Fprintf(os.Stderr, "command: writing a command to mpv: %v\n", err)
	}
}

// parsePresentations decodes the JSON array of presentation blocks that
// the operator sets on the pod. An unset or malformed value gives no
// blocks, so each item sends the empty object and the display uses the
// file's own metadata.
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

// present sends the display the block of the item that is playing.
func (c *commander) present(item int) {
	first := c.reachItem(item)
	c.command(presentationCommand(c.blockForItem(item)))
	// The up-next offer is for the whole run, so it is sent once, after
	// the first item's block.
	if first {
		c.sendNext()
	}
}

// reachItem records the item that is playing, and returns true when it
// is the first item of the run.
func (c *commander) reachItem(item int) bool {
	c.itemMutex.Lock()
	defer c.itemMutex.Unlock()
	if item == c.item {
		return false
	}
	first := c.item == 0
	c.item = item
	return first
}

// blockForItem returns the block of an item, counted from one, or the
// empty object when the item is outside the list.
func (c *commander) blockForItem(item int) json.RawMessage {
	index := item - 1
	if index < 0 || index >= len(c.presentations) {
		return json.RawMessage(emptyPresentation)
	}
	return c.presentations[index]
}

// send publishes a report to the status topic as a retained message and
// keeps it as the last report. A restarted operator reads the retained
// report, and onConnect publishes the last report again after a
// reconnect, so neither loses the position of a running Play.
func (c *commander) send(report playReport) error {
	c.reportMutex.Lock()
	if c.ended {
		report.Ended = true
	}
	report.Pod = c.podUID
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

// endRun marks the run as ended and publishes the last report with
// Ended set, as a retained message. The operator sets the Player's idle
// status from it, and the idle screen draws from that status. A pod
// takes seconds to terminate, and the screen would show a finished film
// for that time if the operator waited for the pod to go.
//
// A run with no report publishes nothing, because there is no item or
// position to send, and the pod's deletion ends that run. The block
// server has no bus and publishes nothing.
//
// It returns true only for the call that marked the run ended. A later
// call publishes the same report again.
func (c *commander) endRun() bool {
	if c.bus == nil {
		return false
	}
	c.reportMutex.Lock()
	first := !c.ended
	c.ended = true
	c.lastReport.Ended = true
	last := c.lastReport
	payload, have := c.marshalLastReport()
	c.reportMutex.Unlock()
	if have {
		c.bus.Publish(c.statusTopic, payload, true)
		if first {
			logLine(c.log, "command: run ended at item %d, %s, published the ending to %s",
				last.Item, last.Position, c.statusTopic)
		}
	}
	return first
}

func (c *commander) runEnded() bool {
	c.reportMutex.Lock()
	defer c.reportMutex.Unlock()
	return c.ended
}

// exit publishes the ending, waits for the grace, and then sends mpv the
// quit. Every ending of a run takes this path: the display's exit press,
// a stop or home command, the end of the playlist, and SIGTERM. The
// ending goes first, so the operator labels the pod while mpv still
// draws the film, and the compositor fades a live surface. That is also
// why the display asks the sidecar to quit mpv and does not quit it
// itself: only the sidecar publishes the ending.
//
// The quit has exit code 0, so the pod ends as Completed and not Error.
func (c *commander) exit() {
	// Only the call that ended the run waits, because the label is
	// already on the pod for any later call.
	if c.endRun() {
		time.Sleep(c.grace)
	}
	c.command(exitCommand())
}

// exitOnSignal runs exit on SIGTERM and then cancels the run's context,
// so runCommand clears the retained topics as after any other ending.
func (c *commander) exitOnSignal(ctx context.Context, signals <-chan os.Signal, stop context.CancelFunc) {
	select {
	case <-ctx.Done():
	case <-signals:
		logLine(c.log, "command: the kubelet asked the pod to stop, so the run ends")
		c.exit()
		stop()
	}
}

// playbackState keeps the latest value of each property that a report
// contains, because mpv sends one property in each event.
type playbackState struct {
	paused   bool
	item     int
	position string
	duration string

	// The languages of the audio and subtitle tracks that mpv chose. A
	// track with no language keeps the last value.
	audioLanguage    string
	subtitleLanguage string
}

// reportable is false until mpv reports which item plays. item counts
// from one, as the API does, so zero means nothing has loaded.
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

// apply updates the state from one property change, and returns true
// when the change needs a report at once. A pause, a resume, a new item,
// and a new track language are rare, and a person watching `kubectl`
// waits for them. A position or duration change waits for reportInterval.
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

// formatPosition formats seconds as H:MM:SS for `kubectl get`. It drops
// the fraction, because rounding would show 0:00:01 while the first
// second still plays.
func formatPosition(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		seconds = 0
	}
	whole := int(math.Floor(seconds))
	return fmt.Sprintf("%d:%02d:%02d", whole/3600, whole%3600/60, whole%60)
}

// reportSender sends one report. runReporter takes a function and not
// the bus, so a test can collect reports with no broker.
type reportSender func(playReport) error

// itemPresenter sends the block of an item, so a test can run
// runReporter with no mpv socket.
type itemPresenter func(item int)

// runReporter applies each property change to the state, and sends a
// report at once for a change that needs one, and otherwise at most one
// report per reportInterval. A failed send is logged and the loop
// continues, because a lost report is no reason to stop playback.
//
// metrics also reads the decode properties, which no report contains,
// from the same channel. It is nil in the block server and in tests that
// do not check metrics.
//
// log gets one line for each change that a person sees on the screen,
// whether the sidecar, the display, or mpv made it.
func runReporter(
	ctx context.Context, changes <-chan propertyChange, send reportSender,
	present itemPresenter, metrics *commandMetrics, log io.Writer,
) {
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
			if metrics != nil {
				metrics.observe(change)
			}
			before := state
			atOnce := state.apply(change)
			if !state.reportable() {
				continue
			}
			if line := describeChange(before, state); line != "" {
				logLine(log, "command: mpv reports %s", line)
			}
			// A new item is a new file, so the decode metrics of the
			// last file are cleared.
			if state.item != presented {
				presented = state.item
				present(state.item)
				if metrics != nil {
					metrics.clearDecode()
				}
			}
			if !atOnce && time.Since(sent) < reportInterval {
				continue
			}
			sent = time.Now()
			if err := send(state.report()); err != nil {
				fmt.Fprintf(os.Stderr, "command: sending a report: %v\n", err)
			}
		}
	}
}
