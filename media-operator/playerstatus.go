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
