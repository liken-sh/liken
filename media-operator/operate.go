package main

// The operator's loop has the shape liken's own operators use:
// level-triggered, woken by a watch, with a ticker as the backstop,
// and a reconcile before the first event ever arrives.
//
// A pass reads the whole collection instead of acting on the object
// an event carried. The event is only a wake. Every pass derives
// every status from what the API server holds right now, so a lost
// event costs at most one backstop tick, a reordered burst collapses
// into one pass, and a restarted operator starts correct with no
// replay.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

// The operator's own environment: the three images it stamps into
// pods, the broker, and the topic base. The Deployment sets each image,
// because an image is a release decision a pod cannot discover, and
// only MEDIA_TOPIC_BASE has a default.
const (
	playerImageVariable = "PLAYER_IMAGE"

	// IDLE_IMAGE names the client that draws the idle screen. It is
	// what an idle container runs where no tier states
	// spec.idle.image, so a household that states nothing gets the
	// client this release ships.
	idleImageVariable = "IDLE_IMAGE"

	// SIDECAR_IMAGE names the image that carries this binary alone,
	// which every sidecar container runs. It holds none of the player
	// image's mpv, drivers, or fonts, because a sidecar decodes
	// nothing.
	sidecarImageVariable = "SIDECAR_IMAGE"

	// IDLE_DISPLAY_CLASS names the cluster's display-draw DeviceClass, the
	// shareable draw companion a Player's idle pod claims. The class is
	// cluster policy, so the Deployment sets it and the operator reads it
	// here. An unset value turns the idle screen off, so reconcileIdle
	// creates no idle claim and no idle pod.
	idleDisplayClassVariable = "IDLE_DISPLAY_CLASS"
)

// backstopInterval is how often the loop reconciles with nothing to
// prompt it. The tick is what recovers a lost watch event, a pod that
// changed phase, and a Player that appeared after its Play.
const backstopInterval = 10 * time.Second

// positionWriteInterval bounds how often a bare position advance reaches
// a Play's status. The command sidecar publishes a live position to the
// bus every second, but a status write wakes the operator's own plays
// watch, so writing the position every second would spin the loop and
// the API server. A pause, an item change, or a phase change writes at
// once; a
// position that advanced alone waits this interval, and the bus carries
// the live value in between.
//
// The backstop tick is what drives a bare position write, because a
// position advance wakes nothing on its own. So this interval sits below
// backstopInterval on purpose: a write stamps a moment after the tick
// that made it, so an interval equal to the tick would miss the next tick
// by that moment and write every second tick, at twice the period. Two
// seconds of headroom absorbs that skew, so a steadily playing film
// writes on every tick.
const positionWriteInterval = 8 * time.Second

// playReclaimGrace is how long a deleted Play's retained status and
// availability stay on the bus before the operator clears them. The
// command sidecar clears its own status on a clean exit but marks the
// availability offline and never clears it, and an unclean exit leaves
// both, so without this a deleted Play's gravestone would sit on the
// broker forever. The grace is short but not zero: a subscriber that
// reads the topic in the moment after the delete still sees the run's
// final state, and then the retained topics go empty. It is a variable so
// a test drives the reclaim without waiting minutes.
var playReclaimGrace = 2 * time.Minute

// volumeSeedGrace is how long the pass waits after a broker session
// begins before it seeds any unit's level. The broker delivers the
// retained levels within milliseconds of the subscribe, and the wait
// covers that delivery, so the first pass reads a desk that already
// holds what the broker holds. A pass that seeded inside the window
// would publish unity over a level a person had set. It is a variable
// so a test seeds without waiting.
var volumeSeedGrace = 2 * time.Second

// defaultTTLSecondsAfterFinished is how long a Finished Play stands when
// its spec sets no ttlSecondsAfterFinished. Five minutes is the default
// continue-watching window: while the Play stands, kubectl get plays still
// answers what just played and where it stopped, and the record goes when
// the Play does. A library app sets the field on each Play it creates when
// its continue-watching feature reads a different window.
const defaultTTLSecondsAfterFinished = 300

// operator holds what every pass needs. The report desk and the bus
// are fields rather than globals so a test builds an operator around a
// desk it can inspect.
type operator struct {
	client *Client
	image  string
	// idleImage is the client an idle container runs where no tier
	// states spec.idle.image. It is a release decision, so it arrives
	// in the environment beside the player image.
	idleImage string
	// sidecarImage is the image every sidecar container runs, the
	// binary alone. It is a release decision, so it arrives in the
	// environment beside the player image.
	sidecarImage string
	busAddress   string
	topicBase    string
	// idleDisplayClass is the display-draw DeviceClass a Player's idle pod
	// claims. An empty value turns the idle screen off, so reconcileIdle
	// builds nothing.
	idleDisplayClass string
	bus              *Bus
	reports          *reports
	// focus is the desk for the retained focus mark, built on the same
	// wake as the report desk: a cycle request on the bus wakes the pass
	// that arbitrates it.
	focus *focusDesk

	// peripherals is the desk for the bluetooth-operator's Peripherals and
	// for the Peripheral each Remote's claim allocated. The pass fills it
	// from the API and reads it when it builds a Player's bus status. The
	// peripherals watch is the wake, so a controller that connects,
	// disconnects, or reports a new charge reaches the pass that
	// republishes that status.
	peripherals *peripheralDesk

	// codes is the desk for each controller's declared code set, built
	// on the same wake: a code set that changed wakes the pass that
	// rewrites the Remote status the gap appears on.
	codes *codesDesk

	// panels is the desk for each unit's panel desire, built on
	// the same wake: a desire that changed wakes the pass that writes
	// the override onto the screen's Display.
	panels *panelDesk

	// panelOverrides holds the override this operator last
	// applied per unit, so a pass writes the Display only when the
	// desire changed, and a deleted Player's dark panel still has a
	// screen to lift. Only the pass goroutine touches it.
	panelOverrides map[string]panelOverride

	// panelFaults holds the last panel fault reported per unit,
	// so a screen with no Display logs once and not once a pass. Only
	// the pass goroutine touches it.
	panelFaults map[string]string

	// volumes is the desk for each unit's level. Unlike the desks
	// above, it wakes no pass, because the level folds into no status.
	// The pass reads it for one question alone: whether the broker
	// already holds a level for a unit. It seeds only where the desk
	// holds none.
	volumes *volumeDesk

	// volumeSeedAfter is when the pass may seed again. A fresh bus
	// session's retained messages arrive on their own goroutine moments
	// after the subscribe, so each connect pushes the first seed out by
	// volumeSeedGrace. Only the pass goroutine touches it.
	volumeSeedAfter time.Time

	// positionWrites stamps when each run last wrote its position, so a
	// bare position advance writes no more than once per
	// positionWriteInterval. Only the pass goroutine touches it.
	positionWrites map[string]time.Time

	// keysPublished maps each Remote's keys topic to the table the
	// operator last published there. The topic is retained, so the
	// broker serves the current table to any new subscriber, and the
	// operator republishes only when the table changes. The map also
	// lets a later pass find a topic whose Remote is gone and clear its
	// retained value. Only the pass goroutine touches it.
	keysPublished map[string]string

	// playerStatusPublished maps each Player's status topic to the payload
	// the operator last published there. It is the keymap pattern applied to
	// the unit's presentable state: the topic is retained, so the operator
	// republishes only when the payload changes, and a topic whose Player no
	// longer exists has its retained value cleared. Only the pass goroutine
	// touches it.
	playerStatusPublished map[string]string

	// playReclaim stamps when the operator first saw a deleted Play whose
	// retained topics still stand, so it clears them once the grace has
	// passed. Only the pass goroutine touches it.
	playReclaim map[string]time.Time

	// recreateBackoff holds one run's recreate count and the earliest time it
	// may recreate again, so a pod that keeps failing recreates slower up to a
	// cap. Only the pass goroutine touches it.
	recreateBackoff map[string]backoffState

	// wake is the loop's own wake channel. The operator schedules one wake at
	// a backoff deadline, so a run waiting out its backoff resumes when the
	// wait ends rather than on the next backstop tick.
	wake chan<- struct{}

	// busReconnected is set on the bus goroutine when a session reaches a
	// CONNACK, and read on the pass goroutine. A fresh broker session
	// holds none of the retained state the operator owns, so the next pass
	// re-establishes it. It is atomic because the two goroutines share it
	// with no other lock between them.
	busReconnected atomic.Bool
}

func operate() {
	// Setup failures end the process on purpose. The kubelet restarts
	// the pod with backoff, and the failure shows in kubectl instead of
	// hiding in a retry loop.
	image := os.Getenv(playerImageVariable)
	if image == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the player image\n", playerImageVariable)
		os.Exit(1)
	}
	idleImage := os.Getenv(idleImageVariable)
	if idleImage == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the idle image\n", idleImageVariable)
		os.Exit(1)
	}
	sidecarImage := os.Getenv(sidecarImageVariable)
	if sidecarImage == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the sidecar image\n", sidecarImageVariable)
		os.Exit(1)
	}
	busAddress := os.Getenv(busAddressVariable)
	if busAddress == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the broker\n", busAddressVariable)
		os.Exit(1)
	}
	topicBase := os.Getenv(topicBaseVariable)
	if topicBase == "" {
		topicBase = defaultTopicBase
	}
	// The idle display class is optional. An unset value turns the idle
	// screen off, so the operator runs with no idle pods rather than
	// exiting the way a missing image or broker does.
	idleDisplayClass := os.Getenv(idleDisplayClassVariable)

	client, err := InClusterClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config: %v\n", err)
		os.Exit(1)
	}

	// One wake channel serves the two watches and the bus handler,
	// because a wake carries no information beyond "read the collection
	// again".
	wake := make(chan struct{}, 1)
	desk := newReports(wake)
	focusDesk := newFocusDesk(wake)
	codesDesk := newCodesDesk(wake)
	panels := newPanelDesk(wake)
	media := &operator{
		client:                client,
		image:                 image,
		idleImage:             idleImage,
		sidecarImage:          sidecarImage,
		busAddress:            busAddress,
		topicBase:             topicBase,
		idleDisplayClass:      idleDisplayClass,
		reports:               desk,
		focus:                 focusDesk,
		peripherals:           newPeripheralDesk(),
		codes:                 codesDesk,
		panels:                panels,
		panelOverrides:        map[string]panelOverride{},
		panelFaults:           map[string]string{},
		volumes:               newVolumeDesk(),
		positionWrites:        map[string]time.Time{},
		keysPublished:         map[string]string{},
		playerStatusPublished: map[string]string{},
		playReclaim:           map[string]time.Time{},
		recreateBackoff:       map[string]backoffState{},
		wake:                  wake,
	}

	// onConnect marks that a fresh broker session began, so the next pass
	// re-establishes the retained state the operator owns. The broker holds
	// none of it on a new session, whether the operator or the broker
	// restarted, so without this the keymaps and focus marks would stay
	// missing until a person edited one.
	onConnect := func(bus *Bus) {
		media.busReconnected.Store(true)
		poke(wake)
	}
	// The bus handler is the only path the control plane takes a report or
	// a focus signal.
	media.bus = newBus(busAddress, "media-operator", nil, onConnect, media.handleBusMessage)
	media.bus.Subscribe(playStatusFilter(topicBase))
	media.bus.Subscribe(playAvailabilityFilter(topicBase))
	media.bus.Subscribe(remoteFocusFilter(topicBase))
	media.bus.Subscribe(remoteFocusCycleFilter(topicBase))
	media.bus.Subscribe(remoteAvailabilityFilter(topicBase))
	media.bus.Subscribe(remoteCodesFilter(topicBase))
	media.bus.Subscribe(playerPanelFilter(topicBase))
	media.bus.Subscribe(playerVolumeFilter(topicBase))
	go media.bus.Run(context.Background())

	// The first lists do two jobs: they prove the operator can read the
	// collections, and their resourceVersions are where the watches
	// start.
	plays, err := ListPlays(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing plays: %v\n", err)
		os.Exit(1)
	}
	remotes, err := ListAllRemotes(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing remotes: %v\n", err)
		os.Exit(1)
	}
	players, err := ListPlayers(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing players: %v\n", err)
		os.Exit(1)
	}
	keymaps, err := ListKeymaps(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing keymaps: %v\n", err)
		os.Exit(1)
	}
	prefs, err := ListMediaPreferences(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing media preferences: %v\n", err)
		os.Exit(1)
	}
	pods, err := ListPlaybackPods(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing playback pods: %v\n", err)
		os.Exit(1)
	}
	// The Peripherals are listed on the same terms as the other
	// collections. A cluster whose bluetooth-operator serves no Peripheral
	// answers an error here, and the operator ends rather than run with no
	// record of any controller's link.
	peripherals, err := ListPeripherals(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing peripherals: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("media.liken.sh: operating %d plays and %d remotes over %s\n",
		len(plays.Items), len(remotes.Items), busAddress)
	go watchPlays(client, plays.Metadata.ResourceVersion, wake)
	go watchRemotes(client, remotes.Metadata.ResourceVersion, wake)
	go watchPlayers(client, players.Metadata.ResourceVersion, wake)
	go watchKeymaps(client, keymaps.Metadata.ResourceVersion, wake)
	go watchMediaPreferences(client, prefs.Metadata.ResourceVersion, wake)
	go watchPods(client, pods.Metadata.ResourceVersion, wake)
	go watchPeripherals(client, peripherals.Metadata.ResourceVersion, wake)

	ticker := time.NewTicker(backstopInterval)
	for {
		media.pass()
		select {
		case <-wake:
		case <-ticker.C:
		}
	}
}

// pass runs one reconcile over every Play and every Remote in the
// cluster. A failure on one object is reported and the pass continues,
// because one broken run must not freeze every other unit's status.
func (o *operator) pass() {
	if o.busReconnected.Swap(false) {
		o.reestablishRetained()
	}
	list, err := ListPlays(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing plays: %v\n", err)
		return
	}
	// Read the household default once per pass. A missing default is not an error,
	// and a read that fails skips the tier this pass.
	defaults, err := GetMediaPreferences(o.client, mediaPreferencesName)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			fmt.Fprintf(os.Stderr, "reading media preferences: %v\n", err)
		}
		defaults = nil
	}
	live := make(map[string]bool, len(list.Items))
	for index := range list.Items {
		play := &list.Items[index]
		live[runKey(play.Metadata.Namespace, play.Metadata.Name)] = true
		// A Finished Play is over, so the pass retires it in place of
		// reconciling it. A Failed Play is not over here, because the
		// reconcile below resumes it, so only a Finished Play is retired.
		if finishedPhase(play.Status.Phase) {
			if err := o.retire(play); err != nil {
				fmt.Fprintf(os.Stderr, "retiring play %s/%s: %v\n",
					play.Metadata.Namespace, play.Metadata.Name, err)
			}
			continue
		}
		if err := o.reconcile(play, defaults); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling play %s/%s: %v\n",
				play.Metadata.Namespace, play.Metadata.Name, err)
		}
	}
	o.reports.retain(live)
	for key := range o.positionWrites {
		if !live[key] {
			delete(o.positionWrites, key)
		}
	}
	for key := range o.recreateBackoff {
		if !live[key] {
			delete(o.recreateBackoff, key)
		}
	}
	o.reclaimPlays(live)
	// The Remotes are listed once for the whole pass. The Peripherals are
	// folded in here, before any Player status is written, because a
	// unit's bus status carries each controller's link and charge. The same
	// list reconciles the standing pods at the end of the pass. A list that
	// fails skips both, so no desk shrinks to a collection the operator
	// could not read.
	remotes, remotesErr := ListAllRemotes(o.client)
	var remoteClaims map[string]claimRead
	if remotesErr != nil {
		fmt.Fprintf(os.Stderr, "listing remotes: %v\n", remotesErr)
	} else {
		remoteClaims = o.observePeripherals(remotes.Items)
	}
	players, err := ListPlayers(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing players: %v\n", err)
	} else {
		// The idle clock reads the household zone, one setting per cluster from
		// the default MediaPreferences alone, so the pass resolves it once and
		// hands it to every Player's idle pod.
		var zone string
		// The idle default is read once from the same MediaPreferences,
		// and each Player's own block overrides it field by field.
		var defaultIdle *IdlePolicy
		if defaults != nil {
			zone = defaults.Spec.TimeZone
			defaultIdle = defaults.Spec.Idle
		}
		// Focus settles before the Player statuses, because a remote part
		// of a unit's bus status carries whether the mark names that unit.
		// Arbitrating after would publish the previous pass's mark and
		// leave the indicator one pass behind.
		o.reconcileFocus(players.Items)
		o.reconcilePlayers(players.Items, list.Items, zone, defaultIdle)
	}
	if remotesErr == nil {
		// The Keymaps are read first, because each Remote's table is its
		// Keymap folded over the base, and the pass compiles one table per
		// Remote.
		o.reconcileRemotes(remotes.Items, remoteClaims, o.loadKeymaps())
	}
}

// handleBusMessage folds one bus message into the report desk or the
// focus desk by its topic. An empty payload on a status or availability
// topic is a cleared retained value, not a live signal, so it is ignored:
// the operator publishes that empty value itself when it reclaims a
// deleted Play, and reading its own clear back as an offline signal would
// mark the run seen again and reclaim it forever.
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
			o.reports.fold(namespace, name, report)
		case playAvailabilityKind:
			o.reports.availability(namespace, name, string(payload) == availabilityOnline)
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
	// An availability with an empty payload is a cleared retained value and
	// not a live signal, so it is ignored the way an empty plays message
	// is. The signal gates the declared codes, because a retained document
	// outlives the pod that wrote it.
	if namespace, name, ok := parseRemoteAvailabilityTopic(o.topicBase, topic); ok {
		if len(payload) == 0 {
			return
		}
		o.codes.setAvailability(controllerKey(namespace, name),
			string(payload) == availabilityOnline)
		return
	}
	// An empty payload on the codes topic is the pod's own clear,
	// published when the controller's nodes vanish, so the desk drops
	// the document rather than keep a stale one.
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
	// An empty payload is a cleared retained value and not a live
	// signal, so the desk holds what it had.
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
	// The operator reads the level only to learn that one stands, so
	// the seed skips the unit. An empty payload is a cleared retained
	// value, and it changes nothing on the desk.
	if namespace, name, ok := parsePlayerVolumeTopic(o.topicBase, topic); ok {
		if len(payload) == 0 {
			return
		}
		state, decoded := parseVolumeState(payload)
		if !decoded {
			return
		}
		o.volumes.setState(playerKey(namespace, name), state)
		return
	}
}

// reestablishRetained rewrites everything the operator publishes retained
// after a fresh broker session, because the broker holds none of it. It
// clears the record of published keymaps and Player statuses, so
// reconcileKeymaps and reconcilePlayers write every one of them again this
// pass, and it republishes each focus mark, so a controller keeps the
// Player it drives across a broker or operator restart. The command sidecar and
// the standing remote pod re-establish their own retained topics from
// their own connect, so those need no help here.
func (o *operator) reestablishRetained() {
	o.keysPublished = map[string]string{}
	o.playerStatusPublished = map[string]string{}
	// The levels are the one retained state this rewrite skips. The
	// sidecars and the broker hold them, not the operator, so the pass
	// waits out volumeSeedGrace and then seeds only the units nothing
	// answered for.
	o.volumeSeedAfter = time.Now().Add(volumeSeedGrace)
	for key, player := range o.focus.snapshot() {
		o.publishFocus(key, player)
	}
}

// retire gives a Finished Play its two endings: the playback objects go at
// once, and the Play itself goes when the window on its spec has passed.
//
// The pod holds nothing worth keeping after the film ends. The final
// position is on the Play's status and the ending already traveled the bus,
// so the pod and its claim go on the first pass that reads the Finished
// phase. Only a Finished Play is retired. A Failed pod stands, because its
// log is the evidence a person debugs from and the reconcile resumes the
// run.
//
// The finishedAt stamp is what tells a Play this pass just retired from one
// retired earlier, so the two deletes run once per Play and the passes that
// follow only read the clock. Both deletes read an absent object as success,
// so a retry after a failed status write deletes nothing twice.
//
// The deletion follows the pass cadence, so a Play goes at most one
// backstopInterval, ten seconds, after its window ends.
func (o *operator) retire(play *Play) error {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	// A Play a person already deleted is on its way out, and the garbage
	// collector takes the pod and the claim with it through their
	// ownerReferences. Leave it alone, the way reconcileStanding leaves a
	// standing object with a deletionTimestamp alone.
	if play.Metadata.DeletionTimestamp != "" {
		return nil
	}
	now := time.Now()
	finished, stamped := finishedTime(play, now)
	if !stamped {
		if err := DeletePod(o.client, namespace, podName(name)); err != nil {
			return err
		}
		if err := DeleteResourceClaim(o.client, namespace, claimName(name)); err != nil {
			return err
		}
	}
	// Deleting the Play is the whole teardown. The ownerReferences collect
	// anything the two deletes above did not, and reclaimPlays clears the
	// retained topics on the same terms as a Play a person deleted: the next
	// pass reads the Play gone, so the run counts as stale and its grace
	// begins.
	if now.Sub(finished) >= playTTL(play) {
		return DeletePlay(o.client, namespace, name)
	}
	if stamped {
		return nil
	}
	status := play.Status
	status.FinishedAt = finished.UTC().Format(time.RFC3339)
	return writePlayStatus(o.client, play, status)
}

// playTTL is how long this Play stands after it finishes: the seconds its
// spec states, or defaultTTLSecondsAfterFinished when it states none. The
// pointer is what tells a spec that asked for zero from a spec that asked
// for nothing, and zero deletes the Play on the pass that sees it finished.
func playTTL(play *Play) time.Duration {
	if play.Spec.TTLSecondsAfterFinished == nil {
		return defaultTTLSecondsAfterFinished * time.Second
	}
	return time.Duration(*play.Spec.TTLSecondsAfterFinished) * time.Second
}

// finishedTime reads the moment this run finished from the status, and
// reports whether the status carried a stamp at all. A Play with no stamp
// finished as far as this operator can tell right now, so its window starts
// at the time the caller passes in. A stamp this operator cannot parse
// counts as no stamp, so a garbled value starts the window over rather than
// holding a Finished Play forever.
func finishedTime(play *Play, now time.Time) (time.Time, bool) {
	stamped, err := time.Parse(time.RFC3339, play.Status.FinishedAt)
	if err != nil {
		return now, false
	}
	return stamped, true
}

// reclaimPlays clears the retained topics a deleted Play leaves on the
// bus. The desk reports the runs it has seen a message for that no longer
// exist; the operator holds each for playReclaimGrace, so a subscriber
// that reads just after the delete still sees the final state, then it
// clears the status and the availability with an empty retained publish
// and forgets the run. A run that reappears before the grace passes, a
// Play recreated under the same name, drops its timer and is not cleared.
func (o *operator) reclaimPlays(live map[string]bool) {
	now := time.Now()
	for _, key := range o.reports.stale(live) {
		if _, tracked := o.playReclaim[key]; !tracked {
			o.playReclaim[key] = now
		}
		if now.Sub(o.playReclaim[key]) < playReclaimGrace {
			continue
		}
		namespace, name := splitRunKey(key)
		o.bus.Publish(playStatusTopic(o.topicBase, namespace, name), nil, true)
		o.bus.Publish(playAvailabilityTopic(o.topicBase, namespace, name), nil, true)
		o.reports.forget(key)
		delete(o.playReclaim, key)
	}
	for key := range o.playReclaim {
		if live[key] {
			delete(o.playReclaim, key)
		}
	}
}

// loadKeymaps reads every Keymap once per pass and indexes it by
// name. The pass holds them rather than compiling here, because a
// table belongs to a Remote: it is the base folded with that Remote's
// Keymap, and a Remote with no Keymap still needs the base.
func (o *operator) loadKeymaps() map[string]*Keymap {
	keymaps := map[string]*Keymap{}
	list, err := ListKeymaps(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing keymaps: %v\n", err)
		return keymaps
	}
	for index := range list.Items {
		keymap := &list.Items[index]
		keymaps[keymap.Metadata.Name] = keymap
	}
	return keymaps
}

// publishKeys compiles one Remote's table and publishes it retained
// on that Remote's keys topic. The topic is retained, so an unchanged
// table is not republished and a new subscriber reads the current one
// from the broker. A Keymap that does not compile publishes nothing
// and leaves the last good table in place, so a broken edit does not
// stop a controller. The table this returns is what the status
// reports the gap against.
func (o *operator) publishKeys(remote *Remote, keymaps map[string]*Keymap, present map[string]bool) []compiledBinding {
	topic := remoteKeysTopic(o.topicBase, remote.Metadata.Namespace, remote.Metadata.Name)
	present[topic] = true
	table, err := compileTable(keymaps[remote.Spec.Keymap])
	if err != nil {
		fmt.Fprintf(os.Stderr, "compiling the key table for remote %s/%s: %v\n",
			remote.Metadata.Namespace, remote.Metadata.Name, err)
		return nil
	}
	payload, err := json.Marshal(table)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshaling the key table for remote %s/%s: %v\n",
			remote.Metadata.Namespace, remote.Metadata.Name, err)
		return nil
	}
	if o.keysPublished[topic] != string(payload) {
		o.bus.Publish(topic, payload, true)
		o.keysPublished[topic] = string(payload)
	}
	return table
}

// reconcilePlayers writes every Player's status, publishes the same state
// to the bus, and ensures every Player's standing idle pod, from the
// Players and Plays the pass already read. A Player's status is
// relational, derived from the Plays that name it, so the pass lists the
// Players once and hands the slice here and to reconcileFocus rather than
// listing twice. The idle pod stands whether or not a Play runs, so its
// reconcile is per Player and not derived from the Plays.
//
// The Kubernetes status and the bus status carry the same activity from
// the same derivation. The API server holds what exists and what is
// desired, and the bus carries the presentable now, which is why the idle
// screen reads a topic and holds no API credentials.
func (o *operator) reconcilePlayers(players []Player, plays []Play, timeZone string, defaultIdle *IdlePolicy) {
	published := make(map[string]bool, len(players))
	live := make(map[string]bool, len(players))
	// The units whose idle screen something draws. A unit under
	// media.liken.sh/none leaves this set, and the panel desks treat
	// it the way they treat a deleted unit: its desire is dropped and
	// a dark panel it left behind takes a lift.
	drawn := make(map[string]bool, len(players))
	// One screens for the pass, so the driver's ResourceSlices
	// are listed at most once however many units the cluster holds.
	lookup := newScreens(o.client)
	for index := range players {
		player := &players[index]
		key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
		live[key] = true
		desired := derivePlayerStatus(player, plays, o.reports)
		idle := resolveIdle(player.Spec.Idle, defaultIdle, o.idleImage)
		// The panel state is what the screen's Display last
		// observed, so the status reports the hardware and not what
		// the media layer asked for.
		if idle.Controller == idleControllerNone {
			// Nothing draws, so no desire is settled and the panel
			// reports nothing. The retained desire is cleared once,
			// while the desk still holds it, so a restarted operator
			// does not read the idle client pod's last word back off
			// the broker after that pod is gone.
			if o.panels.stateFor(key) != "" {
				o.bus.Publish(playerPanelTopic(o.topicBase, player.Metadata.Namespace, player.Metadata.Name), nil, true)
			}
		} else {
			drawn[key] = true
			desired.Panel = o.reconcilePanel(player, key, lookup, idle.OffMode)
		}
		// The idle block is what a delegate reads to draw this
		// unit's screen, so it goes on the status before the write.
		desired.Idle = deriveIdleStatus(player, idle.Controller, o.busAddress, o.topicBase,
			o.idleClaimFor(player), idle, gatherIdleRemotes(player, o.topicBase))
		o.seedVolume(player, key)
		// The status goes out before the re-present, and the order is
		// what the returning idle screen draws from. The client
		// animates the return only when it reads the Idle status before
		// the re-present, because the status is what says the film is
		// over. The client subscribes to the retained status topic
		// itself and reads the re-present off the commands topic, so
		// the order the operator writes is the order the client reads.
		published[o.publishPlayerStatus(player, desired, plays)] = true
		// A Play that ends destroys the film's surface, and the idle
		// client's surface under it stays hidden. Weston's kiosk-shell
		// reveals a lower surface only along a code path gated on a seat,
		// and liken's compositor has none, so the idle clock does not return
		// on its own. On the edge from any active state to idle, the
		// operator publishes a re-present. The client reads it off the
		// commands topic, maps a fresh surface, and kiosk reveals that
		// one. The edge reads the stored status against the
		// derived one, so the re-present goes out once as the Player settles
		// to idle and not on every backstop pass while it stays idle. A
		// status write that fails leaves the stored status unchanged, so the
		// next pass reads the edge again and publishes a second re-present.
		// The client maps a fresh surface over one that is already up, and
		// the screen shows the same clock.
		if player.Status.Activity != playerIdle && desired.Activity == playerIdle {
			o.publishRePresent(player.Metadata.Namespace, player.Metadata.Name)
		}
		if err := writePlayerStatus(o.client, player, desired); err != nil {
			fmt.Fprintf(os.Stderr, "writing player %s/%s status: %v\n",
				player.Metadata.Namespace, player.Metadata.Name, err)
		}
		if err := o.reconcileIdle(player, timeZone, defaultIdle); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling idle for player %s/%s: %v\n",
				player.Metadata.Namespace, player.Metadata.Name, err)
		}
	}
	// The panel desk shrinks to the units something draws, the way
	// the codes desk shrinks to its Remotes.
	o.panels.retain(drawn)
	// The overrides shrink the same way, and a unit dropped
	// while its panel was dark takes a lift on the way out.
	o.retainPanels(drawn)
	// The volume desk shrinks the same way. The retained level itself
	// stays on the broker, so a Player recreated under the same name
	// keeps the level the room was left at.
	o.volumes.retain(live)
	// A topic whose Player no longer exists has its retained value cleared
	// with an empty publish, so a deleted Player leaves no unit on the bus
	// for a subscriber to draw.
	for topic := range o.playerStatusPublished {
		if !published[topic] {
			o.bus.Publish(topic, nil, true)
			delete(o.playerStatusPublished, topic)
		}
	}
}

// publishPlayerStatus writes one unit's presentable state to its retained
// status topic and returns the topic it wrote, so the caller records which
// topics this pass still owns. The topic is retained, so republishing an
// unchanged payload is churn a new subscriber does not need: it reads the
// current value from the broker. The publish happens only when the payload
// differs from the last one this operator wrote, which is what keeps the
// backstop tick off the bus while a unit sits idle.
func (o *operator) publishPlayerStatus(player *Player, desired PlayerStatus, plays []Play) string {
	topic := playerStatusTopic(o.topicBase, player.Metadata.Namespace, player.Metadata.Name)
	payload, err := json.Marshal(derivePlayerBusStatus(player, desired, plays, o.peripherals, o.focus))
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshaling player %s/%s bus status: %v\n",
			player.Metadata.Namespace, player.Metadata.Name, err)
		return topic
	}
	if o.playerStatusPublished[topic] == string(payload) {
		return topic
	}
	o.bus.Publish(topic, payload, true)
	o.playerStatusPublished[topic] = string(payload)
	return topic
}

// seedVolume writes unity to a unit whose level the broker holds
// nothing for, so the state is always readable off the bus and no
// reader carries a default. It never writes over a level that
// stands: the desk answers that, and a duplicate seed from a racing
// pass writes the same value, so the race settles itself. A Player
// with no sinks is not seeded, because a unit with nothing to hear
// has no level to mean anything.
func (o *operator) seedVolume(player *Player, key string) {
	if len(player.Spec.Sinks) == 0 || time.Now().Before(o.volumeSeedAfter) {
		return
	}
	if _, held := o.volumes.stateFor(key); held {
		return
	}
	o.publishVolume(player.Metadata.Namespace, player.Metadata.Name, defaultVolumeState())
}

// writeThroughVolume lays a Play's declared starting state over the
// unit's current one and publishes the result, retained, before the
// pod exists. The override becomes the Player's state, and everything
// after it is the ordinary path. It runs on the creating pass alone:
// a republish on a later pass of the same run would write the Play's
// level over every press a person made during the film.
func (o *operator) writeThroughVolume(play *Play) {
	if play.Spec.Volume == nil {
		return
	}
	namespace, name := play.Metadata.Namespace, playerName(play)
	key := playerKey(namespace, name)
	current, held := o.volumes.stateFor(key)
	if !held {
		current = defaultVolumeState()
	}
	o.publishVolume(namespace, name, current.mergedWith(play.Spec.Volume))
}

// publishVolume writes one unit's level to its topic, retained, and
// records it on the desk at once. Recording the operator's own write
// keeps the next pass from seeding the same unit again before the
// broker echoes the message back.
func (o *operator) publishVolume(namespace, name string, state volumeState) {
	payload, err := marshalVolumeState(state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publishing player %s/%s volume: %v\n", namespace, name, err)
		return
	}
	o.bus.Publish(playerVolumeTopic(o.topicBase, namespace, name), payload, true)
	o.volumes.setState(playerKey(namespace, name), state)
}

// volumeFor is the level the pod's mpv starts at, and whether the
// broker holds one at all. A unit nothing has answered for carries
// no level onto the pod, so mpv keeps its own default and the
// subscription sets the level a moment later.
func (o *operator) volumeFor(play *Play) (volumeState, bool) {
	return o.volumes.stateFor(playerKey(play.Metadata.Namespace, playerName(play)))
}

// publishRePresent publishes the re-present to a Player's commands
// topic, not retained, because a re-present is an event and not a
// state. The idle screen client subscribes to that topic and maps a
// fresh surface, so the clock shows again after a Play ends.
func (o *operator) publishRePresent(namespace, name string) {
	payload, err := json.Marshal(mediaCommand{Action: actionRePresent})
	if err != nil {
		return
	}
	o.bus.Publish(playerCommandsTopic(o.topicBase, namespace, name), payload, false)
}

// reconcileRemotes reconciles a standing pod for every Remote in the
// cluster and writes each Remote's status. A Remote's pod runs whether or
// not anything plays, so the pass reads the whole collection and hands it
// here rather than deriving it from the Plays. It runs after reconcileFocus
// on the same pass, so the status reports the mark this pass settled.
func (o *operator) reconcileRemotes(remotes []Remote, claims map[string]claimRead, keymaps map[string]*Keymap) {
	live := make(map[string]bool, len(remotes))
	present := make(map[string]bool, len(remotes))
	for index := range remotes {
		remote := &remotes[index]
		key := controllerKey(remote.Metadata.Namespace, remote.Metadata.Name)
		live[key] = true
		if err := o.reconcileRemote(remote, claims[key]); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling remote %s/%s: %v\n",
				remote.Metadata.Namespace, remote.Metadata.Name, err)
		}
		table := o.publishKeys(remote, keymaps, present)
		// The one status this pass builds carries the mark and the gap
		// together, because two writers of one status would alternate
		// and each write wakes the watch.
		desired := RemoteStatus{
			Player:     o.focus.markFor(key),
			Peripheral: o.peripherals.peripheralFor(key),
		}
		// A table that did not compile reports no gap, because the gap is
		// what the live table leaves unbound, and this pass has no live
		// table to subtract.
		if declared, held := o.codes.codesFor(key); held && table != nil {
			desired.Unbound = unboundCodes(declared, table)
		}
		if err := writeRemoteStatus(o.client, remote, desired); err != nil {
			fmt.Fprintf(os.Stderr, "writing remote %s/%s status: %v\n",
				remote.Metadata.Namespace, remote.Metadata.Name, err)
		}
	}
	// The codes desk holds a key per controller it has heard from, so it
	// shrinks to the Remotes the cluster still holds.
	o.codes.retain(live)
	// A Remote that is gone leaves its retained table behind, so the
	// pass clears the topic with an empty payload. The map is the record
	// of what this operator wrote, so it is the one place that knows
	// which topics to clear.
	for topic := range o.keysPublished {
		if !present[topic] {
			o.bus.Publish(topic, nil, true)
			delete(o.keysPublished, topic)
		}
	}
}

// writeRemoteStatus follows the same two rules as the Play's and the
// Player's status writers: an unchanged status is not written, and a
// conflict earns one retry. The operator watches Remotes, so a needless
// write would wake the loop that just wrote it, a pass per pass forever.
func writeRemoteStatus(c *Client, remote *Remote, desired RemoteStatus) error {
	same, err := sameRemoteStatus(remote.Status, desired)
	if err != nil {
		return err
	}
	if same {
		return nil
	}

	remote.Status = desired
	_, err = PutRemoteStatus(c, remote)
	if !errors.Is(err, ErrConflict) {
		return err
	}

	// A conflict means the Remote changed between the read and the write.
	// The fresh copy carries the resourceVersion the API server accepts,
	// and the desired status still reports the mark this pass settled, so
	// it goes on unchanged.
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

// sameRemoteStatus compares the marshaled forms, the way the Play's
// and the Player's writers compare, because the marshaled form is what
// the API server stores and what omitempty decides.
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

// reconcile takes one Play from the Player it names to the status it
// earns. The order matters: nothing is created until the Player is read
// and every URI resolves, so a Play that can never run leaves no
// half-built objects behind.
func (o *operator) reconcile(play *Play, defaults *MediaPreferences) error {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	// The default tier's spec, or nil when the cluster has no default.
	var defaultSpec *MediaPreferencesSpec
	if defaults != nil {
		defaultSpec = &defaults.Spec
	}
	if playerName(play) == "" {
		return writePlayStatus(o.client, play, PlayStatus{
			Phase:   phaseFailed,
			Message: "the Play names no Player",
		})
	}

	player, err := GetPlayer(o.client, namespace, playerName(play))
	if errors.Is(err, ErrNotFound) {
		prefs := resolvePreferences(&play.Spec, nil, defaultSpec)
		return writePlayStatus(o.client, play, derivePlayStatus(play, nil, nil, nil, nil, prefs))
	}
	if err != nil {
		return err
	}
	prefs := resolvePreferences(&play.Spec, &player.Spec, defaultSpec)

	resolved, resolveErr := resolvePlay(play.Spec.Items)
	if resolveErr != nil {
		return writePlayStatus(o.client, play, derivePlayStatus(play, player, resolveErr, nil, nil, prefs))
	}

	// A missing Remote fails the Play only while there is still no pod.
	// Once the pod exists, its container set is fixed and no edit to the
	// Player's remotes can reach this run, so a Remote deleted mid-film
	// must not fail the film. A Keymap never reaches this gather: it is
	// compiled and published on the bus per Remote by reconcileRemotes,
	// and a broken Keymap edit leaves the last good table in place
	// instead.
	remotes, remoteErr := gatherRemotes(o.client, player)
	if remoteErr != nil {
		_, err := GetPod(o.client, namespace, podName(name))
		if errors.Is(err, ErrNotFound) {
			return writePlayStatus(o.client, play, derivePlayStatus(play, player, remoteErr, nil, nil, prefs))
		}
		if err != nil {
			return err
		}
		remotes = nil
	}
	// The operator fills each remote's two topics here, because the
	// topic base lives with the operator and not the gather. Both carry
	// the Remote's namespace and name.
	for index := range remotes {
		remotes[index].EventsTopic = remoteEventsTopic(o.topicBase, namespace, remotes[index].Name)
		remotes[index].FocusTopic = remoteFocusTopic(o.topicBase, namespace, remotes[index].Name)
	}

	claim := buildClaim(play, player)
	pod, fresh, err := o.ensurePlayback(play, claim, resolved, prefs, remotes, remoteErr != nil)
	if err != nil {
		return err
	}
	// A genuinely new Play steals its controllers, the most-recent-steals
	// default. A graceful recreate resumes the same Play, so it steals
	// nothing and the mark stays where a person left it.
	if fresh && len(remotes) > 0 {
		o.stealFocus(play, remotes)
	}
	status := derivePlayStatus(play, player, nil, pod, o.reports.latestFor(namespace, name), prefs)
	// A run that keeps failing reads the backoff note in place of the pod's
	// own failure message, but only once the recreates repeat and only while
	// the pod is Failed, so a run that recovers reads a clean status.
	if status.Phase == phaseFailed {
		if note, backing := o.backoffNote(runKey(namespace, name)); backing {
			status.Message = note
		}
	}
	return o.writePlay(play, status)
}

// writePlay writes a Play's status through the position throttle. A
// change in phase, pause, item, or message writes at once. A change that
// is only a position advance waits positionWriteInterval, so the resource
// keeps a coarse clock while the bus carries the live one, and a position
// write does not wake the operator's own watch a second later.
func (o *operator) writePlay(play *Play, desired PlayStatus) error {
	key := runKey(play.Metadata.Namespace, play.Metadata.Name)
	if onlyPositionChanged(play.Status, desired) &&
		time.Since(o.positionWrites[key]) < positionWriteInterval {
		return nil
	}
	if err := writePlayStatus(o.client, play, desired); err != nil {
		return err
	}
	o.positionWrites[key] = time.Now()
	return nil
}

// ensurePlayback brings the running pod into line with the pod the
// current Player would produce. A Play with no pod yet gets its claim
// and its pod. A gather that failed keeps an existing pod as it is,
// because the container set is fixed once it runs and a Keymap broken
// mid-film must not fail the film. A running pod its Player reshaped is
// recreated at the film's place, and a running pod its Player left
// alone is kept.
//
// The bool reports a genuinely new pod, true only in the no-existing-pod
// branch. A fresh Play uses it to steal its controllers, and a recreate,
// which returns false, leaves the focus mark alone.
func (o *operator) ensurePlayback(play *Play, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote, keepExisting bool) (*Pod, bool, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	key := runKey(namespace, name)
	running, err := GetPod(o.client, namespace, podName(name))
	if errors.Is(err, ErrNotFound) {
		// Nothing answers for the pod: a taint evicted it, its node was lost,
		// or the Play never had one. A run with a saved place resumes without
		// stealing its controllers back, and a run with no saved place starts
		// at spec.start and steals them. Both wait out the recreate backoff.
		_, resuming := o.resumePoint(play)
		if !o.mayResume(key) {
			return nil, false, nil
		}
		if err := ensureClaim(o.client, claim); err != nil {
			return nil, false, err
		}
		// A run that starts here for the first time is the one pass a
		// Play's declared level is written through on. A run that
		// resumes skips it, the way the recreate paths below do,
		// because a run that already played must keep the level a
		// person set while it played. The claim carries the speaker
		// gate: a Play against a unit with no sinks writes no level
		// through, the same gate the seed reads off the Player.
		if !resuming && claimHasSink(claim) {
			o.writeThroughVolume(play)
		}
		pod, err := o.createPodAtStash(play, claim, resolved, prefs, remotes)
		return pod, !resuming, err
	}
	if err != nil {
		return nil, false, err
	}
	if keepExisting {
		return running, false, nil
	}
	if running.Status.Phase == podFailed {
		// mpv exited non-zero. Recreate the pod at the film's place the way a
		// Job restarts a failed pod, bounded by the recreate backoff so a
		// file that crashes at the same place does not recreate without end.
		if !o.mayResume(key) {
			return running, false, nil
		}
		pod, err := o.recreateForResume(play, claim, resolved, prefs, remotes)
		return pod, false, err
	}

	claimChanged, err := o.claimDiverged(claim)
	if err != nil {
		return nil, false, err
	}
	desired := buildPod(play, claim, resolved, o.image, o.sidecarImage, o.busAddress, o.topicBase, remotes, prefs)
	if !claimChanged && sameRemoteSet(running, desired) {
		return running, false, nil
	}
	pod, err := o.recreate(play, claim, resolved, prefs, remotes, claimChanged)
	return pod, false, err
}

// recreate replaces a running pod its Player reshaped and keeps the
// film's place. It deletes the pod, deletes and recreates the claim only
// when the claim itself diverged, and creates the replacement so mpv
// starts where the film was. The image is already on the machine, so the
// film resumes within about a second.
func (o *operator) recreate(play *Play, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote, claimChanged bool) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if err := DeletePod(o.client, namespace, podName(name)); err != nil {
		return nil, err
	}
	if claimChanged {
		if err := DeleteResourceClaim(o.client, namespace, claimName(name)); err != nil {
			return nil, err
		}
		if err := ensureClaim(o.client, claim); err != nil {
			return nil, err
		}
	}
	return o.createPodAtStash(play, claim, resolved, prefs, remotes)
}

// recreateForResume replaces a pod that failed and keeps the film's place.
// The claim outlives the pod, so this ensures the claim, deletes the dead
// pod, and creates the replacement at the film's place.
func (o *operator) recreateForResume(play *Play, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if err := ensureClaim(o.client, claim); err != nil {
		return nil, err
	}
	if err := DeletePod(o.client, namespace, podName(name)); err != nil {
		return nil, err
	}
	return o.createPodAtStash(play, claim, resolved, prefs, remotes)
}

// createPodAtStash creates the playback pod with mpv's start set to the
// film's saved place. A genuinely new run has no saved place, so the start
// falls back to spec.start.
func (o *operator) createPodAtStash(play *Play, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote) (*Pod, error) {
	resume := *play
	resume.Spec.Start = o.stashedPosition(play)
	// The copy carries the unit's current level, not the override the
	// Play declared, so the pod builder reads one field and never
	// reads the bus. It is the same move the saved place above makes:
	// the pod is built from the Play as the run stands right now.
	resume.Spec.Volume = nil
	if volume, held := o.volumeFor(play); held {
		resume.Spec.Volume = volume.asPlayVolume()
	}
	return o.createPod(&resume, claim, resolved, prefs, remotes)
}

// createPod creates one playback pod and reads it back on a 409,
// because another pass, or another copy of this operator, created the
// pod first.
func (o *operator) createPod(play *Play, claim *ResourceClaim, resolved resolution, prefs resolvedPreferences, remotes []boundRemote) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	created, err := CreatePod(o.client, buildPod(play, claim, resolved, o.image, o.sidecarImage, o.busAddress, o.topicBase, remotes, prefs))
	if errors.Is(err, ErrConflict) {
		return GetPod(o.client, namespace, podName(name))
	}
	if err != nil {
		return nil, err
	}
	return created, nil
}

// stashedPosition reads the film's place for a recreate. It prefers the
// run's own resume point, and falls back to the Play's spec.start for a
// run that never reported a position, which a startup edit reshaped or a
// brand-new Play that just started.
func (o *operator) stashedPosition(play *Play) string {
	if position, ok := o.resumePoint(play); ok {
		return position
	}
	return play.Spec.Start
}

// resumePoint reads the place a run reached, from the retained bus status
// first and the Play's status second, and reports whether it found one. A
// spec.start is not a resume point, so a run that never reported a position
// starts fresh rather than resumes.
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

// backoffState holds one run's recreate count and the two times that bound
// its rate: last is when it last recreated, and next is the earliest it may
// recreate again.
type backoffState struct {
	count int
	last  time.Time
	next  time.Time
}

// The recreate backoff follows the kubelet's CrashLoopBackOff: the first
// recreate is immediate, and each recreate after it doubles the wait from
// the base to the cap. A run that stays up for the reset window starts the
// count over. They are variables so a test drives them in milliseconds.
var (
	recreateBackoffBase  = 10 * time.Second
	recreateBackoffCap   = 5 * time.Minute
	recreateBackoffReset = 10 * time.Minute
)

// backoffNoteThreshold is how many recreates a run reaches before its status
// reads the repeated-failure note in place of the pod's own message.
const backoffNoteThreshold = 2

// mayResume reports whether a run may recreate its dead pod now, and
// advances the backoff when it may. On a yes it schedules one wake at the
// deadline, so the loop resumes when the wait ends rather than on a backstop
// tick. On a no a wake from the last yes is already pending, so the caller
// waits for it.
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

// backoffDelay returns the wait after the count-th recreate: the base
// doubled once per recreate, up to the cap. The doubling runs in a loop and
// stops at the cap, so a long run of failures never overflows the duration.
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

// backoffNote returns the status message for a run that keeps failing, and
// reports whether the count reached the threshold. The caller shows it only
// while the pod is Failed.
func (o *operator) backoffNote(key string) (string, bool) {
	if o.recreateBackoff[key].count < backoffNoteThreshold {
		return "", false
	}
	return "the playback pod keeps failing; the operator is retrying with a growing delay", true
}

// requeueAfter schedules one wake at delay from now, the requeue-after
// idiom: the timer fires once and pokes the shared wake, which coalesces
// with any queued wake, so a run waiting out its backoff wakes when the wait
// ends.
func (o *operator) requeueAfter(delay time.Duration) {
	if o.wake == nil {
		return
	}
	time.AfterFunc(delay, func() { poke(o.wake) })
}
