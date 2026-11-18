package main

import (
	"fmt"
	"time"
)

// show handles a home press by writing a new showAt to the status.session
// of the Television that shows the session's input. The node workload
// for the Display then sends the TV Image View On and Active Source
// (cecnode_show.go). show writes only when that session belongs to this
// Player, is on the same Display, and is awake, because a home press
// does not wake a room that is off.
//
// Unlike a wake, a failed show is not retried. A person pressed home to
// see the screen at that moment, and a TV that switches input on a later
// pass would surprise them. The retries map keeps one failed event for
// each Player, so a show that went through it would also replace a
// failed wake that is waiting to run again.
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
		r.log.printf("%s; did not ask Television %s to show the Display, because the Television has no awake session of Player %s on this Display",
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
