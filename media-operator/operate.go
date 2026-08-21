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
	"slices"
	"strings"
	"time"
)

// The operator's own environment: the player image it stamps into
// every playback pod, the broker every pod connects to, and the base
// every topic extends. The Deployment sets PLAYER_IMAGE and
// MEDIA_BUS_ADDRESS, because neither is discoverable from inside a pod:
// the image is a release decision, and a pod cannot read the address of
// the broker in front of it. MEDIA_TOPIC_BASE has a default, because a
// cluster that runs one bus needs no policy for the base.
const (
	playerImageVariable = "PLAYER_IMAGE"
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

// operator holds what every pass needs. The report desk and the bus
// are fields rather than globals so a test builds an operator around a
// desk it can inspect.
type operator struct {
	client     *Client
	image      string
	busAddress string
	topicBase  string
	bus        *Bus
	reports    *reports

	// positionWrites stamps when each run last wrote its position, so a
	// bare position advance writes no more than once per
	// positionWriteInterval. Only the pass goroutine touches it.
	positionWrites map[string]time.Time

	// keymapTopics is the set of keymap topics the operator has published
	// a compiled table on. It lets a later pass find a topic whose Keymap
	// no longer exists and clear its retained value. Only the pass
	// goroutine touches it.
	keymapTopics map[string]bool
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
	busAddress := os.Getenv(busAddressVariable)
	if busAddress == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the broker\n", busAddressVariable)
		os.Exit(1)
	}
	topicBase := os.Getenv(topicBaseVariable)
	if topicBase == "" {
		topicBase = defaultTopicBase
	}

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
	media := &operator{
		client:         client,
		image:          image,
		busAddress:     busAddress,
		topicBase:      topicBase,
		reports:        desk,
		positionWrites: map[string]time.Time{},
		keymapTopics:   map[string]bool{},
	}

	// The bus handler is the only path a report takes to the control
	// plane. It maps each message's topic back to a Play, folds a status
	// report into the desk, and drops a Play's report when its
	// availability goes offline.
	media.bus = newBus(busAddress, "media-operator", nil, nil, func(topic string, payload []byte) {
		namespace, name, kind, ok := parsePlayTopic(topicBase, topic)
		if !ok {
			return
		}
		switch kind {
		case playStatusKind:
			var report playReport
			if err := json.Unmarshal(payload, &report); err != nil {
				return
			}
			desk.fold(namespace, name, report)
		case playAvailabilityKind:
			desk.availability(namespace, name, string(payload) == availabilityOnline)
		}
	})
	media.bus.Subscribe(playStatusFilter(topicBase))
	media.bus.Subscribe(playAvailabilityFilter(topicBase))
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
	fmt.Printf("media.liken.sh: operating %d plays and %d remotes over %s\n",
		len(plays.Items), len(remotes.Items), busAddress)
	go watchPlays(client, plays.Metadata.ResourceVersion, wake)
	go watchRemotes(client, remotes.Metadata.ResourceVersion, wake)
	go watchPlayers(client, players.Metadata.ResourceVersion, wake)
	go watchKeymaps(client, keymaps.Metadata.ResourceVersion, wake)

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
// because one namespace's broken run must not freeze every other room's
// status.
func (o *operator) pass() {
	list, err := ListPlays(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing plays: %v\n", err)
		return
	}
	live := make(map[string]bool, len(list.Items))
	for index := range list.Items {
		play := &list.Items[index]
		live[runKey(play.Metadata.Namespace, play.Metadata.Name)] = true
		// A Play in a terminal phase is done. Its pod and claims stay
		// until the Play is deleted, and the garbage collector takes
		// them then, through the ownerReferences they carry.
		if terminalPhase(play.Status.Phase) {
			continue
		}
		if err := o.reconcile(play); err != nil {
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
	o.reconcilePlayers(list.Items)
	o.reconcileRemotes()
	o.reconcileKeymaps()
}

// reconcileKeymaps compiles every Keymap and publishes it to its keymap
// topic, retained, so a translator reads the current table the instant
// it subscribes and a Keymap edit reaches a running film with no pod
// restart. A Keymap that does not compile publishes nothing and leaves
// the last-good retained value in place, so a broken edit does not empty
// a running translation. A topic whose Keymap no longer exists has its
// retained value cleared with an empty publish, so a deleted Keymap
// leaves nothing behind on the bus.
func (o *operator) reconcileKeymaps() {
	list, err := ListKeymaps(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing keymaps: %v\n", err)
		return
	}
	present := make(map[string]bool, len(list.Items))
	for index := range list.Items {
		keymap := &list.Items[index]
		topic := keymapTopic(o.topicBase, keymap.Metadata.Name)
		bindings, err := compileKeymap(keymap)
		if err != nil {
			fmt.Fprintf(os.Stderr, "compiling keymap %s: %v\n", keymap.Metadata.Name, err)
			continue
		}
		payload, err := json.Marshal(bindings)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshaling keymap %s: %v\n", keymap.Metadata.Name, err)
			continue
		}
		o.bus.Publish(topic, payload, true)
		o.keymapTopics[topic] = true
		present[topic] = true
	}
	for topic := range o.keymapTopics {
		if !present[topic] {
			o.bus.Publish(topic, nil, true)
			delete(o.keymapTopics, topic)
		}
	}
}

// reconcilePlayers writes every Player's status from the same Plays the
// pass just read. A Player's status is relational, so it is a second
// read and a write on the same pass rather than a loop of its own:
// nothing watches Players, and the derivation needs every Play, which
// the pass already holds.
func (o *operator) reconcilePlayers(plays []Play) {
	players, err := ListPlayers(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing players: %v\n", err)
		return
	}
	for index := range players.Items {
		player := &players.Items[index]
		desired := derivePlayerStatus(player, plays)
		if err := writePlayerStatus(o.client, player, desired); err != nil {
			fmt.Fprintf(os.Stderr, "writing player %s/%s status: %v\n",
				player.Metadata.Namespace, player.Metadata.Name, err)
		}
	}
}

// reconcileRemotes reconciles a standing pod for every Remote in the
// cluster. A Remote's pod runs whether or not anything plays, so this
// pass is its own read of the whole collection and not derived from the
// Plays.
func (o *operator) reconcileRemotes() {
	list, err := ListAllRemotes(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing remotes: %v\n", err)
		return
	}
	for index := range list.Items {
		remote := &list.Items[index]
		if err := o.reconcileRemote(remote); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling remote %s/%s: %v\n",
				remote.Metadata.Namespace, remote.Metadata.Name, err)
		}
	}
}

// reconcile takes one Play from the Player it names to the status it
// earns. The order matters: nothing is created until the Player is read
// and every URI resolves, so a Play that can never run leaves no
// half-built objects behind.
func (o *operator) reconcile(play *Play) error {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if playerName(play) == "" {
		return writePlayStatus(o.client, play, PlayStatus{
			Phase:   phaseFailed,
			Message: "the Play names no Player",
		})
	}

	player, err := GetPlayer(o.client, namespace, playerName(play))
	if errors.Is(err, ErrNotFound) {
		return writePlayStatus(o.client, play, derivePlayStatus(play, nil, nil, nil, nil))
	}
	if err != nil {
		return err
	}

	resolved, resolveErr := resolveURIs(play.Spec.URIs)
	if resolveErr != nil {
		return writePlayStatus(o.client, play, derivePlayStatus(play, player, resolveErr, nil, nil))
	}

	// A missing Remote fails the Play only while there is still no pod.
	// Once the pod exists, its container set is fixed and no edit to the
	// Player's remotes can reach this run, so a Remote deleted mid-film
	// must not fail the film. A Keymap never reaches this gather: it is
	// compiled and published on the bus by reconcileKeymaps, and a broken
	// Keymap edit leaves the last-good table in place instead.
	remotes, remoteErr := gatherRemotes(o.client, player)
	if remoteErr != nil {
		_, err := GetPod(o.client, namespace, podName(name))
		if errors.Is(err, ErrNotFound) {
			return writePlayStatus(o.client, play, derivePlayStatus(play, player, remoteErr, nil, nil))
		}
		if err != nil {
			return err
		}
		remotes = nil
	}
	// The operator fills each remote's three topics here, because the
	// topic base lives with the operator and not the gather. The events
	// and focus topics carry the Remote's namespace and name; the keymap
	// topic carries the Keymap name alone, because a Keymap is
	// cluster-scoped.
	for index := range remotes {
		remotes[index].EventsTopic = remoteEventsTopic(o.topicBase, namespace, remotes[index].Name)
		remotes[index].KeymapTopic = keymapTopic(o.topicBase, remotes[index].Keymap)
		remotes[index].FocusTopic = remoteFocusTopic(o.topicBase, namespace, remotes[index].Name)
	}

	claim := buildClaim(play, player)
	pod, err := o.ensurePlayback(play, claim, resolved, remotes, remoteErr != nil)
	if err != nil {
		return err
	}
	return o.writePlay(play,
		derivePlayStatus(play, player, nil, pod, o.reports.latestFor(namespace, name)))
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

// ensureClaim creates the playback claim when none exists and keeps an
// existing one. A 409 on the create is success, because another pass,
// or another copy of this operator, created the same claim first. The
// graceful recreate deletes a claim a Player reshaped before it calls
// this, so ensureClaim never updates a claim in place.
func ensureClaim(c *Client, claim *ResourceClaim) error {
	_, err := GetResourceClaim(c, claim.Metadata.Namespace, claim.Metadata.Name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if _, err := CreateResourceClaim(c, claim); err != nil && !errors.Is(err, ErrConflict) {
		return err
	}
	return nil
}

// ensurePlayback brings the running pod into line with the pod the
// current Player would produce. A Play with no pod yet gets its claim
// and its pod. A gather that failed keeps an existing pod as it is,
// because the container set is fixed once it runs and a Keymap broken
// mid-film must not fail the film. A running pod its Player reshaped is
// recreated at the film's place, and a running pod its Player left
// alone is kept.
func (o *operator) ensurePlayback(play *Play, claim *ResourceClaim, resolved resolution, remotes []boundRemote, keepExisting bool) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	running, err := GetPod(o.client, namespace, podName(name))
	if errors.Is(err, ErrNotFound) {
		if err := ensureClaim(o.client, claim); err != nil {
			return nil, err
		}
		return o.createPod(play, claim, resolved, remotes)
	}
	if err != nil {
		return nil, err
	}
	if keepExisting {
		return running, nil
	}

	claimChanged, err := o.claimDiverged(claim)
	if err != nil {
		return nil, err
	}
	desired := buildPod(play, claim, resolved, o.image, o.busAddress, o.topicBase, remotes)
	if !claimChanged && sameRemoteSet(running, desired) {
		return running, nil
	}
	return o.recreate(play, claim, resolved, remotes, claimChanged)
}

// recreate replaces a running pod its Player reshaped and keeps the
// film's place. It reads the position, deletes the pod, deletes and
// recreates the claim only when the claim itself diverged, and creates
// the replacement so mpv starts where the film was. The image is
// already on the machine, so the film resumes within about a second.
func (o *operator) recreate(play *Play, claim *ResourceClaim, resolved resolution, remotes []boundRemote, claimChanged bool) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	start := o.stashedPosition(play)
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
	resume := *play
	resume.Spec.Start = start
	return o.createPod(&resume, claim, resolved, remotes)
}

// createPod creates one playback pod and reads it back on a 409,
// because another pass, or another copy of this operator, created the
// pod first.
func (o *operator) createPod(play *Play, claim *ResourceClaim, resolved resolution, remotes []boundRemote) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	created, err := CreatePod(o.client, buildPod(play, claim, resolved, o.image, o.busAddress, o.topicBase, remotes))
	if errors.Is(err, ErrConflict) {
		return GetPod(o.client, namespace, podName(name))
	}
	if err != nil {
		return nil, err
	}
	return created, nil
}

// stashedPosition reads the film's place for a recreate. It prefers
// the retained bus status the operator holds, then the position last
// written to the Play, then the Play's own spec.start for a pod that
// never reported a position before a startup edit reshaped it.
func (o *operator) stashedPosition(play *Play) string {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if report := o.reports.latestFor(namespace, name); report != nil && report.Position != "" {
		return report.Position
	}
	if play.Status.Position != "" {
		return play.Status.Position
	}
	return play.Spec.Start
}

// claimDiverged reports whether the claim the current Player produces
// differs from the one in the cluster. An absent claim counts as
// diverged, so the recreate creates it.
func (o *operator) claimDiverged(desired *ResourceClaim) (bool, error) {
	current, err := GetResourceClaim(o.client, desired.Metadata.Namespace, desired.Metadata.Name)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	same, err := sameClaimSpec(current.Spec, desired.Spec)
	if err != nil {
		return false, err
	}
	return !same, nil
}

// sameClaimSpec compares two claim specs by their marshaled form. It
// reads only the fields this operator's own types model, because the
// client drops every field it does not know when it reads the stored
// claim, so a field the API server adds does not read as a change.
func sameClaimSpec(current, desired ResourceClaimSpec) (bool, error) {
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

// sameRemoteSet reports whether two pods carry the same translator
// sidecars, by the events topics they subscribe to. It reads no keymap,
// so a Keymap edit is bus state and not a shape change and recreates no
// pod. Only what the Player controls, the claim and the set of
// controllers, reshapes a running film.
func sameRemoteSet(current, desired *Pod) bool {
	return slices.Equal(podRemoteTopics(current), podRemoteTopics(desired))
}

// podRemoteTopics reads the events topics the translator sidecars
// subscribe to, in order, from each translator container's environment.
// Adding or removing a controller changes this set, and the recreate
// follows.
func podRemoteTopics(pod *Pod) []string {
	var topics []string
	for _, container := range pod.Spec.InitContainers {
		if !strings.HasPrefix(container.Name, translatorContainer("")) {
			continue
		}
		for _, variable := range container.Env {
			if variable.Name == remoteEventsVariable {
				topics = append(topics, variable.Value)
			}
		}
	}
	return topics
}
