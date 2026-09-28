package main

// The ending label is how a finished run reaches the compositor. The
// sidecar publishes the ending and then holds mpv alive for its exit
// grace, so there is a live surface for that half second. The label is
// what the compositor acts on: a Layout whose film region excludes
// media.liken.sh/ending stops matching the pod the moment the label
// lands, and the region's exit fade takes the film off the screen.
//
// The Play's status is not the signal. A Layout matches pods, and
// display-operator reads none of this operator's kinds, so the mark
// goes onto the pod itself.
//
// The ending also has to reach the bus as the unit's Idle state, which
// is what every screen client keys its return on, and that answer comes
// from memory. The pass that the report wakes runs only after the pass
// in flight ends, and a pass waits on the API server for each write it
// sends, with nothing bounding the worst case. A person is looking at
// the screen for the whole wait.
// The operator read both collections on its last pass, and neither the
// film's last frame nor the pass that follows it changes what those
// lists say about this unit, so the fold derives the unit's state from
// the lists it holds and publishes it at once. The pass runs behind it
// and derives the same payload, which the broker already holds, so it
// writes nothing.

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// passSnapshot holds the Plays and the Players the last pass listed. One
// mutex covers both, because the pass writes them on its own goroutine
// and the bus reader reads them on another.
//
// Each list is recorded where the pass reads it, so a list read that
// failed leaves the previous list standing rather than emptying the
// memory. The slices are copies: the pass writes a Play's finalizers and
// a Player's status onto the elements of the lists it holds, and a
// reader on the bus goroutine must not see those writes.
type passSnapshot struct {
	mutex   sync.Mutex
	plays   []Play
	players []Player
}

func (s *passSnapshot) recordPlays(plays []Play) {
	copied := append([]Play(nil), plays...)
	s.mutex.Lock()
	s.plays = copied
	s.mutex.Unlock()
}

func (s *passSnapshot) recordPlayers(players []Player) {
	copied := append([]Player(nil), players...)
	s.mutex.Lock()
	s.players = copied
	s.mutex.Unlock()
}

// lists answers both lists as the last pass read them. An operator that
// has finished no pass answers two empty lists.
func (s *passSnapshot) lists() ([]Play, []Player) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.plays, s.players
}

// answerEnding publishes one unit's presentable state the moment the
// run's sidecar reports the film is over, from the lists the last pass
// read. The bus reader calls it on its own goroutine, so it makes no API
// request and no blocking call: the publish enqueues one frame and
// returns.
//
// A snapshot that holds neither the Play nor the Player it names is an
// operator that has finished no pass, or a run created since the last
// one. Such an ending is left to the pass, which reads both collections
// and answers it a moment later.
func (o *operator) answerEnding(namespace, name string) {
	plays, players := o.snapshot.lists()
	play := findPlay(plays, namespace, name)
	if play == nil {
		return
	}
	player := findPlayer(players, namespace, playerName(play))
	if player == nil {
		return
	}
	o.publishPlayerStatus(player, plays)
}

// findPlayer returns the Player of that name in that namespace, or nil
// where the list holds none.
func findPlayer(players []Player, namespace, name string) *Player {
	for index := range players {
		player := &players[index]
		if player.Metadata.Namespace == namespace && player.Metadata.Name == name {
			return player
		}
	}
	return nil
}

// labelEnding patches the ending label onto one Play's playback pod,
// once. It runs first in the pass for that Play, ahead of the retire
// that deletes the pod, because the fade needs the label while the pod
// still draws. The ending report is what wakes that pass, so the label
// lands within milliseconds of the film's last frame.
//
// The memo holds the UID of the pod that reported the ending it
// labeled, so the ending of a new pod under the same name is labeled on
// its own. A merge patch names the pod by its name alone, and a new pod
// can hold the name before the pod store shows it, so for an ending that
// names its pod the pass reads the pod from the API server first and
// labels it only when the UID matches. Only the pass creates a playback
// pod, and the pass is where this runs, so no new pod can take the name
// between the read and the patch. A pod that has gone, or whose name
// another pod holds, is nothing to label: that pod's fade is over or
// never started, so the memo records it as done. A read or a patch that
// fails for any other reason is reported and tried again on the next
// pass.
func (o *operator) labelEnding(play *Play) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	key := runKey(namespace, name)
	uid, ended := o.reports.endedBy(namespace, name)
	if !ended {
		return
	}
	if labeled, done := o.endingLabeled[key]; done && labeled == uid {
		return
	}
	pod := podName(name)
	if uid != "" {
		// A direct read, not the view: an earlier pass may have created
		// the pod that holds the name now, and the view can still hold
		// the pod it replaced, whose UID matches the ending. The patch
		// names the pod by its name alone, so a patch on the view's copy
		// would label the new pod and fade a film that plays.
		held, err := GetPod(o.client, namespace, pod)
		switch {
		case errors.Is(err, ErrNotFound) || (err == nil && held.Metadata.UID != uid):
			o.endingLabeled[key] = uid
			return
		case err != nil:
			fmt.Fprintf(os.Stderr, "reading pod %s/%s to label its ending: %v\n",
				namespace, pod, err)
			return
		}
	}
	err := PatchPodLabels(o.client, namespace, pod,
		map[string]string{endingLabelKey: endingLabelValue})
	if err != nil && !errors.Is(err, ErrNotFound) {
		fmt.Fprintf(os.Stderr, "labeling the ending on pod %s/%s: %v\n",
			namespace, pod, err)
		return
	}
	o.endingLabeled[key] = uid
}
