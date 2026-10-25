package main

// A Player's power. spec.power is On or Off, and an absent field is On.
// A remote's power keys write it (powerkeys.go), and so does a person
// with kubectl. This operator turns the unit's hardware to follow it.
//
// Two kinds of work follow spec.power. The states follow it on every
// pass, so they need no record: while the Player is Off, the screen's
// Display holds the override {power: Off} (screen.go), and the
// Receiver session's awake flag is false (receiver.go). The asks are
// events, so they go out once for each change: on Off, a powerAsk of
// off on the Receiver, which the equipment operator answers by putting
// the receiver and the TV in standby, a sleep on the power topic, which
// lowers the screen client's shade, and a stop on each standing Play's
// commands topic. On On, a wake on the power topic. The client then
// states the on desire, the awake flag follows the desire, and the
// equipment operator wakes the TV and the receiver.
//
// The record of a change is status.power, the value the operator last
// applied, because a change must survive a restart of the operator and
// the operator holds no state of its own beyond caches. A pass acts
// when spec.power differs from status.power. A Player whose status
// records no value adopts its spec and acts on nothing, the same way
// the operator adopts the Receiver sessions it finds at start.
//
// The idle timers stay a soft off. They darken the panel and let the
// awake flag go false, and spec.power stays On through them.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// The PowerApplied condition says what the last change of spec.power
// asked of the unit's hardware. True means every write landed. False
// names the write that failed, and the next pass asks again.
const (
	powerAppliedCondition         = "PowerApplied"
	powerReasonApplied            = "Applied"
	powerReasonReceiverAskFailed  = "ReceiverAskFailed"
	powerReasonDisplayWriteFailed = "DisplayWriteFailed"
)

// The powerAsk actions this operator writes on a Receiver.
const receiverPowerOff = "off"

// powerStep is what one pass does about one Player's power: the value
// spec.power asks for, and whether this pass asks the hardware for it.
type powerStep struct {
	want   string
	moving bool
}

// powerStepFor reads the step from the Player the pass holds. A pass
// asks when spec.power differs from the value the status records, and
// also when the last change failed: some of its writes may have landed,
// so a change that spec.power took back still asks the hardware for the
// value it wants now.
func powerStepFor(player *Player) powerStep {
	want := specPower(player)
	applied := player.Status.Power
	held, holds := heldCondition(player.Status.Conditions, powerAppliedCondition)
	failed := holds && held.Status == conditionFalse
	return powerStep{want: want, moving: applied != "" && (applied != want || failed)}
}

// turningOff and turningOn answer whether this pass is the one that
// asks the hardware for a change.
func (p powerStep) turningOff() bool { return p.moving && p.want == playerPowerOff }
func (p powerStep) turningOn() bool  { return p.moving && p.want == playerPowerOn }

// powerOutcome collects what the pass asked of each piece of hardware
// for a change of spec.power, and the first write that failed.
type powerOutcome struct {
	asked   []string
	reason  string
	failure string
}

// ask records one thing the pass asked, in the words of the condition's
// message.
func (o *powerOutcome) ask(format string, values ...any) {
	o.asked = append(o.asked, fmt.Sprintf(format, values...))
}

// fail records a write that failed. The first failure names the
// condition's reason, because the next pass asks for everything again.
func (o *powerOutcome) fail(reason, message string) {
	if o.reason == "" {
		o.reason, o.failure = reason, message
	}
}

// condition is the PowerApplied condition for one change.
func (o *powerOutcome) condition(want string) PlayerCondition {
	if o.reason != "" {
		return PlayerCondition{
			Type:    powerAppliedCondition,
			Status:  conditionFalse,
			Reason:  o.reason,
			Message: want + ": " + o.failure,
		}
	}
	return PlayerCondition{
		Type:    powerAppliedCondition,
		Status:  conditionTrue,
		Reason:  powerReasonApplied,
		Message: want + ": " + strings.Join(o.asked, ", "),
	}
}

// settlePower publishes the asks of a change, and answers the power the
// status records and the conditions it carries. The Display and the
// Receiver were written earlier in the same pass, and outcome holds
// what they were asked. A change whose writes all landed records the
// new value. A change with a failed write keeps the value the status
// held, so the next pass asks the hardware again; every ask names the
// state it wants, so a second ask leaves hardware already in that
// state as it is.
//
// A pass that changes nothing carries the condition the status holds.
func (o *operator) settlePower(player *Player, plays []Play, step powerStep, outcome *powerOutcome, list []PlayerCondition) (string, []PlayerCondition) {
	if !step.moving {
		if held, holds := heldCondition(player.Status.Conditions, powerAppliedCondition); holds {
			list = append(list, held)
		}
		return step.want, list
	}
	o.announcePower(player, plays, step, outcome)
	condition := composeCondition(player, outcome.condition(step.want))
	list = append(list, condition)
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	logLine(o.log, "player %s: spec.power %s, was %s: %s", key, step.want, player.Status.Power, condition.Message)
	if outcome.reason != "" {
		return player.Status.Power, list
	}
	return step.want, list
}

// composeCondition sets one condition over the ones the status holds,
// so its lastTransitionTime moves only when its status does.
func composeCondition(player *Player, next PlayerCondition) PlayerCondition {
	composed := append([]PlayerCondition(nil), player.Status.Conditions...)
	conditions.Set(&composed, next)
	condition, _ := conditions.Find(composed, next.Type)
	return condition
}

// announcePower publishes the asks of a change on the bus. The screen
// client reads sleep and wake on the power topic. A standing Play stops
// the way the remote's stop key stops it, through its commands topic,
// so the playback pod ends the run by the path every ending takes.
func (o *operator) announcePower(player *Player, plays []Play, step powerStep, outcome *powerOutcome) {
	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	action := screenWake
	if step.want == playerPowerOff {
		action = screenSleep
	}
	o.publishCommand(playerPowerTopic(o.topicBase, namespace, name), powerMessage{Action: action})
	outcome.ask("asked the screen to %s", action)
	if step.want != playerPowerOff {
		return
	}
	for _, play := range standingPlaysOn(player, plays) {
		o.publishCommand(playCommandsTopic(o.topicBase, namespace, play), mediaCommand{Action: actionStop})
		outcome.ask("asked play %s to stop", play)
	}
}

// publishCommand publishes one ask, not retained, because an ask is an
// event and not a state.
func (o *operator) publishCommand(topic string, message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	o.bus.Publish(topic, payload, false)
}

// standingPlaysOn names each Play that stands on the unit, by the test
// playerHasStandingPlay makes.
func standingPlaysOn(player *Player, plays []Play) []string {
	var names []string
	for index := range plays {
		play := &plays[index]
		if play.Metadata.Namespace != player.Metadata.Namespace || playerName(play) != player.Metadata.Name {
			continue
		}
		if !finishedPhase(play.Status.Phase) {
			names = append(names, play.Metadata.Name)
		}
	}
	return names
}

// postPowerChange posts the Event of a change that landed and that no
// condition transition posted: a change from Off to On, or back, leaves
// PowerApplied True with the reason Applied, and only its message moves.
// Each change is an action a person caused, so each one posts.
func (o *operator) postPowerChange(player *Player, written PlayerStatus, step powerStep, moved []PlayerCondition) {
	if !step.moving || written.Power != step.want {
		return
	}
	for _, condition := range moved {
		if condition.Type == powerAppliedCondition {
			return
		}
	}
	if condition, held := heldCondition(written.Conditions, powerAppliedCondition); held {
		o.recorder.Normal(playerRef(player), condition.Reason, condition.Message)
	}
}

// turnOnForPlay turns a Player that is off on before a Play starts on
// it, with the same apply a key press sends, so the pass after it runs
// the change from Off to On the way a press does: the wake, the lifted
// override, the condition, and the Event. A person who starts a film
// wants to see it. The local copy takes the new value, so the session
// the caller applies next is a Player that is on. A refused apply
// leaves the Player off, and the run starts on equipment that stays
// asleep until spec.power turns On.
func (o *operator) turnOnForPlay(play *Play, player *Player) {
	if specPower(player) != playerPowerOff {
		return
	}
	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	if err := ApplyPlayerPower(o.client, o.view.players.Versions, namespace, name, playerPowerOn); err != nil {
		fmt.Fprintf(os.Stderr, "play %s/%s: applying spec.power On on player %s: %v\n",
			play.Metadata.Namespace, play.Metadata.Name, name, err)
		return
	}
	player.Spec.Power = playerPowerOn
	logLine(o.log, "play %s/%s: applied spec.power On on player %s, was Off, because a play starts on it",
		play.Metadata.Namespace, play.Metadata.Name, name)
}
