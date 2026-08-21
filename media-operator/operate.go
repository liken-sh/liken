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
// a Play's status. The bridge publishes a live position to the bus every
// second, but a status write wakes the operator's own plays watch, so
// writing the position every second would spin the loop and the API
// server. A pause, an item change, or a phase change writes at once; a
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
	fmt.Printf("media.liken.sh: operating %d plays and %d remotes over %s\n",
		len(plays.Items), len(remotes.Items), busAddress)
	go watchPlays(client, plays.Metadata.ResourceVersion, wake)
	go watchRemotes(client, remotes.Metadata.ResourceVersion, wake)

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

	// A Remote that will not resolve fails the Play only while there is
	// still no pod. Once the pod exists, its container set is fixed and
	// no edit to a Remote or a Keymap can reach this run, so a Keymap
	// broken mid-film must not fail the film.
	remotes, remoteErr := gatherRemotes(o.client, namespace, playerName(play))
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
	// The events topic each bound remote publishes on is what the bridge
	// sidecar subscribes to. The operator fills it here, because the
	// topic base lives with the operator.
	for index := range remotes {
		remotes[index].EventsTopic = remoteEventsTopic(o.topicBase, namespace, remotes[index].Name)
	}

	claim := buildClaim(play, player)
	if err := ensureClaim(o.client, claim); err != nil {
		return err
	}
	pod, err := o.ensurePod(play, claim, resolved, remotes)
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

// ensureClaim creates the claim once and never updates it. A Play's
// spec is immutable, and the Player it names is read at the start of
// the run, so the claim a run starts with is the claim it keeps: a
// Player edited mid-run changes the next Play, not this one.
//
// A 409 on the create is success, because it means another pass, or
// another copy of this operator, created the same claim first.
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

// ensurePod creates the pod once per Play and never rebuilds it. The
// pod holds no credential and reports over the bus, so an operator that
// restarted reads a running Play's position back from the broker's
// retained status and adopts nothing from the pod.
//
// A 409 on the create means another pass, or another copy of this
// operator, created the pod first, so the pod is read back and kept.
func (o *operator) ensurePod(play *Play, claim *ResourceClaim, resolved resolution, remotes []boundRemote) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	pod, err := GetPod(o.client, namespace, podName(name))
	if err == nil {
		return pod, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	created, err := CreatePod(o.client, buildPod(play, claim, resolved, o.image, o.busAddress, o.topicBase, remotes))
	if errors.Is(err, ErrConflict) {
		return GetPod(o.client, namespace, podName(name))
	}
	if err != nil {
		return nil, err
	}
	return created, nil
}
