package main

// The Deployment's half of a home press. A TV that shows its own apps
// is on another input, and a home press on the Player must bring the
// Player back to the screen. The session writes a new showAt on the TV
// that shows its input, and the node workload that speaks for the
// Display sends the TV Image View On and Active Source when the TV
// reports On (cecnode_show.go). The ask needs a session that holds the
// room awake, because a home press does not wake a room that is off.

import (
	"fmt"
	"time"
)

// show writes a new showAt on the TV that shows the session's input,
// when the TV's session is this Player's on the same Display and holds
// the room awake. It keeps the rest of the session as the TV holds it.
// An ask the API server refuses is not tried again, unlike a wake: a
// person pressed home for the screen now, and a TV that switched on a
// later pass would surprise them. It stays out of the retries for the
// same reason, so it never replaces a wake that waits for a retry.
func (r *roomTelevision) show(trigger string) {
	t := r.sessions
	t.mutex.Lock()
	defer t.mutex.Unlock()
	_, display, television, ok := r.resolve()
	if !ok || television == nil {
		return
	}
	held := television.Status.Session
	if held == nil || held.Player != r.player || held.Display != display || !held.Awake {
		r.log.printf("%s; asked Television %s for nothing, because Player %s's session does not hold the room awake",
			trigger, television.Metadata.Name, r.player)
		return
	}
	asked := *held
	asked.ShowAt = time.Now().UTC().Format(wakeTimeLayout)
	line := fmt.Sprintf("%s; asked Television %s to show Display %s", trigger, television.Metadata.Name, display)
	if err := t.apply(television.Metadata.Name, &asked); err != nil {
		r.log.refused(line, err)
		return
	}
	r.log.printf("%s", line)
}
