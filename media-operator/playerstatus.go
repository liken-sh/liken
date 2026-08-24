package main

// A Player's status is relational: it comes from the Plays that name
// the Player, not from the Player itself. So the operator writes it
// on the same pass that reconciles the Plays, from the same list it
// already read. A Player that no Play names is idle, and one that a
// Play runs on is playing.

import (
	"encoding/json"
	"errors"
)

// derivePlayerStatus reads the whole pass's Plays and returns what
// this Player is doing. A Play running on the Player wins over one
// still starting, because a person wants the run in progress named
// first; among equals, the earliest name is chosen, so the answer is
// the same on every pass. A Play in a terminal phase is over and
// names nothing.
func derivePlayerStatus(player *Player, plays []Play) PlayerStatus {
	var running, starting string
	for index := range plays {
		play := &plays[index]
		if play.Metadata.Namespace != player.Metadata.Namespace {
			continue
		}
		if playerName(play) != player.Metadata.Name {
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

// playerBusStatus is the presentable state of one unit, the whole of what
// the operator says to the idle screen. The Kubernetes status stays the
// record of what the Player is doing; this is the same fact plus the words
// a screen draws, so the display formats one message and resolves nothing.
//
// The Player it belongs to is named by the topic, not by the body, the way
// a Play's report is.
type playerBusStatus struct {
	DisplayName string               `json:"displayName"`
	Activity    string               `json:"activity"`
	Play        *playerBusPlay       `json:"play,omitempty"`
	Components  []playerBusComponent `json:"components,omitempty"`
}

// playerBusPlay names the Play that runs or starts on the unit. Name is
// the object a person finds with kubectl, and Title is the one line the
// screen draws.
type playerBusPlay struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// playerBusComponent is one part of the unit: its friendly name, its kind,
// and its presence when the part has any. Connected is a pointer so a part
// with no live state carries no key at all, so the display draws it at full
// brightness always.
type playerBusComponent struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Connected *bool  `json:"connected,omitempty"`
}

// The three kinds of part the idle screen draws. The kind is the display's
// whole vocabulary for a part, so it says what to draw and not which
// DeviceClass the part came from.
const (
	displayComponent = "display"
	sinkComponent    = "sink"
	remoteComponent  = "remote"
)

// derivePlayerBusStatus builds the message the idle screen reads from the
// Player, the activity the same pass derived, and the presence the desk
// folded. The parts come from the spec in the order the screen shows them:
// the display first, then each sink, then each remote. Only a remote
// carries presence, because a wired screen and its speakers report none,
// and a controller a person carries comes and goes.
func derivePlayerBusStatus(player *Player, activity PlayerStatus, plays []Play, presence *presenceDesk) playerBusStatus {
	status := playerBusStatus{
		DisplayName: idlePlayerName(player),
		Activity:    activity.Activity,
	}
	if play := findPlay(plays, player.Metadata.Namespace, activity.Play); play != nil {
		status.Play = &playerBusPlay{Name: play.Metadata.Name, Title: playTitle(play)}
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
		component := playerBusComponent{
			Name: remoteDisplayName(remote),
			Kind: remoteComponent,
		}
		if connected, held := presence.presenceFor(controllerKey(player.Metadata.Namespace, remote.Name)); held {
			component.Connected = &connected
		}
		status.Components = append(status.Components, component)
	}
	return status
}

// findPlay returns the Play the activity names, or nil when the activity
// names none. An idle Player names no Play, so its status carries no play
// block and the screen draws the clock alone.
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

// playTitle resolves the one line the idle screen draws for a Play. The
// first item's Presentation is what the library that fed liken said the
// item is, so a Series names the show a person put on, a Title names a
// film, and a Play whose first item declares neither falls back to the
// Play's own name. The operator resolves it here so the display formats one
// string and reads no Presentation of its own.
func playTitle(play *Play) string {
	if len(play.Spec.Items) > 0 && play.Spec.Items[0].Presentation != nil {
		presentation := play.Spec.Items[0].Presentation
		if presentation.Series != "" {
			return presentation.Series
		}
		if presentation.Title != "" {
			return presentation.Title
		}
	}
	return play.Metadata.Name
}

// writePlayerStatus follows the same two rules as the Play's status
// writer: an unchanged status is not written, and a conflict earns
// one retry. Nothing watches Players, so a needless write wakes no
// loop, but the skip still spares the API server a write per settled
// Player every pass.
func writePlayerStatus(c *Client, player *Player, desired PlayerStatus) error {
	same, err := samePlayerStatus(player.Status, desired)
	if err != nil {
		return err
	}
	if same {
		return nil
	}

	player.Status = desired
	_, err = PutPlayerStatus(c, player)
	if !errors.Is(err, ErrConflict) {
		return err
	}

	// A conflict means the Player changed between the read and the
	// write. The fresh copy carries the resourceVersion the API
	// server accepts, and the desired status still describes the
	// same Plays, so it goes on unchanged.
	fresh, err := GetPlayer(c, player.Metadata.Namespace, player.Metadata.Name)
	if err != nil {
		return err
	}
	same, err = samePlayerStatus(fresh.Status, desired)
	if err != nil || same {
		return err
	}
	fresh.Status = desired
	_, err = PutPlayerStatus(c, fresh)
	return err
}

// samePlayerStatus compares the marshaled form, because that is what
// the API server stores and what omitempty decides.
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
