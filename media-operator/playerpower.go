package main

// A Player's spec.power is On or Off, and an empty field means On. The
// remote's power keys (powerkeys.go), kubectl, and the start of a Play
// on a Player that is Off write it. This file makes the hardware follow
// it.
//
// Some effects are computed from spec.power on every pass: while the
// Player is Off, its Display has the override {power: Off} (screen.go)
// and its Receiver session has awake false (receiver.go). The other
// effects are commands that a pass sends once for each change:
//
//   - To Off: a powerAsk of off on the Receiver, so that
//     equipment-operator puts the receiver and the TV in standby, sleep
//     on the Player's power topic for the screen client, and stop on the
//     commands topic of each unfinished Play.
//   - To On: wake on the power topic. The screen client then publishes
//     an on panel desire, the session's awake flag follows it, and
//     equipment-operator wakes the TV and the receiver.
//
// status.power records the value that the last change applied, so a
// restarted operator does not send the commands again. When status.power
// is empty, the pass records spec.power and sends nothing.
//
// The screen client has no API credentials, so when it turns the panel
// off on its own, spec.power stays On.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// PowerApplied reports the result of the last change of spec.power. When
// a write failed, the condition is False, its reason names the write,
// and the next pass sends the commands again.
const (
	powerAppliedCondition         = "PowerApplied"
	powerReasonApplied            = "Applied"
	powerReasonReceiverAskFailed  = "ReceiverAskFailed"
	powerReasonDisplayWriteFailed = "DisplayWriteFailed"
)

const receiverPowerOff = "off"

// powerStep is spec.power and whether this pass sends the commands for
// a change to it.
type powerStep struct {
	want   string
	moving bool
}

// powerStepFor also sends the commands while PowerApplied is False.
// Some writes of a failed change can have succeeded, so when spec.power
// returns to the value in status.power, the hardware may no longer match
// it.
func powerStepFor(player *Player) powerStep {
	want := specPower(player)
	applied := player.Status.Power
	condition, found := conditionOfType(player.Status.Conditions, powerAppliedCondition)
	failed := found && condition.Status == conditionFalse
	return powerStep{want: want, moving: applied != "" && (applied != want || failed)}
}

func (p powerStep) turningOff() bool { return p.moving && p.want == playerPowerOff }
func (p powerStep) turningOn() bool  { return p.moving && p.want == playerPowerOn }

// powerOutcome collects the writes of one change of spec.power, for the
// message of PowerApplied, and the first write that failed.
type powerOutcome struct {
	writes  []string
	reason  string
	failure string
}

// record records a write that succeeded.
func (o *powerOutcome) record(format string, values ...any) {
	o.writes = append(o.writes, fmt.Sprintf(format, values...))
}

// fail records a failed write. Only the first failure sets the reason,
// because the next pass sends every command again anyway.
func (o *powerOutcome) fail(reason, message string) {
	if o.reason == "" {
		o.reason, o.failure = reason, message
	}
}

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
		Message: want + ": " + strings.Join(o.writes, ", "),
	}
}

// settlePower runs after the pass has written the Display and the
// Receiver session into outcome. It publishes the bus commands and sets
// the power, poweredOnAt, and PowerApplied in desired. When a write
// failed, desired.Power keeps the old value, so the next pass sends
// every command again. That is safe because each command names the
// state it wants, not a toggle.
//
// The pass that turns the Player on records status.poweredOnAt before
// it publishes the wake, so every panel desire that the screen client
// publishes in response is later than that time.
func (o *operator) settlePower(player *Player, plays []Play, step powerStep, outcome *powerOutcome, desired *PlayerStatus) {
	desired.Power = step.want
	desired.PoweredOnAt = player.Status.PoweredOnAt
	if !step.moving {
		if condition, found := conditionOfType(player.Status.Conditions, powerAppliedCondition); found {
			desired.Conditions = append(desired.Conditions, condition)
		}
		return
	}
	if step.turningOn() {
		desired.PoweredOnAt = o.now().UTC().Format(askTimeLayout)
	}
	o.publishPowerCommands(player, plays, step, outcome)
	condition := composeCondition(player, outcome.condition(step.want))
	desired.Conditions = append(desired.Conditions, condition)
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	logLine(o.log, "player %s: spec.power %s, was %s: %s", key, step.want, player.Status.Power, condition.Message)
	if outcome.reason != "" {
		desired.Power = player.Status.Power
	}
}

// composeCondition sets next on a copy of the status conditions, so
// lastTransitionTime changes only when the condition's status changes.
func composeCondition(player *Player, next PlayerCondition) PlayerCondition {
	composed := append([]PlayerCondition(nil), player.Status.Conditions...)
	conditions.Set(&composed, next)
	condition, _ := conditions.Find(composed, next.Type)
	return condition
}

// publishPowerCommands sends each unfinished Play the same stop that the
// remote's stop key sends, so the playback pod ends the way every other
// ending does.
func (o *operator) publishPowerCommands(player *Player, plays []Play, step powerStep, outcome *powerOutcome) {
	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	action := screenWake
	if step.want == playerPowerOff {
		action = screenSleep
	}
	o.publishCommand(playerPowerTopic(o.topicBase, namespace, name), powerMessage{Action: action})
	outcome.record("published %s to the screen", action)
	if step.want != playerPowerOff {
		return
	}
	for _, play := range unfinishedPlayNames(player, plays) {
		o.publishCommand(playCommandsTopic(o.topicBase, namespace, play), mediaCommand{Action: actionStop})
		outcome.record("published stop to Play %s", play)
	}
}

// publishCommand does not set the retain flag, so a client that
// subscribes later does not receive an old command.
func (o *operator) publishCommand(topic string, message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	o.bus.Publish(topic, payload, false)
}

// unfinishedPlayNames selects the same Plays as playerHasUnfinishedPlay.
func unfinishedPlayNames(player *Player, plays []Play) []string {
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

// postPowerChange posts an Event for a change that succeeded. A change
// from Off to On, or back, usually leaves PowerApplied True with the
// reason Applied, so no condition transition posts an Event for it.
func (o *operator) postPowerChange(player *Player, written PlayerStatus, step powerStep, moved []PlayerCondition) {
	if !step.moving || written.Power != step.want {
		return
	}
	for _, condition := range moved {
		if condition.Type == powerAppliedCondition {
			return
		}
	}
	if condition, held := conditionOfType(written.Conditions, powerAppliedCondition); held {
		o.recorder.Normal(playerRef(player), condition.Reason, condition.Message)
	}
}

// turnOnForPlay sets spec.power to On, with the same apply as a power
// key, before a Play starts on a Player that is Off. The next pass then
// runs the change to On as it does for a key press. The caller's copy of
// the Player is set to On too, so the session that the caller applies
// next is awake. When the apply fails, the Play still starts, and the
// equipment stays asleep until spec.power is On.
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
