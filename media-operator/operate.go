package main

// The operator is level-triggered. A watch event, a bus message, or the
// tick only wakes the loop, and each pass then derives every status from
// the cluster as it is now, not from the object that an event carried.
// So a lost event delays the work by at most one tick, a burst of events
// costs one pass, and a restarted operator needs no replay of what it
// missed. A pass reads the watches' caches (clusterview.go), so a pass
// with nothing to change sends the API server no request.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// Environment variables of the operator. An image variable that is set
// overrides the image that images.go derives from the operator's own
// pod.
const (
	playerImageVariable = "PLAYER_IMAGE"

	// IDLE_IMAGE is the image of the client that draws the idle screen.
	// An idle container runs it unless the Player or the default
	// MediaPreferences sets spec.idle.image.
	idleImageVariable = "IDLE_IMAGE"

	// SIDECAR_IMAGE is the image that holds only this program, and
	// every sidecar container runs it. It has none of the player
	// image's mpv, drivers, or fonts, because a sidecar decodes
	// nothing.
	sidecarImageVariable = "SIDECAR_IMAGE"

	// DISPLAY_IMAGE is the image that the display container runs.
	displayImageVariable = "DISPLAY_IMAGE"

	// IDLE_DISPLAY_CLASS names the shareable DeviceClass that a Player's
	// idle pod claims a display through. The class is cluster policy, so
	// the Deployment sets it. When it is unset, reconcileIdle creates no
	// idle claim and no idle pod.
	idleDisplayClassVariable = "IDLE_DISPLAY_CLASS"

	// MEDIA_METRICS_ADDRESS is the address of the /metrics listener, and
	// an empty value turns the listener off. The operator also sets it on
	// the media-screen and idle pods that it creates, each at that
	// client's own port.
	metricsAddressVariable = "MEDIA_METRICS_ADDRESS"
)

// tickInterval is how often the loop runs a pass when nothing woke it.
// Two changes wake nothing: a playing position that advanced, which the
// pass writes to the Play's status, and a Finished Play whose time to
// live ran out, which the pass deletes. Every other change wakes the
// pass through a watch (clusterwatch.go) or the bus handler.
const tickInterval = 10 * time.Second

// positionWriteInterval limits how often a Play's status is written when
// only its position advanced. The command sidecar publishes the position
// on the bus every second, and each status write wakes the operator's
// own Plays watch, so a write every second would keep the loop and the
// API server busy. A pause, an item change, or a phase change is written
// at once.
//
// Only the tick writes a bare position advance. Each write is stamped a
// little after its tick, so with an interval equal to tickInterval the
// next tick would come too early and the position would be written on
// every second tick. The interval is 2 seconds shorter than the tick, so
// a steady film writes its position on every tick.
const positionWriteInterval = 8 * time.Second

// catchUpGrace is how long the operator waits after a broker session
// starts before it publishes a Player's volume level or a controller's
// focus mark that it did not receive from the broker. The broker sends
// its retained values right after the subscribe, and the wait lets them
// arrive first. Without it, a restarted operator would publish a level
// again and draw the volume indicator on every screen, or publish a
// focus mark that moves a controller to a different Player.
const catchUpGrace = 2 * time.Second

// defaultTTLSecondsAfterFinished is how long a Finished Play remains
// when its spec does not set ttlSecondsAfterFinished. While the Play
// remains, `kubectl get plays` shows what just played and where it
// stopped.
const defaultTTLSecondsAfterFinished = 300

// operator holds what every pass needs. The bus and the caches are
// fields and not globals, so a test can build an operator around ones
// it can inspect.
type operator struct {
	// client sends every write, and every read that must include this
	// operator's own last write. view serves every other read from the
	// watches' caches (clusterview.go).
	client       *apiclient.Client
	view         *clusterView
	image        string
	idleImage    string
	sidecarImage string
	displayImage string
	busAddress   string
	topicBase    string
	// idleDisplayClass is empty when the idle screen is off.
	idleDisplayClass string
	// playerVerbose is MEDIA_PLAYER_VERBOSE, which the operator copies
	// onto each playback pod that it creates. So `kubectl set env` on
	// the Deployment turns on mpv's full output for the pods created
	// after the restart.
	playerVerbose string
	// resources holds the cpu and memory settings of each container that
	// the operator builds, read once at start (containerresources.go).
	resources resourceSettings
	bus       *Bus
	reports   *reports
	// focus holds the retained focus mark of each controller. It shares
	// the wake channel of reports, so a cycle request on the bus wakes
	// the pass that settles it.
	focus *focusCache

	// playerReceivers maps each Player to the Receiver that it is connected to.
	// The pass fills it, and the bus reader reads it when a press must
	// select the Player's input on that Receiver.
	playerReceivers *playerReceivers

	// sessions writes each change to a Receiver's status.session, for
	// the pass and the bus reader (receiverasks.go).
	sessions sessionWriter

	// levels is the volume engine (volumeengine.go). The pass gives it
	// each Player's volume devices and their reports, and the bus reader
	// gives it the presses.
	levels *volumeEngine

	// seenScreenRequests records the screenAsk requests of the Televisions that
	// the pass relayed (screenask.go). Only the pass goroutine uses it.
	seenScreenRequests screenRequestLog

	// peripherals holds the bluetooth-operator's Peripherals and the
	// Peripheral that each Remote's claim allocated. The pass fills it,
	// and the pass and the bus reader both read it to build a Player's
	// bus status, which has each controller's link and charge.
	peripherals *peripheralCache

	// codes holds the key codes that each controller declared. A change
	// wakes the pass, which writes the unbound codes to the Remote's
	// status.
	codes *codesCache

	// panels holds the newest panel desire that each Player's idle
	// client published. A change wakes the pass, which writes the
	// override onto the screen's Display.
	panels *panelCache

	// panelOverrides holds the override that this operator last applied
	// for each Player, so a pass writes the Display only on a change,
	// and the override of a deleted Player can still be removed. Only
	// the pass goroutine uses it.
	panelOverrides map[string]panelOverride

	// absenceChecked holds each persistent pod, as namespace/name, that
	// the pass wants gone and has read from the API server since this
	// process started (persistent.go). Only the pass goroutine uses it.
	absenceChecked map[string]bool

	// panelFaults holds the last panel fault logged for each Player, so a
	// screen with no Display is logged once. Only the pass goroutine
	// uses it.
	panelFaults map[string]string

	// The screen and Receiver lookups of one pass. The pass resets them
	// at its start. Only the pass goroutine uses them.
	screenCache   *screens
	receiverCache *receivers

	// receiverFaults holds each Player whose last session write to its
	// Receiver failed. The pass retries the write on every pass, and
	// this set makes the first failure and the later success post one
	// Event each. Only the pass goroutine uses it.
	receiverFaults map[string]bool

	// receiverSessions holds the session that this operator last applied
	// for each Player, so a pass writes a Receiver only on a change, and
	// the session of a deleted Player can still be removed. Only the
	// pass goroutine uses it.
	receiverSessions map[string]receiverSession

	// lastScreens records the last screen that each Player resolved, and
	// when it stopped resolving (screengap.go). Only the pass goroutine
	// uses it.
	lastScreens map[string]lastScreen

	// specReleased holds each Receiver whose spec has no session from
	// this operator: one that the pass read with no spec.session, or one
	// whose spec.session the pass removed since this process started.
	// Only the pass goroutine uses it.
	specReleased map[string]bool

	// catchUpEnds is when the retained values of the current broker
	// session have had time to arrive (catchUpGrace). It is zero before
	// the first session. Only the pass goroutine uses it.
	catchUpEnds time.Time

	// endingLabeled holds, for each run, the UID of the pod that the
	// pass labeled as ending, so each pod is patched once and a new pod
	// of the same run is labeled again. Only the pass goroutine uses it.
	endingLabeled map[string]string

	// positionWrites holds when the pass last wrote each run's status, or
	// found it current, for positionWriteInterval. Only the pass
	// goroutine uses it.
	positionWrites map[string]time.Time

	// displayRestarts holds, for each run, the restart count of the
	// display container that the last pass read, so the counter adds
	// only the growth. It also holds the Player label of the run's
	// metric series, so the end of the run deletes that series. Only the
	// pass goroutine uses it.
	displayRestarts map[string]displayRestartMemo

	// keysPublished maps each Remote's keys topic to the table that the
	// operator last published there, so an unchanged table is not
	// published again, and the topic of a deleted Remote can be found
	// and cleared. Only the pass goroutine uses it.
	keysPublished map[string]string

	// panelClears holds each Player under media.liken.sh/none whose
	// clear of the retained panel desire the bus did not queue, so the
	// next pass publishes the clear again. Only the pass goroutine uses
	// it.
	panelClears map[string]bool

	// keyTables holds the table last logged for each keys topic. A new
	// broker session resets keysPublished and not keyTables, so the
	// publish after a reconnect writes no log line, and a person's edit
	// does.
	keyTables map[string]string

	// playerStatuses holds the payload that the operator last published
	// on each Player's status topic. The pass and the bus reader both
	// write it, so it has its own mutex.
	playerStatuses publishedStatuses

	// snapshot holds the Plays and Players that the last pass listed.
	// The bus reader answers an ending from it (ending.go).
	snapshot passSnapshot

	// recreateBackoff holds each run's recreate backoff. Only the pass
	// goroutine uses it.
	recreateBackoff map[string]backoffState

	// replacements holds, for each run, the reason for a recreate whose
	// delete was sent and whose new pod is not created yet
	// (replace.go). Only the pass goroutine uses it.
	replacements map[string]recreate

	// wake is the loop's wake channel. requeueAfter sends on it at a
	// deadline, so the loop does not wait for the next tick.
	wake chan<- struct{}

	// now is the clock for the screen gap and for status.poweredOnAt. A
	// test replaces it to move time without waiting.
	now func() time.Time

	// busReconnected is set on the bus goroutine when a session receives
	// its CONNACK, and read on the pass goroutine, which then publishes
	// the retained state again (reestablishRetained).
	busReconnected atomic.Bool

	// metrics is nil in a test that does not need it.
	metrics *mediaMetrics

	// log receives one line for each operation that a person caused.
	log io.Writer

	// recorder posts the Events on Plays, Players, and Remotes
	// (events.go). A nil recorder posts nothing.
	recorder *events.Recorder
}

func operate() {
	// A failure in setup ends the process. The kubelet restarts the pod
	// with a backoff, and the failure shows in kubectl instead of being
	// retried silently.
	busAddress := os.Getenv(busAddressVariable)
	if busAddress == "" {
		fmt.Fprintf(os.Stderr, "%s is unset, so the operator has no message bus address. Set it in the Deployment\n", busAddressVariable)
		os.Exit(1)
	}
	topicBase := os.Getenv(topicBaseVariable)
	if topicBase == "" {
		topicBase = defaultTopicBase
	}
	idleDisplayClass := os.Getenv(idleDisplayClassVariable)
	playerVerbose := os.Getenv(playerVerboseVariable)
	metricsAddress := os.Getenv(metricsAddressVariable)

	client, err := inClusterClient(requestTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the in-cluster configuration: %v\n", err)
		os.Exit(1)
	}

	images, err := resolveImages(client)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// The metrics listener starts before the Lease wait, so a copy that
	// waits for the Lease can be scraped too.
	metrics := newMediaMetrics(operatorVersion(client))
	metrics.serve(metricsAddress)

	// Only the process that holds the Lease goes past this line, so a
	// second copy opens no bus session and reconciles nothing
	// (leader.go). A signal ends the wait here, or ends the loop below
	// after its pass.
	stop, cancelSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancelSignals()
	leader := lead(stop)
	// Every write from here on, from the passes, the bus handler, the
	// volume engine, and the Event recorder, goes through the leader's
	// write guard.
	client = leader.guard(client)

	// The watches and the bus handler share one wake channel, because a
	// wake only means "run a pass".
	wake := make(chan struct{}, 1)
	playReports := newReports(wake)
	focusCache := newFocusCache(wake)
	codesCache := newCodesCache(wake)
	panels := newPanelCache(wake)
	media := &operator{
		client:           client,
		image:            images.player,
		idleImage:        images.idle,
		sidecarImage:     images.sidecar,
		displayImage:     images.display,
		busAddress:       busAddress,
		topicBase:        topicBase,
		idleDisplayClass: idleDisplayClass,
		playerVerbose:    playerVerbose,
		resources:        loadResourceSettings(containerResourcesPath, func(line string) { fmt.Println(line) }),
		reports:          playReports,
		focus:            focusCache,
		playerReceivers:  newPlayerReceivers(),
		peripherals:      newPeripheralCache(),
		codes:            codesCache,
		panels:           panels,
		panelOverrides:   map[string]panelOverride{},
		panelFaults:      map[string]string{},
		receiverSessions: map[string]receiverSession{},
		lastScreens:      map[string]lastScreen{},
		specReleased:     map[string]bool{},
		endingLabeled:    map[string]string{},
		positionWrites:   map[string]time.Time{},
		displayRestarts:  map[string]displayRestartMemo{},
		keysPublished:    map[string]string{},
		recreateBackoff:  map[string]backoffState{},
		replacements:     map[string]recreate{},
		wake:             wake,
		now:              time.Now,
		metrics:          metrics,
		log:              os.Stdout,
		recorder:         events.New(context.Background(), client, operatorComponent, events.Options{}),
	}
	media.levels = media.newVolumeEngine()

	// A restarted broker has none of the retained state that the
	// operator owns, so each new session makes the next pass publish it
	// again. newSession makes the focus cache and the volume engine drop
	// what the last session sent, so after catchUpGrace an empty entry
	// means that the broker lost the value.
	onConnect := func(bus *Bus) {
		// The flag is set first, so a pass that runs between these lines
		// treats the catch-up as not over and publishes no mark from the
		// records that newSession is about to clear.
		media.busReconnected.Store(true)
		media.levels.newSession()
		media.focus.newSession()
		signalWake(wake)
	}
	media.bus = newBus(busAddress, "media-operator", nil, onConnect, media.handleBusMessage)
	for _, filter := range busFilters(topicBase) {
		media.bus.Subscribe(filter)
	}
	// The bus session must end before the Lease is released, because a
	// press or a report on the bus can write to the API. Run returns only
	// after the handler returns.
	busContext, stopBus := context.WithCancel(context.Background())
	busDone := make(chan struct{})
	go func() {
		media.bus.Run(busContext)
		close(busDone)
	}()
	// quiet ends the bus session and reports whether it ended within 5
	// seconds. stepDown calls it before it releases the Lease.
	quiet := func() bool {
		stopBus()
		select {
		case <-busDone:
			return true
		case <-time.After(5 * time.Second):
			return false
		}
	}

	// The watches start after the Lease is held, so a copy that waits
	// for the Lease watches nothing. The first pass waits until every
	// watch has listed its collection, because a pass over an empty view
	// would release every run and every persistent pod. When a required
	// collection cannot be listed within clusterSyncWait, the process
	// ends. So a cluster where bluetooth-operator serves no Peripherals
	// stops the operator, and the operator does not run without the
	// controllers' links. The Displays and the Receivers are optional
	// (clusterwatch.go).
	//
	// When the wait fails or a signal ends it, the operator releases the
	// Lease before it exits, so a waiting copy takes the Lease on its
	// next read and does not wait for it to expire.
	watcher, err := informer.InCluster()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the in-cluster configuration for the watches: %v\n", err)
		leader.stepDown(quiet, media.recorder)
		os.Exit(1)
	}
	watching, stopWatching := context.WithCancel(context.Background())
	defer stopWatching()
	firstRead, cancelFirstRead := context.WithTimeout(stop, clusterSyncWait)
	media.view, err = watchCluster(watching, firstRead, watcher, media.client, wake, metrics)
	cancelFirstRead()
	if err != nil {
		if stop.Err() != nil {
			leader.stepDown(quiet, media.recorder)
			return
		}
		fmt.Fprintf(os.Stderr, "watching the cluster: %v\n", err)
		leader.stepDown(quiet, media.recorder)
		os.Exit(1)
	}
	media.reports.readPodsFrom(media.view)
	plays, _ := media.view.Plays(media.client)
	remotes, _ := media.view.Remotes()
	fmt.Printf("media.liken.sh: operating %d plays and %d remotes over %s\n",
		len(plays), len(remotes), busAddress)

	ticker := time.NewTicker(tickInterval)
	for {
		media.pass()
		select {
		case <-wake:
		case <-ticker.C:
		case <-stop.Done():
			leader.stepDown(quiet, media.recorder)
			return
		}
	}
}

// pass runs one reconcile over every Play, Player, and Remote in the
// cluster. A failure on one object is reported and the pass continues,
// because one broken run must not freeze the status of every other
// Player.
func (o *operator) pass() {
	// Reset the lookups, so a cable that moved between passes is seen.
	o.screenCache, o.receiverCache = nil, nil
	if o.metrics != nil {
		o.metrics.busConnected.Set(boolToFloat(o.bus.Connected()))
	}
	if o.busReconnected.Swap(false) {
		o.reestablishRetained()
	}
	plays, err := o.view.Plays(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading Plays: %v\n", err)
		return
	}
	o.snapshot.recordPlays(plays)
	// Tell reports which pod each run has before the endings below are
	// read, so the ending report of a pod that is gone stops counting
	// (runpod.go).
	for index := range plays {
		namespace, name := plays[index].Metadata.Namespace, plays[index].Metadata.Name
		pod, err := o.view.Pod(namespace, podName(name))
		switch {
		case errors.Is(err, apiclient.ErrNotFound):
			o.reports.observe(namespace, name, nil)
		case err == nil:
			o.reports.observe(namespace, name, pod)
		}
	}
	// Label the ending pods first. After its ending report, the sidecar
	// keeps mpv running only for a short grace period, and the label
	// must reach display-operator within it, because the label makes the
	// compositor fade the surface (ending.go).
	for index := range plays {
		o.labelEnding(&plays[index])
	}
	// Publish each Player's bus status now, before the slow writes of
	// the rest of the pass, because a person is waiting: the browser and
	// other readers of the status topic act when a Player turns Idle.
	// reconcilePlayers publishes again at the end of the pass, with the
	// settled focus mark and the controllers' links, and an unchanged
	// payload is not sent twice.
	//
	// publishEndingStatus usually publishes an ending first, from the last
	// pass's lists, as soon as the report arrives. This publish then
	// sends nothing. It is the only publish for a run that the last pass
	// had not listed.
	players, playersErr := o.view.Players(o.client)
	if playersErr != nil {
		fmt.Fprintf(os.Stderr, "reading Players: %v\n", playersErr)
	} else {
		o.snapshot.recordPlayers(players)
		o.publishPlayerStatuses(players, plays)
	}
	// A missing default MediaPreferences is not an error, and a failed
	// read leaves this pass without the default.
	defaults, err := o.view.MediaPreferences(mediaPreferencesName)
	if err != nil {
		if !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "reading MediaPreferences: %v\n", err)
		}
		defaults = nil
	}
	live := make(map[string]bool, len(plays))
	// A Player runs only its newest Play, so the pass finds the
	// superseded Plays from the whole list before it reconciles any.
	superseded := supersededPlays(plays)
	for index := range plays {
		play := &plays[index]
		namespace, name := play.Metadata.Namespace, play.Metadata.Name
		live[runKey(namespace, name)] = true
		// Garbage collection keeps a deleting Play's pod until the Play's
		// finalizers clear, and the pod keeps the Player's claim until
		// then. So removePlayFinalizer deletes the pod itself, and the next run
		// can start at once. A deleting Play is reconciled no further, so
		// nothing recreates its pod.
		if play.Metadata.deleting() {
			o.removePlayFinalizer(play)
			continue
		}
		// Add the finalizer before the run starts, so the operator clears
		// the retained topics of a Play that is deleted right after its
		// creation.
		o.addPlayFinalizer(play)
		// Deleting a superseded Play is the whole ending: the sidecar
		// quits mpv when the pod receives SIGTERM, and the library's store
		// records the last position before the finalizer clears.
		if superseded[runKey(namespace, name)] {
			if err := o.deletePlay(namespace, name); err != nil {
				fmt.Fprintf(os.Stderr, "deleting superseded Play %s/%s: %v\n", namespace, name, err)
			} else {
				logLine(o.log, "play %s/%s: deleted, because a newer play on player %s replaces it",
					namespace, name, playerName(play))
				o.postEnded(play, reasonSuperseded, "deleted, because a newer play on player "+playerName(play)+" replaces it")
			}
			continue
		}
		// A Failed Play is not retired, because the reconcile below
		// resumes it.
		if finishedPhase(play.Status.Phase) {
			if err := o.retire(play); err != nil {
				fmt.Fprintf(os.Stderr, "retiring Play %s/%s: %v\n",
					play.Metadata.Namespace, play.Metadata.Name, err)
			}
			continue
		}
		start := time.Now()
		err := o.reconcile(play, defaults)
		if o.metrics != nil {
			o.metrics.observeReconcile(kindPlay, time.Since(start), err)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "reconciling Play %s/%s: %v\n",
				play.Metadata.Namespace, play.Metadata.Name, err)
		}
	}
	o.reports.retain(live)
	for key := range o.positionWrites {
		if !live[key] {
			delete(o.positionWrites, key)
		}
	}
	for key := range o.endingLabeled {
		if !live[key] {
			delete(o.endingLabeled, key)
		}
	}
	for key := range o.displayRestarts {
		if !live[key] {
			o.forgetDisplayRestarts(key)
		}
	}
	for key := range o.recreateBackoff {
		if !live[key] {
			delete(o.recreateBackoff, key)
		}
	}
	for key := range o.replacements {
		if !live[key] {
			delete(o.replacements, key)
		}
	}
	o.reclaimPlays(live)
	// observePeripherals runs before any Player status is written,
	// because a Player's bus status has each controller's link and
	// charge. When the Remotes cannot be read, the pass skips it and
	// reconcileRemotes, so no cache shrinks to a collection that the
	// operator could not read.
	remotes, remotesErr := o.view.Remotes()
	var remoteClaims map[string]claimRead
	if remotesErr != nil {
		fmt.Fprintf(os.Stderr, "reading Remotes: %v\n", remotesErr)
	} else {
		remoteClaims = o.observePeripherals(remotes)
	}
	if playersErr == nil {
		// The idle clock's time zone is one setting for the cluster, from
		// the default MediaPreferences only. Each Player's own idle block
		// overrides the default idle policy field by field.
		var zone string
		var defaultIdle *IdlePolicy
		if defaults != nil {
			zone = defaults.Spec.TimeZone
			defaultIdle = defaults.Spec.Idle
		}
		// Settle focus first, because a Player's bus status says whether
		// each controller's focus mark names that Player.
		o.reconcileFocus(players)
		start := time.Now()
		o.reconcilePlayers(players, plays, zone, defaultIdle)
		o.relayScreenAsks()
		if o.metrics != nil {
			o.metrics.observeReconcile(kindPlayer, time.Since(start), nil)
		}
	}
	if remotesErr == nil {
		start := time.Now()
		o.reconcileRemotes(remotes, remoteClaims, o.loadKeymaps())
		if o.metrics != nil {
			o.metrics.observeReconcile(kindRemote, time.Since(start), nil)
		}
	}
}

// handleBusMessage stores one bus message in the cache for its topic,
// or acts on it. An empty payload is the clear of a retained value. The
// operator ignores it on a Play's topics, because it publishes that
// clear itself when it reclaims a deleted Play, and reading the clear
// back as an offline signal would mark the run as seen and reclaim it
// again forever.
func (o *operator) handleBusMessage(topic string, payload []byte) {
	if namespace, name, kind, ok := parsePlayTopic(o.topicBase, topic); ok {
		if len(payload) == 0 {
			return
		}
		switch kind {
		case playStatusKind:
			var report playReport
			if err := json.Unmarshal(payload, &report); err != nil {
				return
			}
			// A person is waiting on an ending, so it is published here
			// and not left to the pass that the fold wakes.
			if o.reports.fold(namespace, name, report) {
				o.publishEndingStatus(namespace, name)
			}
		case playAvailabilityKind:
			online, pod := parsePlayAvailability(payload)
			o.reports.availability(namespace, name, online, pod)
		}
		return
	}
	if namespace, name, ok := parseRemoteFocusTopic(o.topicBase, topic); ok {
		o.focus.setMark(controllerKey(namespace, name), string(payload))
		return
	}
	if namespace, name, ok := parseRemoteFocusCycleTopic(o.topicBase, topic); ok {
		o.focus.requestCycle(controllerKey(namespace, name))
		return
	}
	// A press that turns the Player on does nothing else. Every other
	// press asks the Player's Receiver to select the Player's input, and
	// ensureInput drops a repeat or a release, so a held button sends one
	// request. A volume key changes the level on the press and on each
	// repeat.
	if namespace, name, ok := parseRemoteEventsTopic(o.topicBase, topic); ok {
		if o.pressPower(namespace, name, payload) {
			return
		}
		o.ensureInput(namespace, name, payload)
		o.pressVolume(namespace, name, payload)
		return
	}
	// Availability decides whether the declared codes count, because a
	// retained codes document outlives the pod that wrote it.
	if namespace, name, ok := parseRemoteAvailabilityTopic(o.topicBase, topic); ok {
		if len(payload) == 0 {
			return
		}
		o.codes.setAvailability(controllerKey(namespace, name),
			string(payload) == availabilityOnline)
		return
	}
	// A Remote's reader pod clears its codes topic when the controller's
	// input devices close, so the codes cache drops the document.
	if namespace, name, ok := parseRemoteCodesTopic(o.topicBase, topic); ok {
		key := controllerKey(namespace, name)
		if len(payload) == 0 {
			o.codes.clear(key)
			return
		}
		var codes remoteCodes
		if err := json.Unmarshal(payload, &codes); err != nil {
			return
		}
		o.codes.setCodes(key, codes)
		return
	}
	// A home request on a Player's commands topic asks the Player's
	// Receiver to show the Player, as a Remote's home press does.
	if namespace, name, ok := parsePlayerCommandsTopic(o.topicBase, topic); ok {
		o.showForHomeAsk(namespace, name, payload)
		return
	}
	// The panel cache keeps its desire on a clear. reconcilePlayers
	// drops the desire of a Player that nothing draws.
	if namespace, name, ok := parsePlayerPanelTopic(o.topicBase, topic); ok {
		if len(payload) == 0 {
			return
		}
		var panel panelDesire
		if err := json.Unmarshal(payload, &panel); err != nil {
			return
		}
		o.panels.setState(playerKey(namespace, name), panel.Desire)
		return
	}
	// The retained level is this operator's own earlier publish. Reading
	// it back keeps a restarted operator from publishing it again.
	if namespace, name, ok := parsePlayerVolumeTopic(o.topicBase, topic); ok {
		if len(payload) > 0 {
			o.levels.recordBrokerPayload(playerKey(namespace, name), payload)
		}
		return
	}
	if namespace, name, ok := parsePlayerVolumeCommandsTopic(o.topicBase, topic); ok {
		o.commandVolume(namespace, name, payload)
	}
}

// reestablishRetained runs on the first pass of a new broker session,
// because a restarted broker has none of the operator's retained
// values. It forgets which key tables and Player statuses were
// published, so reconcileRemotes and reconcilePlayers publish all of
// them again in this pass. The volume levels and the focus marks wait
// for catchUpGrace, and then only the values that the broker did not
// send back are published. So a controller keeps its Player across a
// broker restart, and an operator restart publishes no value that the
// broker already has. The command sidecar and the Remote reader pods
// publish their own retained topics when they reconnect.
func (o *operator) reestablishRetained() {
	o.keysPublished = map[string]string{}
	o.playerStatuses.reset()
	o.catchUpEnds = time.Now().Add(catchUpGrace)
	// Run a pass when the grace ends, not at the next tick.
	o.requeueAfter(catchUpGrace)
}

// caughtUp reports whether the retained values of the current broker
// session have had time to arrive, so that a cache with no value for a
// key means that the broker has none. It is false before the first
// session, while the bus is disconnected, and from a connect until
// catchUpGrace after the pass that runs reestablishRetained.
func (o *operator) caughtUp() bool {
	if o.catchUpEnds.IsZero() || o.busReconnected.Load() || !o.bus.Connected() {
		return false
	}
	return !time.Now().Before(o.catchUpEnds)
}

// retire ends a Finished Play. The first pass that reads the Finished
// phase deletes the playback pod and its claim, because the final
// position is in the Play's status and the ending was already published.
// A Failed Play is not retired, because its pod's log is what a person
// debugs from.
//
// The same pass writes status.finishedAt, so later passes skip the
// deletes and only compare the time with playTTL. Both deletes treat an
// absent object as success, so a retry after a failed status write is
// safe. Nothing wakes the pass when the time to live ends, so the Play
// is deleted up to one tickInterval later.
func (o *operator) retire(play *Play) error {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	now := time.Now()
	finished, stamped := finishedTime(play, now)
	if !stamped {
		if err := DeletePod(o.client, namespace, podName(name)); err != nil {
			return err
		}
		if err := DeleteResourceClaim(o.client, namespace, claimName(name)); err != nil {
			return err
		}
		logLine(o.log, "play %s/%s: finished at item %d, %s, deleted its playback pod and its claim",
			namespace, name, play.Status.Item, play.Status.Position)
	}
	// Garbage collection removes anything that the deletes above missed,
	// and the finalizer keeps the Play until the operator has cleared its
	// retained topics, as for a Play that a person deleted.
	if now.Sub(finished) >= playTTL(play) {
		if err := o.deletePlay(namespace, name); err != nil {
			return err
		}
		logLine(o.log, "play %s/%s: deleted, because %s passed after it finished", namespace, name, playTTL(play))
		o.postEnded(play, reasonRetired, fmt.Sprintf("deleted, because %s passed after it finished", playTTL(play)))
		return nil
	}
	if stamped {
		return nil
	}
	status := play.Status
	status.FinishedAt = finished.UTC().Format(time.RFC3339)
	return writePlayStatus(o.client, o.view.plays.Versions, play, status)
}

// supersededPlays returns the key of each unfinished Play that a newer
// Play on the same Player replaces. So a play request from any program
// ends the film that is playing. Without this, the pod of the second
// Play would stay Pending on the Player's claim, and nothing would end
// the first. A Play that is finished, failed, being deleted, or names
// no Player takes no part.
func supersededPlays(plays []Play) map[string]bool {
	newest := map[string]*Play{}
	superseded := map[string]bool{}
	for index := range plays {
		play := &plays[index]
		if !unfinishedPlay(play) {
			continue
		}
		unit := runKey(play.Metadata.Namespace, playerName(play))
		held, seen := newest[unit]
		if !seen {
			newest[unit] = play
			continue
		}
		older := held
		if newerPlay(play, held) {
			newest[unit] = play
		} else {
			older = play
		}
		superseded[runKey(older.Metadata.Namespace, older.Metadata.Name)] = true
	}
	return superseded
}

func unfinishedPlay(play *Play) bool {
	return play.Metadata.DeletionTimestamp == "" &&
		!finishedPhase(play.Status.Phase) &&
		play.Status.Phase != phaseFailed &&
		playerName(play) != ""
}

// newerPlay reports whether play is newer than than. The later name
// breaks a tie, so every pass picks the same run. A Play with no
// creation time is the older one.
func newerPlay(play, than *Play) bool {
	created, other := playCreated(play), playCreated(than)
	if !created.Equal(other) {
		return created.After(other)
	}
	return play.Metadata.Name > than.Metadata.Name
}

func playCreated(play *Play) time.Time {
	created, err := time.Parse(time.RFC3339, play.Metadata.CreationTimestamp)
	if err != nil {
		return time.Time{}
	}
	return created
}

// playTTL is how long a Play remains after it finishes. A spec that
// sets zero deletes the Play on the pass that reads it Finished, and a
// spec that sets nothing gets defaultTTLSecondsAfterFinished.
func playTTL(play *Play) time.Duration {
	if play.Spec.TTLSecondsAfterFinished == nil {
		return defaultTTLSecondsAfterFinished * time.Second
	}
	return time.Duration(*play.Spec.TTLSecondsAfterFinished) * time.Second
}

// finishedTime returns status.finishedAt, and whether it parsed. When
// it is missing or does not parse, it returns now, so a garbled value
// restarts the time to live and does not keep a Finished Play forever.
func finishedTime(play *Play, now time.Time) (time.Time, bool) {
	stamped, err := time.Parse(time.RFC3339, play.Status.FinishedAt)
	if err != nil {
		return now, false
	}
	return stamped, true
}

// loadKeymaps indexes the Keymaps by name. publishKeys compiles them,
// because each Remote's table is the base table with that Remote's
// Keymap applied, and a Remote with no Keymap still gets the base.
func (o *operator) loadKeymaps() map[string]*Keymap {
	keymaps := map[string]*Keymap{}
	list, err := o.view.Keymaps()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading Keymaps: %v\n", err)
		return keymaps
	}
	for index := range list {
		keymap := &list[index]
		keymaps[keymap.Metadata.Name] = keymap
	}
	return keymaps
}

// publishKeys compiles one Remote's table and publishes it retained on
// the Remote's keys topic when it changed. A Keymap that does not
// compile publishes nothing and leaves the last good table on the
// broker, so a broken edit does not stop a controller. It returns the
// compiled table, for the unbound codes in the Remote's status, or nil
// when the Keymap does not compile.
func (o *operator) publishKeys(remote *Remote, keymaps map[string]*Keymap, present map[string]bool) []compiledBinding {
	topic := remoteKeysTopic(o.topicBase, remote.Metadata.Namespace, remote.Metadata.Name)
	present[topic] = true
	table, err := compileTable(keymaps[remote.Spec.Keymap])
	if err != nil {
		// Log the refusal once for each distinct error, not on every
		// pass until a person fixes the Keymap.
		refusal := "refused: " + err.Error()
		if o.keyTables[topic] != refusal {
			if o.keyTables == nil {
				o.keyTables = map[string]string{}
			}
			o.keyTables[topic] = refusal
			logLine(o.log, "remote %s/%s: key table not published, the last good table stays: compiling %s: %v",
				remote.Metadata.Namespace, remote.Metadata.Name, keymapSource(remote), err)
			o.recorder.Warning(remoteRef(remote), reasonKeymapRefused,
				fmt.Sprintf("the key table was not published, and the last good table stays: compiling %s: %v", keymapSource(remote), err))
		}
		return nil
	}
	payload, err := json.Marshal(table)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding the key table of Remote %s/%s: %v\n",
			remote.Metadata.Namespace, remote.Metadata.Name, err)
		return nil
	}
	// Record a table only when the bus queued it, so a table that the
	// bus dropped is sent again on the next pass.
	if o.keysPublished[topic] != string(payload) && o.bus.Publish(topic, payload, true) {
		o.keysPublished[topic] = string(payload)
		if o.keyTables[topic] != string(payload) {
			if o.keyTables == nil {
				o.keyTables = map[string]string{}
			}
			o.keyTables[topic] = string(payload)
			logLine(o.log, "remote %s/%s: published a key table of %s, %s, to %s",
				remote.Metadata.Namespace, remote.Metadata.Name,
				countOf(len(table), "row", "rows"), keymapSource(remote), topic)
		}
	}
	return table
}

func keymapSource(remote *Remote) string {
	if remote.Spec.Keymap == "" {
		return "the base with no Keymap"
	}
	return "the base with Keymap " + remote.Spec.Keymap
}

// reconcilePlayers writes every Player's status, publishes its bus
// status, and reconciles its idle pod. Both statuses are derived from
// the same activity, and the idle screen reads the bus status, so it
// needs no API credentials. The publish here repeats the early publish
// of the pass with this pass's focus marks and controller links, and
// sends nothing when those changed nothing.
func (o *operator) reconcilePlayers(players []Player, plays []Play, timeZone string, defaultIdle *IdlePolicy) {
	published := make(map[string]bool, len(players))
	live := make(map[string]bool, len(players))
	// drawn holds the Players whose idle screen something draws. The
	// panel caches treat a Player under media.liken.sh/none like a
	// deleted Player: its desire is dropped, and a panel that it left
	// off has its override removed.
	drawn := make(map[string]bool, len(players))
	lookup := o.screenLookup()
	// matched holds the Players whose screen matches a Receiver input.
	// The session on that Receiver lasts while the match does, whether
	// the Player is idle or playing.
	matched := make(map[string]bool, len(players))
	// The media_players gauge counts Players by zone and state, not one
	// series per Player.
	playerCounts := map[[2]string]int{}
	for index := range players {
		player := &players[index]
		key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
		live[key] = true
		desired := derivePlayerStatus(player, plays, o.reports)
		// derivePlayerStatus builds a new status, so reconcileScreen and
		// reconcileSinks carry forward the fields that only the status
		// records: the Screen field and condition, which say why an idle
		// pod waits, and which Sink the scheduler picked for each of the
		// spec's selections. media-api reads the Sinks from here, because
		// the run's claim is gone between runs.
		if player.Spec.Display != nil {
			desired.Screen, desired.Conditions = o.reconcileScreen(player, key, lookup)
		}
		if len(player.Spec.Sinks) > 0 {
			desired.Sinks = o.reconcileSinks(player, desired.Play)
		}
		idle := resolveIdle(player.Spec.Idle, defaultIdle, o.idleImage)
		// A pass that changes spec.power requests the change on the
		// hardware once (playerpower.go).
		power := powerStepFor(player)
		var outcome powerOutcome
		if idle.Controller == idleControllerNone {
			// Nothing draws, so the status reports no panel. Clear the
			// retained desire, so a restarted operator does not read the
			// last desire of the idle pod back from the broker after the
			// pod is gone. The bus drops a clear while it has no session
			// or its queue is full, so panelClears keeps the Player until
			// a clear is queued.
			if o.panels.stateFor(key) != "" || o.panelClears[key] {
				o.clearPanel(key, player.Metadata.Namespace, player.Metadata.Name)
			}
		} else {
			drawn[key] = true
			desired.Panel = o.reconcilePanel(player, key, lookup, idle.OffMode, power, &outcome)
		}
		// The session's active flag follows whether a run exists.
		unfinished := playerHasUnfinishedPlay(player, plays)
		desired.Receiver = o.reconcileReceiver(player, unfinished, power, &outcome)
		matched[key] = desired.Receiver != nil
		o.settlePower(player, plays, power, &outcome, &desired)
		// The idle block tells a delegate operator what it needs to
		// draw this Player's screen.
		desired.Idle = deriveIdleStatus(player, idle.Controller, o.busAddress, o.topicBase,
			o.idleClaimFor(player), idle, gatherIdleRemotes(player, o.topicBase))
		// The volume engine publishes the devices' reports and holds a
		// pending target until a report reaches it, so it receives them
		// on every pass.
		o.levels.observe(key, o.volumeDevices(player, desired.Sinks), o.caughtUp())
		// Take the activity and the Play from the derivation that the
		// publish made, so the Player's status and the gauge match the
		// bus, even when an ending arrived during this Player's work
		// above.
		topic, derived := o.publishPlayerStatus(player, plays)
		published[topic] = true
		desired.Activity, desired.Play = derived.Activity, derived.Play
		playerCounts[[2]string{player.Spec.Zone, playerMetricState(player.Metadata.Namespace, desired, plays)}]++
		moved, err := writePlayerStatusFrom(o.client, o.view.players.Versions, player, desired)
		if err != nil {
			fmt.Fprintf(os.Stderr, "writing the status of Player %s/%s: %v\n",
				player.Metadata.Namespace, player.Metadata.Name, err)
		}
		// A Screen of Unknown means that no Display reports the saved
		// monitor, which a person must fix, so it posts a Warning. A
		// Screen of False repeats the Display, and with PanelAway the
		// CRD documents it as expected, so it posts a Normal Event. A
		// PowerApplied of False means that a write to the hardware failed,
		// so it posts a Warning.
		for _, condition := range moved {
			bad := conditions.Unknown
			if condition.Type == powerAppliedCondition {
				bad = conditions.False
			}
			o.recorder.Transition(playerRef(player), condition, bad)
		}
		if err == nil {
			o.postPowerChange(player, desired, power, moved)
		}
		if err := o.reconcileIdle(player, timeZone, defaultIdle); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling the idle pod of Player %s/%s: %v\n",
				player.Metadata.Namespace, player.Metadata.Name, err)
		}
	}
	o.panels.retain(drawn)
	// A pending clear is dropped when its Player is deleted, or when
	// something draws its idle screen again, because that idle pod
	// publishes a desire of its own.
	for key := range o.panelClears {
		if !live[key] || drawn[key] {
			delete(o.panelClears, key)
		}
	}
	o.removeGonePanelOverrides(drawn)
	// A Player that is gone, or whose screen no longer matches an
	// input, gives up its Receiver session.
	o.retainSessions(matched)
	o.retainHeldScreens(live)
	o.playerReceivers.retain(live)
	o.levels.retain(live)
	// Clear the status topic of each deleted Player, so a subscriber
	// draws nothing for it.
	o.playerStatuses.clear(o.bus, published)
	if o.metrics != nil {
		// Reset first, so a zone or state with no Player in this pass
		// is removed and does not keep an earlier pass's count.
		o.metrics.players.Reset()
		for bucket, count := range playerCounts {
			o.metrics.players.WithLabelValues(bucket[0], bucket[1]).Set(float64(count))
		}
	}
}

// publishPlayerStatus derives one Player's bus status and publishes it
// on the Player's retained status topic, unless the broker already has
// that payload. It returns the topic and the derived PlayerStatus. The
// pass and the bus reader both call it, so the status is derived while
// the mutex of playerStatuses is held (publishedStatuses).
func (o *operator) publishPlayerStatus(player *Player, plays []Play) (string, PlayerStatus) {
	topic := playerStatusTopic(o.topicBase, player.Metadata.Namespace, player.Metadata.Name)
	var desired PlayerStatus
	was, moved, err := o.playerStatuses.publishDerived(o.bus, topic, func() ([]byte, string, error) {
		desired = derivePlayerStatus(player, plays, o.reports)
		status := derivePlayerBusStatus(player, desired, plays, o.peripherals, o.focus)
		payload, err := json.Marshal(status)
		return payload, status.Activity, err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding the bus status of Player %s/%s: %v\n",
			player.Metadata.Namespace, player.Metadata.Name, err)
		return topic, desired
	}
	if moved {
		logLine(o.log, "player %s/%s: activity %s, was %s%s, published to %s",
			player.Metadata.Namespace, player.Metadata.Name, desired.Activity, was, playOf(desired), topic)
	}
	return topic, desired
}

func playOf(status PlayerStatus) string {
	if status.Play == "" {
		return ""
	}
	return ", play " + status.Play
}

// reconcileRemotes reconciles each Remote's reader pod, publishes its
// key table, and writes its status. It runs after reconcileFocus, so the
// status has the focus mark that this pass settled.
func (o *operator) reconcileRemotes(remotes []Remote, claims map[string]claimRead, keymaps map[string]*Keymap) {
	live := make(map[string]bool, len(remotes))
	present := make(map[string]bool, len(remotes))
	for index := range remotes {
		remote := &remotes[index]
		key := controllerKey(remote.Metadata.Namespace, remote.Metadata.Name)
		live[key] = true
		if err := o.reconcileRemote(remote, claims[key]); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling Remote %s/%s: %v\n",
				remote.Metadata.Namespace, remote.Metadata.Name, err)
		}
		table := o.publishKeys(remote, keymaps, present)
		// One write sets the mark and the unbound codes together,
		// because two writers of one status would undo each other, and
		// each write wakes the watch.
		desired := RemoteStatus{
			Player:     o.focus.markFor(key),
			Peripheral: o.peripherals.peripheralFor(key),
		}
		// A Keymap that did not compile reports no unbound codes, because
		// this pass has no compiled table to compare the codes with.
		if declared, held := o.codes.codesFor(key); held && table != nil {
			desired.Unbound = unboundCodes(declared, table)
		}
		if err := writeRemoteStatus(o.client, remote, desired); err != nil {
			fmt.Fprintf(os.Stderr, "writing the status of Remote %s/%s: %v\n",
				remote.Metadata.Namespace, remote.Metadata.Name, err)
		}
	}
	o.codes.retain(live)
	o.clearGoneKeys(present)
}

// clearGoneKeys clears the retained table of each Remote that is gone.
// A topic leaves keysPublished only after the bus queued the clear, so
// a dropped clear is sent again on the next pass.
func (o *operator) clearGoneKeys(present map[string]bool) {
	for topic := range o.keysPublished {
		if !present[topic] && o.bus.Publish(topic, nil, true) {
			delete(o.keysPublished, topic)
		}
	}
}

// writeRemoteStatus skips an unchanged status and retries a conflict
// once, as the Play and Player writers do. The operator watches
// Remotes, so an unneeded write would wake another pass, which would
// write again.
func writeRemoteStatus(c *apiclient.Client, remote *Remote, desired RemoteStatus) error {
	same, err := sameRemoteStatus(remote.Status, desired)
	if err != nil {
		return err
	}
	if same {
		return nil
	}

	remote.Status = desired
	_, err = PutRemoteStatus(c, remote)
	if !errors.Is(err, apiclient.ErrConflict) {
		return err
	}

	// The desired status does not depend on what changed, so it is
	// written unchanged onto the fresh copy.
	fresh, err := GetRemote(c, remote.Metadata.Namespace, remote.Metadata.Name)
	if err != nil {
		return err
	}
	same, err = sameRemoteStatus(fresh.Status, desired)
	if err != nil || same {
		return err
	}
	fresh.Status = desired
	_, err = PutRemoteStatus(c, fresh)
	return err
}

// sameRemoteStatus compares the marshaled forms, because the API server
// stores the marshaled form, and omitempty decides which fields it has.
func sameRemoteStatus(current, desired RemoteStatus) (bool, error) {
	was, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	wants, err := json.Marshal(desired)
	if err != nil {
		return false, err
	}
	return string(was) == string(wants), nil
}

// reconcile reconciles one Play from the view, and again from the API
// server when the run needs the pass to act (viewReads).
func (o *operator) reconcile(play *Play, defaults *MediaPreferences) error {
	err := o.reconcileFrom(play, defaults, o.viewReads(), false)
	if errors.Is(err, errReadLive) {
		return o.reconcileFrom(play, defaults, o.liveReads(), true)
	}
	return err
}

var errReadLive = errors.New("the run needs a read from the API server")

type runReads struct {
	player func(namespace, name string) (*Player, error)
	remote func(namespace, name string) (*Remote, error)
}

// viewReads reads a run's Player and Remotes from the view, so a
// settled run costs no request. A run that the pass would act on reads
// them again from the API server (liveReads): one whose Player or Remote
// the view lacks, which would fail the Play, and one whose pod the pass
// would create or replace. Each collection has its own watch, and
// nothing orders one watch's events against another's. So a Play
// applied in one file with its Player can reach the view before the
// Player, and a Player edit can reach it after the pod that the edit
// changed. The second read keeps the pass from failing a Play for an
// object that exists, and from building a pod from an old Player spec.
func (o *operator) viewReads() runReads {
	return runReads{
		player: func(namespace, name string) (*Player, error) { return o.view.Player(o.client, namespace, name) },
		remote: o.view.Remote,
	}
}

func (o *operator) liveReads() runReads {
	return runReads{
		player: func(namespace, name string) (*Player, error) { return o.view.FreshPlayer(o.client, namespace, name) },
		remote: func(namespace, name string) (*Remote, error) { return GetRemote(o.client, namespace, name) },
	}
}

// reconcileFrom reconciles one Play with the given reads. Nothing is
// created until the Player is read and every URI resolves, so a Play
// that can never run leaves no objects behind. live is true when the
// reads come from the API server. A reconcile from the view returns
// errReadLive when the run needs the pass to act.
func (o *operator) reconcileFrom(play *Play, defaults *MediaPreferences, reads runReads, live bool) error {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	var defaultSpec *MediaPreferencesSpec
	if defaults != nil {
		defaultSpec = &defaults.Spec
	}
	if playerName(play) == "" {
		return o.refusePlay(play, PlayStatus{
			Phase:   phaseFailed,
			Message: "the Play names no Player",
		})
	}

	player, err := reads.player(namespace, playerName(play))
	if errors.Is(err, apiclient.ErrNotFound) {
		if !live {
			return errReadLive
		}
		prefs := resolvePreferences(&play.Spec, nil, defaultSpec)
		return o.writePlay(play, derivePlayStatus(play, nil, nil, nil, nil, prefs))
	}
	if err != nil {
		return err
	}
	prefs := resolvePreferences(&play.Spec, &player.Spec, defaultSpec)

	resolved, resolveErr := resolvePlay(namespace, play.Spec.Items, play.Spec.Next, o.patternScreen(player))
	if resolveErr != nil {
		return o.refusePlay(play, derivePlayStatus(play, player, resolveErr, nil, nil, prefs))
	}

	// A missing Remote fails the Play only while the run has no pod, so
	// a Remote that is deleted during a film does not fail the film. The
	// Keymaps do not reach the pod, so a broken Keymap never fails a
	// Play (publishKeys).
	remotes, remoteErr := gatherRemotes(reads.remote, player)
	if remoteErr != nil {
		// A pod that the operator created in the last pass may not be in
		// the view yet, so a pod that the view lacks is read from the API
		// server.
		if !live {
			if _, err := o.view.Pod(namespace, podName(name)); err != nil {
				return errReadLive
			}
		} else {
			_, err := GetPod(o.client, namespace, podName(name))
			if errors.Is(err, apiclient.ErrNotFound) {
				return o.refusePlay(play, derivePlayStatus(play, player, remoteErr, nil, nil, prefs))
			}
			if err != nil {
				return err
			}
		}
		remotes = nil
	}
	// The topic base belongs to the operator, so the topics are filled in
	// here and not in the gather.
	for index := range remotes {
		remotes[index].EventsTopic = remoteEventsTopic(o.topicBase, namespace, remotes[index].Name)
		remotes[index].FocusTopic = remoteFocusTopic(o.topicBase, namespace, remotes[index].Name)
	}

	claim := buildClaim(play, player)
	var pod *Pod
	fresh := false
	if live {
		pod, fresh, err = o.ensurePlayback(play, player, claim, resolved, prefs, remotes, remoteErr != nil)
		if err != nil {
			return err
		}
	} else {
		var settled bool
		if pod, settled = o.viewedPlayback(play, claim, remotes, remoteErr != nil); !settled {
			return errReadLive
		}
		if pod != nil {
			delete(o.replacements, runKey(namespace, name))
		}
	}
	// A new run takes over its controllers' focus. A recreated pod
	// resumes the same run, so the focus stays where a person left it.
	if fresh && len(remotes) > 0 {
		o.takeFocus(play, remotes)
	}
	o.countDisplayRestarts(play, pod)
	status := derivePlayStatus(play, player, nil, pod, o.reports.latestFor(namespace, name), prefs)
	// After backoffNoteThreshold recreates, the backoff note replaces the
	// pod's failure message, but only while the Play is Failed, so a run
	// that recovers has a clean status.
	if status.Phase == phaseFailed {
		if note, backing := o.backoffNote(runKey(namespace, name)); backing {
			status.Message = note
		}
	}
	return o.writePlay(play, status)
}

// writePlay writes a Play's status. A change that is only a position
// advance waits positionWriteInterval. A phase change gets a log line,
// an Event, and a count on the playback counters, and each changed
// condition posts its Event. The change is compared with the status that
// the API server had, which writePlayStatusFrom returns, so a pass that
// read a stale Play does not log, post, or count the change twice, and a
// refused write posts nothing.
func (o *operator) writePlay(play *Play, desired PlayStatus) error {
	return o.writePlayAs(play, desired, reasonPlaybackFailed)
}

// refusePlay writes the Failed status of a Play whose spec can never
// run, with the Event reason InvalidSpec.
func (o *operator) refusePlay(play *Play, desired PlayStatus) error {
	return o.writePlayAs(play, desired, reasonInvalidSpec)
}

// writePlayAs is writePlay with failReason as the Event reason of a
// change to Failed.
func (o *operator) writePlayAs(play *Play, desired PlayStatus, failReason string) error {
	key := runKey(play.Metadata.Namespace, play.Metadata.Name)
	if onlyPositionChanged(play.Status, desired) &&
		time.Since(o.positionWrites[key]) < positionWriteInterval {
		return nil
	}
	held, wrote, err := writePlayStatusFrom(o.client, o.view.plays.Versions, play, desired)
	if err != nil {
		return err
	}
	o.positionWrites[key] = time.Now()
	if !wrote {
		return nil
	}
	for _, condition := range transitions(held.Conditions, desired.Conditions) {
		o.recorder.Transition(playRef(play), condition, conditions.False)
	}
	if desired.Phase != held.Phase {
		logLine(o.log, "play %s: phase %s, was %s%s", key, desired.Phase, phaseName(held.Phase), messageOf(desired))
		o.postPhase(play, desired, failReason)
		if o.metrics != nil {
			o.metrics.notePlaybackPhase(held.Phase, desired)
		}
	}
	return nil
}

// phaseName returns "none" for the empty phase of a new Play.
func phaseName(phase string) string {
	if phase == "" {
		return "none"
	}
	return phase
}

func messageOf(status PlayStatus) string {
	if status.Message == "" {
		return ""
	}
	return ": " + status.Message
}

// ensurePlayback makes the run's pod match the pod that the current
// Player produces. A run with no pod gets its claim and its pod. A
// Failed pod, and a pod whose claim or Remotes the Player changed, is
// recreated at the film's position. When keepExisting is set, because
// the Remotes could not be gathered, an existing pod stays as it is,
// even a Failed one. A pod that is being deleted is left to finish,
// and the pass that finds it gone creates the replacement.
//
// The bool is true only for a new run: no pod, no replacement owed, and
// no saved position. The caller then takes over the run's controllers.
func (o *operator) ensurePlayback(play *Play, player *Player, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote, keepExisting bool) (*Pod, bool, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	key := runKey(namespace, name)
	// The pass is about to act, so it reads the pod from the API server
	// and not from the view (clusterview.go).
	running, err := GetPod(o.client, namespace, podName(name))
	if errors.Is(err, apiclient.ErrNotFound) {
		if reason, owed := o.replacements[key]; owed {
			pod, err := o.finishReplacement(play, player, claim, resolved, prefs, remotes, reason)
			return pod, false, err
		}
		// The run has no pod because the Play is new, or because an
		// eviction or a lost node removed it. Both cases wait for the
		// recreate backoff.
		_, resuming := o.resumePoint(play)
		if !o.mayResume(key) {
			return nil, false, nil
		}
		if err := ensureClaim(o.client, claim); err != nil {
			return nil, false, err
		}
		// Turn the Player on and apply its Receiver session before the
		// pod exists, so the equipment is on the right input when mpv
		// draws its first frame.
		o.turnOnForPlay(play, player)
		o.applyReceiverSession(player)
		pod, err := o.createPodAtSavedPosition(play, player, claim, resolved, prefs, remotes)
		switch {
		case err != nil:
		case resuming:
			// A run with a saved position lost its pod, which a person
			// may need to look at, so this create also posts an Event.
			o.notePodRecreated(play, fmt.Sprintf("created playback pod %s on player %s at %s, because the run had no pod",
				pod.Metadata.Name, player.Metadata.Name, startName(o.savedPosition(play))), true)
		default:
			logLine(o.log, "play %s: created playback pod %s on player %s at %s, because the play is new",
				key, pod.Metadata.Name, player.Metadata.Name, startName(o.savedPosition(play)))
		}
		return pod, !resuming, err
	}
	if err != nil {
		return nil, false, err
	}
	if running.Metadata.deleting() {
		// The replacement uses the same name, so nothing can be created
		// until this pod is gone. Its status does not describe the run
		// either, because mpv exits with 4 on the SIGTERM of the delete,
		// which makes the pod Failed. The pod watch wakes the pass that
		// creates the replacement.
		return nil, false, nil
	}
	// A live pod with this name is the run's pod, whoever created it, so
	// no replacement is owed.
	delete(o.replacements, key)
	if keepExisting {
		return running, false, nil
	}
	if running.Status.Phase == podFailed {
		// The pod is Failed when mpv exited with a non-zero status, or
		// when the kubelet ended the pod, such as for an eviction. Both
		// cases recreate the pod. The recreate backoff keeps a file that
		// crashes at the same position from being recreated without end.
		if !o.mayResume(key) {
			return running, false, nil
		}
		pod, err := o.replace(play, player, running, claim, resolved, prefs, remotes, false,
			recreate{reason: "the pod failed" + podMessage(running), fault: true})
		return pod, false, err
	}

	claimChanged, err := o.claimDiverged(claim)
	if err != nil {
		return nil, false, err
	}
	if !claimChanged && sameRemoteSet(running, remotes) {
		return running, false, nil
	}
	changed := "its remotes"
	if claimChanged {
		changed = "its devices"
	}
	pod, err := o.replace(play, player, running, claim, resolved, prefs, remotes, claimChanged,
		recreate{reason: "a spec edit changed " + changed + " on player " + player.Metadata.Name})
	return pod, false, err
}

// viewedPlayback reports, from the view, whether ensurePlayback would do
// nothing: the pod is being deleted, or it has not failed and has the
// claim and the Remotes that the current Player produces. When the bool
// is false, the reconcile reads the API server and runs ensurePlayback.
// The branches must match those of ensurePlayback.
func (o *operator) viewedPlayback(play *Play, claim *ResourceClaim, remotes []boundRemote, keepExisting bool) (*Pod, bool) {
	pod, err := o.view.Pod(play.Metadata.Namespace, podName(play.Metadata.Name))
	if err != nil {
		return nil, false
	}
	if pod.Metadata.deleting() {
		return nil, true
	}
	if pod.Status.Phase == podFailed {
		return nil, false
	}
	if keepExisting {
		return pod, true
	}
	current, err := o.view.ResourceClaim(claim.Metadata.Namespace, claim.Metadata.Name)
	if err != nil {
		return nil, false
	}
	same, err := sameClaimSpec(current.Spec, claim.Spec)
	if err != nil || !same || !sameRemoteSet(pod, remotes) {
		return nil, false
	}
	return pod, true
}

func startName(position string) string {
	if position == "" {
		return "the start"
	}
	return position
}

func podMessage(pod *Pod) string {
	if pod.Status.Message == "" {
		return ""
	}
	return ": " + pod.Status.Message
}

// createPodAtSavedPosition creates the playback pod with mpv starting at the
// position that savedPosition returns.
func (o *operator) createPodAtSavedPosition(play *Play, player *Player, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote) (*Pod, error) {
	resume := *play
	resume.Spec.Start = o.savedPosition(play)
	return o.createPod(&resume, player, claim, resolved, prefs, remotes)
}

// createPod creates one playback pod and logs which audio formats mpv
// passes through. When the create returns 409, because another pass or
// another copy of the operator created the pod first, it reads that pod
// back.
func (o *operator) createPod(play *Play, player *Player, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	passthrough := o.passthroughFor(player)
	created, err := CreatePod(o.client, o.resources.apply(buildPod(play, claim, resolved, o.image, o.sidecarImage,
		o.displayImage, o.busAddress, o.topicBase, remotes, prefs, passthrough.codecs, o.playerVerbose)))
	if err == nil {
		o.logPassthrough(play, player, passthrough)
	}
	if errors.Is(err, apiclient.ErrConflict) {
		return GetPod(o.client, namespace, podName(name))
	}
	if err != nil {
		return nil, err
	}
	return created, nil
}

// savedPosition returns the position that a new pod starts at: the
// run's resume point, or spec.start for a run that never reported a
// position.
func (o *operator) savedPosition(play *Play) string {
	if position, ok := o.resumePoint(play); ok {
		return position
	}
	return play.Spec.Start
}

// resumePoint returns the position that a run reached, from the bus
// report first and the Play's status second, and whether it found one.
func (o *operator) resumePoint(play *Play) (string, bool) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if report := o.reports.latestFor(namespace, name); report != nil && report.Position != "" {
		return report.Position, true
	}
	if play.Status.Position != "" {
		return play.Status.Position, true
	}
	return "", false
}

// backoffState holds one run's recreate count, when it last recreated,
// and the earliest time it may recreate again.
type backoffState struct {
	count int
	last  time.Time
	next  time.Time
}

// The recreate backoff follows the kubelet's CrashLoopBackOff: the
// first recreate is immediate, and the wait doubles after each one from
// the base up to the cap. A run with no recreate for the reset window
// starts the count again.
const (
	recreateBackoffBase  = 10 * time.Second
	recreateBackoffCap   = 5 * time.Minute
	recreateBackoffReset = 10 * time.Minute
)

// backoffNoteThreshold is the recreate count from which a Failed run's
// status shows the backoff note in place of the pod's message.
const backoffNoteThreshold = 2

// mayResume reports whether a run may create its pod now. When it may,
// it advances the backoff and schedules a wake at the next deadline, so
// a refused run is retried by that wake and not at the next tick.
func (o *operator) mayResume(key string) bool {
	now := time.Now()
	state := o.recreateBackoff[key]
	if !state.last.IsZero() && now.Sub(state.last) > recreateBackoffReset {
		state = backoffState{}
	}
	if now.Before(state.next) {
		return false
	}
	state.count++
	state.last = now
	state.next = now.Add(backoffDelay(state.count))
	o.recreateBackoff[key] = state
	o.requeueAfter(time.Until(state.next))
	return true
}

// backoffDelay returns the wait after the count-th recreate. The loop
// stops at the cap, so a long run of failures cannot overflow the
// duration.
func backoffDelay(count int) time.Duration {
	delay := recreateBackoffBase
	for range count - 1 {
		delay *= 2
		if delay >= recreateBackoffCap {
			return recreateBackoffCap
		}
	}
	return delay
}

// backoffNote returns the status message for a run that keeps failing,
// and whether the count reached backoffNoteThreshold.
func (o *operator) backoffNote(key string) (string, bool) {
	if o.recreateBackoff[key].count < backoffNoteThreshold {
		return "", false
	}
	return "the playback pod keeps failing, so the operator recreates it after a growing delay", true
}

// requeueAfter schedules one wake after delay. The wake channel has a
// buffer of one, so it merges with a wake that is already queued.
func (o *operator) requeueAfter(delay time.Duration) {
	if o.wake == nil {
		return
	}
	time.AfterFunc(delay, func() { signalWake(o.wake) })
}

// clearPanel clears one Player's retained panel desire, and records the
// Player in panelClears when the bus did not queue the clear.
func (o *operator) clearPanel(key, namespace, name string) {
	if o.bus.Publish(playerPanelTopic(o.topicBase, namespace, name), nil, true) {
		delete(o.panelClears, key)
		return
	}
	if o.panelClears == nil {
		o.panelClears = map[string]bool{}
	}
	o.panelClears[key] = true
}
