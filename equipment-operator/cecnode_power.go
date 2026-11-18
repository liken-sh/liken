package main

// The node workload whose adapter sends the bus's commands applies each
// generation of a `Television`'s `spec.power` once. It records the
// generation in `status.powerGeneration`, so a restarted node workload
// does not apply it again, and a TV that a person turned off with its
// own remote stays off. A spec edit makes a new `metadata.generation`,
// and a `Television` that is deleted and created again has a new uid,
// so either one is applied.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// After a power command, the adapter reads the TV's power every
// cecPowerReadEvery until the TV reports the target state. A TV takes
// seconds to wake: it reports Standby, then ToOn, then On. It can also
// drop a command that it receives during the change. So when
// cecPowerWindow passes and no read showed the target state or the
// transition toward it, the adapter sends the command again. An
// application ends after cecPowerSends windows, and an intent gets at
// most cecPowerSends commands in total, even when its application is
// cancelled and started again.
//
// A TV can report its old state for a while after a command. So a read
// within cecPowerSettle of the last command to the TV, sent by this
// adapter or a Standby heard on the bus, does not prove that the TV
// needs no command.
const (
	cecPowerReadEvery = time.Second
	cecPowerWindow    = 10 * time.Second
	cecPowerSends     = 3
	cecPowerSettle    = 15 * time.Second
)

// commandKey identifies one intent of one `Television`: its uid, and the
// generation of its spec or the time of its session's wake.
type commandKey struct {
	uid    string
	intent string
}

func powerKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, fmt.Sprintf("generation %d", television.Metadata.Generation)}
}

// commandBudget counts the commands sent for the intent in sent. A
// command for another intent starts the count again. The node's mutex
// guards it.
type commandBudget struct {
	sent  commandKey
	sends int
}

// powerState is the node workload's record of its applications of
// `spec.power`. The node's mutex guards it.
type powerState struct {
	// job is the application in progress.
	job *powerJob
	// done is the intent of the last application that ended, and
	// unwritten is its result until the API server accepts it. A pass
	// writes the result again and never sends the command again.
	done      commandKey
	unwritten *powerRecord
	budget    commandBudget
}

// powerJob is one running application of `spec.power`.
type powerJob struct {
	key        commandKey
	television Television
	cancel     func()
	done       chan struct{}
}

// powerRecord is the result of one application, to write to the
// `Television`'s status.
type powerRecord struct {
	television Television
	condition  Condition
}

// passTelevision starts an application of the `Television`'s
// `spec.power` when this adapter sends the bus's commands and the
// generation is not applied yet. It cancels a running application when
// a new generation replaces it, when the `Television` is gone, or when
// this adapter no longer sends the bus's commands.
func (n *cecNode) passTelevision(bus *CECBus, television *Television) {
	n.writePower()
	own, commands := n.commands(bus)
	if !commands {
		n.cancelPower()
		return
	}
	n.mutex.Lock()
	running, done := n.powered.job, n.powered.done
	n.mutex.Unlock()
	var key commandKey
	pending := false
	if television != nil {
		key = powerKeyOf(television)
		pending = television.Spec.Power != "" && television.Status.PowerGeneration != television.Metadata.Generation && done != key
	}
	if running != nil && (!pending || running.key != key) {
		n.cancelPower()
		running = nil
	}
	if pending && running == nil {
		n.startPower(*television, own)
	}
}

// startPower runs one application of `spec.power` in the mode's work
// group, so a change of mode cancels it before the handle leaves
// Control.
func (n *cecNode) startPower(television Television, own cec.LogicalAddress) {
	ctx, cancel := context.WithCancel(n.modeContext)
	job := &powerJob{key: powerKeyOf(&television), television: television, cancel: cancel, done: make(chan struct{})}
	n.mutex.Lock()
	n.powered.job = job
	n.mutex.Unlock()
	n.modeWork.Go(func() {
		defer close(job.done)
		defer func() {
			n.mutex.Lock()
			if n.powered.job == job {
				n.powered.job = nil
			}
			n.mutex.Unlock()
		}()
		n.applyPower(ctx, job, own)
	})
}

// cancelPower cancels the running application, if any, and waits for it
// to end. A cancelled application writes nothing, so the generation
// stays unapplied for a later application.
func (n *cecNode) cancelPower() {
	n.mutex.Lock()
	job := n.powered.job
	n.mutex.Unlock()
	if job == nil {
		return
	}
	job.cancel()
	<-job.done
}

// applyPower drives the TV to the state that `spec.power` asks for and
// records the result. It marks the intent as done in memory before it
// writes the result, so when the API server refuses the write, the next
// pass writes it again and the command is not sent again.
func (n *cecNode) applyPower(ctx context.Context, job *powerJob, own cec.LogicalAddress) {
	target := cec.PowerOn
	if job.television.Spec.Power == TelevisionStandby {
		target = cec.PowerStandby
	}
	budget := &n.powered.budget
	sentBefore := n.sendsFor(budget, job.key)
	began := time.Now()
	result := n.confirmPower(ctx, budget, job.key, own, target)
	request := fmt.Sprintf("Television %s: spec.power at generation %d is %s", job.television.Metadata.Name, job.television.Metadata.Generation, job.television.Spec.Power)
	if result.stopped {
		// An application that stopped before it sent a command changed
		// nothing on the TV, so it writes no log line.
		if n.sendsFor(budget, job.key) > sentBefore {
			fmt.Fprintf(n.log, "%s; the adapter on %s sent %s to the TV, and the application stopped after %s, before the TV reported %s\n",
				request, n.machine, commandNames[target], elapsed(time.Since(began)), target)
		}
		return
	}
	fmt.Fprintf(n.log, "%s; %s\n", request, result.log)
	condition := stampCondition(conditionPowerApplied, result.verdict, job.television.Metadata.Generation, job.television.Status.Conditions, n.now())
	n.mutex.Lock()
	n.powered.done = job.key
	n.powered.unwritten = &powerRecord{television: job.television, condition: condition}
	n.mutex.Unlock()
	n.writePower()
}

// writePower writes a result that the API server has not accepted yet.
// When the `Television` is gone, or was created again under the same
// name, the API server refuses the write permanently, so writePower
// drops the result.
func (n *cecNode) writePower() {
	n.mutex.Lock()
	record := n.powered.unwritten
	n.mutex.Unlock()
	if record == nil {
		return
	}
	err := ApplyTelevisionPower(n.client, &record.television, n.machine, record.television.Metadata.Generation, record.condition)
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing the applied power of Television %s: %v\n", record.television.Metadata.Name, err)
		n.retryLater()
	}
	if err == nil || err == apiclient.ErrNotFound || err == apiclient.ErrConflict {
		n.mutex.Lock()
		settled := n.powered.unwritten == record
		if settled {
			n.powered.unwritten = nil
		}
		n.mutex.Unlock()
		// A pass and the application that made the record can write it at
		// the same time, so only the writer that settles it posts the
		// `Event`.
		if settled && err == nil {
			n.postTelevision(&record.television, record.condition)
		}
	}
}

// powerResult is how an application ended. stopped means that the
// application was cancelled or the adapter left, and nothing is
// recorded.
type powerResult struct {
	verdict verdict
	stopped bool
	// log is the line that the node workload logs for the outcome.
	log string
	// audio is the outcome of the System Audio Mode Request that a wake
	// sends, and nil for a wake that sent none (cecnode_systemaudio.go).
	audio *audioResult
}

// commandNames gives the CEC specification's name for the command that
// drives the TV to each state.
var commandNames = map[cec.PowerStatus]string{cec.PowerOn: "Image View On", cec.PowerStandby: "Standby"}

// towards gives the power status that a TV reports while it changes to
// each state.
var towards = map[cec.PowerStatus]cec.PowerStatus{cec.PowerOn: cec.PowerToOn, cec.PowerStandby: cec.PowerToStandby}

// send transmits one command for an intent, unless the application was
// cancelled or the intent already has cecPowerSends commands. It returns
// the transmit result, whether it transmitted, and a powerResult when
// the application must end because it was cancelled, the adapter left,
// or the kernel refused the call. name is the command's name for the
// log line of a refused call.
func (n *cecNode) send(ctx context.Context, budget *commandBudget, key commandKey, command cec.Message, name string) (cec.Result, bool, *powerResult) {
	if ctx.Err() != nil {
		return cec.Result{}, false, &powerResult{stopped: true}
	}
	n.mutex.Lock()
	if budget.sent != key {
		budget.sent, budget.sends = key, 0
	}
	if budget.sends >= cecPowerSends {
		n.mutex.Unlock()
		return cec.Result{}, false, nil
	}
	budget.sends++
	n.lastCommand = time.Now()
	n.mutex.Unlock()
	sent, err := n.device.Transmit(command, 0, 0)
	switch {
	case err != nil && cec.IsGone(err):
		n.fail(err)
		return sent, true, &powerResult{stopped: true}
	case err != nil:
		return sent, true, &powerResult{
			verdict: verdict{ConditionFalse, reasonRefused, fmt.Sprintf("the adapter on %s: %v", n.machine, err)},
			log:     fmt.Sprintf("the adapter on %s refused %s: %v", n.machine, name, err),
		}
	}
	return sent, true, nil
}

func (n *cecNode) sendsFor(budget *commandBudget, key commandKey) int {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	if budget.sent != key {
		return 0
	}
	return budget.sends
}

// confirmPower drives the TV to target and returns the outcome. It
// reads the TV's power first and sends nothing when the TV already
// reports target, because Image View On also switches the TV's input.
// It skips that read within cecPowerSettle of the last command to the
// TV. Otherwise it sends the command and reads the power as the
// constants at the top of this file describe.
func (n *cecNode) confirmPower(ctx context.Context, budget *commandBudget, key commandKey, own cec.LogicalAddress, target cec.PowerStatus) powerResult {
	began := time.Now()
	n.mutex.Lock()
	settled := time.Since(n.lastCommand) >= cecPowerSettle
	n.mutex.Unlock()
	power := cec.PowerUnknown
	if settled {
		var err error
		if power, err = n.readTVPower(own); err != nil {
			return powerResult{stopped: true}
		}
		if power == target {
			message := fmt.Sprintf("the TV already reported %s, so the adapter on %s sent no command", target, n.machine)
			return powerResult{verdict: verdict{ConditionTrue, reasonConfirmed, message}, log: message}
		}
	}
	command := cec.PowerCommand(own, target == cec.PowerOn)
	name := commandNames[target]
	// last is the result of this application's last transmit. It stays
	// nil when the budget was already spent before this application
	// started.
	var last *cec.Result
	sent, transmitted, stop := n.send(ctx, budget, key, command, name)
	if stop != nil {
		return *stop
	}
	if transmitted {
		last = &sent
	}
	start := time.Now()
	windowEnd := start.Add(cecPowerWindow)
	progressed := false
	for time.Since(start) < time.Duration(cecPowerSends)*cecPowerWindow {
		select {
		case <-ctx.Done():
			return powerResult{stopped: true}
		case <-time.After(cecPowerReadEvery):
		}
		var err error
		if power, err = n.readTVPower(own); err != nil {
			return powerResult{stopped: true}
		}
		if power == target {
			sends := times(n.sendsFor(budget, key))
			return powerResult{
				verdict: verdict{ConditionTrue, reasonConfirmed, fmt.Sprintf(
					"the TV reported %s after the adapter on %s sent %s %s", target, n.machine, name, sends)},
				log: fmt.Sprintf("the adapter on %s sent %s to the TV %s; the TV reported %s after %s",
					n.machine, name, sends, target, elapsed(time.Since(began))),
			}
		}
		progressed = progressed || power == towards[target]
		if time.Now().Before(windowEnd) {
			continue
		}
		windowEnd = time.Now().Add(cecPowerWindow)
		if !progressed {
			sent, transmitted, stop := n.send(ctx, budget, key, command, name)
			if stop != nil {
				return *stop
			}
			if transmitted {
				last = &sent
			}
		}
		progressed = false
	}
	reported := "the TV last reported " + power.String()
	if power == cec.PowerUnknown {
		reported = "the TV did not answer Give Device Power Status"
	}
	message := fmt.Sprintf("the adapter on %s sent %s %s, and %s", n.machine, name, times(n.sendsFor(budget, key)), reported)
	if last != nil && !last.Acked {
		message += "; the TV did not acknowledge the last command: " + last.Status
	}
	return powerResult{verdict: verdict{ConditionFalse, reasonUnconfirmed, message}, log: fmt.Sprintf("%s; the application ended after %s", message, elapsed(time.Since(began)))}
}

// times writes a count of sends as a phrase: "once" or "N times".
func times(count int) string {
	if count == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", count)
}
