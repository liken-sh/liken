package main

// When a film ends, the command sidecar in the playback pod publishes
// an ending report and keeps mpv running for exitGrace before it
// exits. The operator acts on the report in two ways.
//
// labelEnding adds the label media.liken.sh/ending to the playback
// pod. A Layout whose film region selects only pods without that label
// stops matching the pod, and display-operator takes the film off the
// screen with the region's transition. The label is on the pod and not
// on the Play, because a Layout selects pods and display-operator does
// not read this operator's kinds. The label must reach the pod before
// mpv exits, so the pass labels pods as soon as it has read the Plays.
//
// publishEndingStatus publishes the Player's Idle status to the bus as soon as
// the report arrives. The media browser and other readers of the
// Player's status topic act when a Player reads Idle, and a person is
// watching the screen. The pass that the report wakes starts only when
// the running pass finishes, which can take a long time on a busy API
// server. So the bus reader publishes the status itself, from the Plays
// and Players that the last pass listed. The next pass computes the
// same payload and does not publish it again.

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// passSnapshot is the Plays and Players that the last pass listed, for
// the bus reader's goroutine. The pass records each list when it reads
// it, so a failed read keeps the previous list. The slices are copies,
// because the pass then writes finalizers and status into the elements
// of its own lists.
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

// lists returns empty lists until the first pass has read them.
func (s *passSnapshot) lists() ([]Play, []Player) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.plays, s.players
}

// publishEndingStatus publishes the status of the Player of a Play whose film
// is over, from the last pass's lists. The bus reader calls it, so it
// makes no API request, and the publish only queues the message.
//
// When the snapshot has no such Play or Player, no pass has listed them
// yet, and the next pass publishes the status.
func (o *operator) publishEndingStatus(namespace, name string) {
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

func findPlayer(players []Player, namespace, name string) *Player {
	for index := range players {
		player := &players[index]
		if player.Metadata.Namespace == namespace && player.Metadata.Name == name {
			return player
		}
	}
	return nil
}

// labelEnding adds the ending label to the playback pod of a Play whose
// run has ended, once for each pod. The pass calls it before it deletes
// the pod, because display-operator needs the label while the pod still
// draws to the screen.
//
// endingLabeled records the UID of each labeled pod, so a later pod
// with the same name gets its own label. The patch selects the pod by
// name, so when the report gives the pod's UID, labelEnding reads the
// pod from the API server and patches only when the UIDs match. It does
// not read o.view, because the view can still hold a pod that an
// earlier pass replaced, and a patch by that pod's name would label the
// new pod and take a film that is still playing off the screen. Only
// the pass creates playback pods, so no new pod can take the name
// between the read and the patch.
//
// A pod that is gone or replaced is no longer on the screen, so it
// needs no label and is recorded as done. Any other failure is logged,
// and the next pass tries again.
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
		current, err := GetPod(o.client, namespace, pod)
		switch {
		case errors.Is(err, apiclient.ErrNotFound) || (err == nil && current.Metadata.UID != uid):
			o.endingLabeled[key] = uid
			return
		case err != nil:
			fmt.Fprintf(os.Stderr, "reading Pod %s/%s to label its ending: %v\n",
				namespace, pod, err)
			return
		}
	}
	err := PatchPodLabels(o.client, namespace, pod,
		map[string]string{endingLabelKey: endingLabelValue})
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "labeling the ending on Pod %s/%s: %v\n",
			namespace, pod, err)
		return
	}
	o.endingLabeled[key] = uid
}
