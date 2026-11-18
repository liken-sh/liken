package main

// A Player's status comes from the Plays that name the Player, so the
// pass that reconciles the Plays writes it from the list it already read.

import (
	"encoding/json"
	"sync"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// publishedStatuses records the payload last published on each Player's
// retained status topic, so an unchanged payload is not published again
// and does not wake every subscriber.
//
// The reconcile pass and the bus reader both publish here. The bus
// reader folds a run's ending report into reports, then publishes Idle.
// One mutex covers the whole sequence of derive, compare, publish, and
// record. Without it, the pass could derive Playing from reports read
// before the fold, then publish that Playing after the bus reader's
// Idle. A mutex over the map alone would also let the two goroutines
// publish in one order and record in the other, so the map would not
// match what the broker retains.
//
// The zero value is ready to use, and the first pass publishes every
// topic.
type publishedStatuses struct {
	mutex    sync.Mutex
	payloads map[string]string

	// activities survives reset, because a new broker session clears the
	// retained payloads but does not change what a Player is doing.
	activities map[string]string
}

// publishDerived calls derive and publishes the payload, retained,
// unless the topic's last payload was the same. It returns the Player's
// previous activity and whether the activity changed. A topic with no
// previous activity counts as unchanged, so an operator restart logs no
// changes.
func (p *publishedStatuses) publishDerived(bus *Bus, topic string,
	derive func() (payload []byte, activity string, err error)) (string, bool, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	payload, activity, err := derive()
	if err != nil {
		return "", false, err
	}
	// Record the payload only when the bus queued it, so a publish
	// dropped on a full queue is sent again on the next pass.
	if p.payloads[topic] != string(payload) && bus.Publish(topic, payload, true) {
		if p.payloads == nil {
			p.payloads = map[string]string{}
		}
		p.payloads[topic] = string(payload)
	}
	was, known := p.activities[topic]
	if p.activities == nil {
		p.activities = map[string]string{}
	}
	p.activities[topic] = activity
	return was, known && was != activity, nil
}

func (p *publishedStatuses) payloadFor(topic string) string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.payloads[topic]
}

// reset forgets every recorded payload, so the next pass publishes each
// topic again into a new broker session, which has none of them.
func (p *publishedStatuses) reset() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.payloads = nil
}

// clear publishes an empty retained payload on every recorded topic that
// is not in owned, so a deleted Player leaves no status on the bus. A
// topic is forgotten only after the bus queues its clear, so a dropped
// clear is sent again on the next pass.
func (p *publishedStatuses) clear(bus *Bus, owned map[string]bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for topic := range p.payloads {
		if !owned[topic] && bus.Publish(topic, nil, true) {
			delete(p.payloads, topic)
		}
	}
}

// derivePlayerStatus returns what the Player is doing, from the Plays
// the pass listed. A Running Play wins over a Pending one, and within a
// phase the lowest name wins, so every pass returns the same result.
// Plays in other phases do not count.
//
// A Play that is being deleted does not count, although its phase stays
// Running until its finalizer is removed. Before it removes the
// finalizer, the operator deletes the pod, clears the Play's topics, and
// forgets the run's ending report, all while the Play is still listed.
// Counting that Play would publish Playing again, between the Idle of
// the ending and the Idle after the Play is gone.
//
// A Play whose sidecar reported the ending does not count either. Its
// pod takes seconds to terminate and its phase stays Running meanwhile,
// but the film is over, so the idle screen returns as soon as the report
// arrives.
func derivePlayerStatus(player *Player, plays []Play, ended *reports) PlayerStatus {
	var running, starting string
	for index := range plays {
		play := &plays[index]
		if play.Metadata.Namespace != player.Metadata.Namespace {
			continue
		}
		if playerName(play) != player.Metadata.Name {
			continue
		}
		if play.Metadata.deleting() || ended.endedFor(play.Metadata.Namespace, play.Metadata.Name) {
			continue
		}
		switch play.Status.Phase {
		case phaseRunning:
			if running == "" || play.Metadata.Name < running {
				running = play.Metadata.Name
			}
		case phasePending:
			if starting == "" || play.Metadata.Name < starting {
				starting = play.Metadata.Name
			}
		}
	}
	if running != "" {
		return PlayerStatus{Activity: playerPlaying, Play: running}
	}
	if starting != "" {
		return PlayerStatus{Activity: playerStarting, Play: starting}
	}
	return PlayerStatus{Activity: playerIdle}
}

// playerMetricState returns the state that the media_players gauge
// reports for a Player. A Player whose Play is still starting counts as
// idle, because playback has not begun. A playing Player counts as paused
// when its Play's status.activity says so.
func playerMetricState(namespace string, status PlayerStatus, plays []Play) string {
	if status.Activity != playerPlaying {
		return playerMetricIdle
	}
	for index := range plays {
		play := &plays[index]
		if play.Metadata.Namespace == namespace && play.Metadata.Name == status.Play {
			if play.Status.Activity == activityPaused {
				return playerMetricPaused
			}
			break
		}
	}
	return playerMetricPlaying
}

// publishPlayerStatuses publishes the bus status of each Player and
// writes nothing to the API server. The pass calls it before it
// reconciles any Play or Player, so an ending reaches the idle screen
// without waiting for the rest of the pass. reconcilePlayers publishes
// again later, and that publish is skipped when nothing changed.
func (o *operator) publishPlayerStatuses(players []Player, plays []Play) {
	for index := range players {
		player := &players[index]
		o.publishPlayerStatus(player, plays)
	}
}

// deriveIdleStatus returns the idle block of a Player's status: what an
// idle-screen controller needs to draw the screen. A delegate reads the
// status and never the spec, because the spec can leave the controller
// to MediaPreferences, and only this operator resolves it. The bus block
// is there because the broker address and the topic base are this
// operator's configuration, and a delegate has no other way to learn
// them.
//
// A nil claim means the Player drives no screen, or the cluster has no
// display-draw class, so there is no idle block. Under
// media.liken.sh/none nothing draws, so the block holds the controller
// alone.
func deriveIdleStatus(
	player *Player, controller, busAddress, topicBase string,
	claim *ResourceClaim, idle resolvedIdle, remotes []idleRemoteTopics,
) *PlayerIdleStatus {
	if claim == nil {
		return nil
	}
	if controller == idleControllerNone {
		return &PlayerIdleStatus{Controller: controller}
	}
	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	bus := &PlayerIdleBus{
		Address:       busAddress,
		StatusTopic:   playerStatusTopic(topicBase, namespace, name),
		VolumeTopic:   idleVolumeTopic(player, topicBase),
		CommandsTopic: playerCommandsTopic(topicBase, namespace, name),
		PanelTopic:    playerPanelTopic(topicBase, namespace, name),
		// spec.power applies to the screen of every Player, with or without
		// a Receiver, so every Player has a power topic.
		PowerTopic: playerPowerTopic(topicBase, namespace, name),
		Remotes:    idleBusRemotes(remotes),
	}
	return &PlayerIdleStatus{
		Controller:       controller,
		Claim:            claim.Metadata.Name,
		Requests:         claimRequests(claim),
		FadeAfterSeconds: idle.FadeAfterSeconds,
		OffAfterSeconds:  idle.OffAfterSeconds,
		Bus:              bus,
	}
}

// idleBusRemotes keeps spec.remotes order, because a focus message
// identifies a remote by its index in that list.
func idleBusRemotes(remotes []idleRemoteTopics) []PlayerIdleRemote {
	if len(remotes) == 0 {
		return nil
	}
	list := make([]PlayerIdleRemote, len(remotes))
	for index, remote := range remotes {
		list[index] = PlayerIdleRemote{Events: remote.Events, Focus: remote.Focus}
	}
	return list
}

// playerBusStatus is the message on a Player's status topic. Beyond the
// activity in the Kubernetes status, it has the names the idle screen
// shows, so the screen looks up nothing. The topic identifies the
// Player, so the body does not.
type playerBusStatus struct {
	DisplayName string               `json:"displayName"`
	Activity    string               `json:"activity"`
	Play        *playerBusPlay       `json:"play,omitempty"`
	Components  []playerBusComponent `json:"components,omitempty"`
}

// playerBusPlay is the Play that is running or starting on the Player.
// DisplayAlive copies the Play's condition, so the screen learns that
// the display crashed without a request to the API server.
type playerBusPlay struct {
	Name         string              `json:"name"`
	Title        string              `json:"title"`
	DisplayAlive *playerBusCondition `json:"displayAlive,omitempty"`
}

// playerBusCondition is one condition on the bus. Its JSON key is the
// condition type, so the body has no type field.
type playerBusCondition struct {
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

func busDisplayAlive(play *Play) *playerBusCondition {
	for _, condition := range play.Status.Conditions {
		if condition.Type != displayAliveCondition {
			continue
		}
		return &playerBusCondition{
			Status:  string(condition.Status),
			Reason:  condition.Reason,
			Message: condition.Message,
		}
	}
	return nil
}

// playerBusComponent is one part of the Player: a display, a sink, or a
// remote. The optional fields are pointers so that an absent value is
// left out of the JSON, and a remote that reports no battery level does
// not read as empty. When Connected is absent, the screen draws the part
// at full brightness. Focused is set only on the Player that the remote's
// focus mark names, so when several Players list the same controller,
// only one of them draws it as focused.
type playerBusComponent struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Connected *bool  `json:"connected,omitempty"`
	Battery   *int   `json:"battery,omitempty"`
	Focused   *bool  `json:"focused,omitempty"`
}

// The kinds of part the idle screen draws. A kind is not a DeviceClass.
const (
	displayComponent = "display"
	sinkComponent    = "sink"
	remoteComponent  = "remote"
)

// derivePlayerBusStatus lists the parts in the order the screen shows
// them: the display, then each sink, then each remote. Only a remote
// has a link state, a charge, and focus, because a wired screen and
// speakers report none of them. A remote with no Peripheral has no link
// state and no charge, but can still be focused.
func derivePlayerBusStatus(
	player *Player, activity PlayerStatus, plays []Play,
	peripherals *peripheralCache, focus *focusCache,
) playerBusStatus {
	status := playerBusStatus{
		DisplayName: idlePlayerName(player),
		Activity:    activity.Activity,
	}
	if play := findPlay(plays, player.Metadata.Namespace, activity.Play); play != nil {
		status.Play = &playerBusPlay{
			Name:         play.Metadata.Name,
			Title:        playTitle(play),
			DisplayAlive: busDisplayAlive(play),
		}
	}
	if player.Spec.Display != nil {
		status.Components = append(status.Components, playerBusComponent{
			Name: deviceDisplayName(*player.Spec.Display),
			Kind: displayComponent,
		})
	}
	for _, sink := range player.Spec.Sinks {
		status.Components = append(status.Components, playerBusComponent{
			Name: deviceDisplayName(sink),
			Kind: sinkComponent,
		})
	}
	for _, remote := range player.Spec.Remotes {
		key := controllerKey(player.Metadata.Namespace, remote.Name)
		component := playerBusComponent{
			Name: remoteDisplayName(remote),
			Kind: remoteComponent,
		}
		if name := peripherals.peripheralFor(key); name != "" {
			if connected, held := peripherals.connectedFor(name); held {
				component.Connected = &connected
			}
			component.Battery = peripherals.batteryFor(name)
		}
		if focus.markFor(key) == player.Metadata.Name {
			focused := true
			component.Focused = &focused
		}
		status.Components = append(status.Components, component)
	}
	return status
}

// findPlay returns the Play with this namespace and name, or nil when
// the name is empty or no Play matches.
func findPlay(plays []Play, namespace, name string) *Play {
	if name == "" {
		return nil
	}
	for index := range plays {
		play := &plays[index]
		if play.Metadata.Namespace == namespace && play.Metadata.Name == name {
			return play
		}
	}
	return nil
}

// playTitle returns the line the idle screen draws for a Play: the
// first item's Series, Title, or Album, in that order, or else the
// Play's name.
func playTitle(play *Play) string {
	if len(play.Spec.Items) > 0 && play.Spec.Items[0].Presentation != nil {
		presentation := play.Spec.Items[0].Presentation
		if presentation.Series != "" {
			return presentation.Series
		}
		if presentation.Title != "" {
			return presentation.Title
		}
		if presentation.Album != "" {
			return presentation.Album
		}
	}
	return play.Metadata.Name
}

// writePlayerStatus skips a status that has not changed, and retries
// once after a conflict. The Players watch wakes the pass on every
// change, including this operator's own status writes, so a write of an
// unchanged status would start another pass with nothing to do.
//
// versions records each copy the API server returns, so the next pass
// reads the Player from a copy at least as new as this write. A nil
// versions records nothing.
func writePlayerStatus(c *apiclient.Client, versions *memo.Versions, player *Player, desired PlayerStatus) error {
	_, err := writePlayerStatusFrom(c, versions, player, desired)
	return err
}

// writePlayerStatusFrom is writePlayerStatus that also returns the
// conditions that changed, so the caller can post one Event for each.
// It returns none when the write failed or was not needed.
func writePlayerStatusFrom(c *apiclient.Client, versions *memo.Versions, player *Player, desired PlayerStatus) ([]PlayerCondition, error) {
	var compared error
	var moved []PlayerCondition
	wrote, err := informer.SettleStatus(c, versions, playerPath(player.Metadata.Namespace, player.Metadata.Name), player,
		func(held *Player) bool {
			same, err := samePlayerStatus(held.Status, desired)
			if err != nil {
				compared = err
				return false
			}
			if same {
				return false
			}
			moved = transitions(held.Status.Conditions, desired.Conditions)
			held.Status = desired
			return true
		})
	if compared != nil {
		return nil, compared
	}
	if err != nil || !wrote {
		return nil, err
	}
	return moved, nil
}

// samePlayerStatus compares the JSON forms, because the API server
// stores JSON. With omitempty, a nil field and an empty one have the
// same JSON, although a Go comparison finds them different.
func samePlayerStatus(current, desired PlayerStatus) (bool, error) {
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
