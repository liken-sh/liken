package main

// The node workload's application of a Television's spec.power. The
// adapter that sends the bus's commands applies each generation of each
// Television once: every spec edit is a new metadata.generation, and a
// Television deleted and created again is a new uid. The node workload
// records the generation it applied in status.powerGeneration, so a
// restart applies none twice. It never asserts a generation again, so
// a person who turns the TV off with its own remote is not overruled.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// The confirmation of a power command. After the command the adapter
// reads the TV's power every cecPowerReadEvery. A TV takes seconds to
// wake: cec-follower on vivid reports Standby for about 2 seconds after
// Image View On, then ToOn for about 6 seconds. A TV can also drop a
// command it receives while it changes state. So when a window of
// cecPowerWindow ends and no read in it showed the new state or the
// transition toward it, the adapter sends the command again. A
// generation gets at most cecPowerSends commands, whatever happens to
// its applications, and an application ends after cecPowerSends
// windows.
//
// A TV answers its old state for a while after a command, so a read
// within cecPowerSettle of the adapter's last command is not a reason
// to send nothing: the new state may not show yet.
var (
	cecPowerReadEvery = time.Second
	cecPowerWindow    = 10 * time.Second
	cecPowerSends     = 3
	cecPowerSettle    = 15 * time.Second
)

// powerKey names one spec of one Television: its uid and its
// generation.
type powerKey struct {
	uid        string
	generation int64
}

func keyOf(television *Television) powerKey {
	return powerKey{television.Metadata.UID, television.Metadata.Generation}
}

// powerMemory is what the node workload holds about its applications.
// The node's mutex guards it.
type powerMemory struct {
	// job is the application in progress.
	job *powerJob
	// done is the last generation whose application ended, and
	// unwritten is its result while the API server has not accepted it.
	// A pass writes the result again and never sends the command again.
	done      powerKey
	unwritten *powerRecord
	// sends counts the commands sent for the generation sent names.
	sent  powerKey
	sends int
	// lastCommand is when the adapter last sent the TV a command.
	lastCommand time.Time
}

// powerJob is one application of spec.power in progress.
type powerJob struct {
	key        powerKey
	television Television
	cancel     func()
	done       chan struct{}
}

// powerRecord is the result of one application, for the Television's
// status.
type powerRecord struct {
	television Television
	condition  Condition
}

// passTelevision applies the spec.power of the bus's Television when
// this adapter sends the bus's commands and that generation is not
// applied yet. A new generation cancels an application of an older one.
func (n *cecNode) passTelevision(bus *CECBus) {
	n.writePower()
	own, commands := n.commands(bus)
	if !commands {
		n.cancelPower()
		return
	}
	list, err := ListTelevisions(n.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions: %v\n", err)
		return
	}
	television := televisionFor(list.Items, bus.Metadata.Name)
	n.mutex.Lock()
	running, done := n.powered.job, n.powered.done
	n.mutex.Unlock()
	var key powerKey
	pending := false
	if television != nil {
		key = keyOf(television)
		pending = television.Spec.Power != "" && television.Status.PowerGeneration != key.generation && done != key
	}
	if running != nil && (!pending || running.key != key) {
		n.cancelPower()
		running = nil
	}
	if pending && running == nil {
		n.startPower(*television, own)
	}
}

// startPower runs one application of spec.power in the mode's work,
// so a change of mode stops it before the handle leaves Control.
func (n *cecNode) startPower(television Television, own cec.LogicalAddress) {
	ctx, cancel := context.WithCancel(n.modeContext)
	job := &powerJob{key: keyOf(&television), television: television, cancel: cancel, done: make(chan struct{})}
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

// cancelPower stops the application in progress, if one runs, and
// waits for it to end. A cancelled application writes nothing, so the
// generation stays unapplied for the next application.
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

// applyPower drives the TV to one state and records the result. The
// node workload records the generation as done in memory before it
// writes the result, so a write the API server refuses is written
// again at the next pass, and the command is not sent again.
func (n *cecNode) applyPower(ctx context.Context, job *powerJob, own cec.LogicalAddress) {
	target := cec.PowerOn
	if job.television.Spec.Power == TelevisionStandby {
		target = cec.PowerStandby
	}
	sentBefore := n.sendsFor(job.key)
	began := time.Now()
	result := n.confirmPower(ctx, job.key, own, target)
	asks := fmt.Sprintf("Television %s: generation %d asks %s", job.television.Metadata.Name, job.key.generation, job.television.Spec.Power)
	if result.stopped {
		// An application that sent nothing and stopped leaves no trace on
		// the TV, so only one that sent a command is a line.
		if n.sendsFor(job.key) > sentBefore {
			fmt.Fprintf(n.log, "%s; the adapter on %s sent %s to the TV, and the application stopped after %s, before the TV reported %s\n",
				asks, n.machine, commandNames[target], elapsed(time.Since(began)), target)
		}
		return
	}
	fmt.Fprintf(n.log, "%s; %s\n", asks, result.log)
	condition := stampCondition(conditionPowerApplied, result.verdict, job.key.generation, job.television.Status.Conditions, n.now())
	n.mutex.Lock()
	n.powered.done = job.key
	n.powered.unwritten = &powerRecord{television: job.television, condition: condition}
	n.mutex.Unlock()
	n.writePower()
}

// writePower writes a result the API server has not accepted yet. A
// Television that is gone, or that was created again under the same
// name, refuses the write for good, so the result is dropped.
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
	}
	if err == nil || err == ErrNotFound || err == ErrConflict {
		n.mutex.Lock()
		if n.powered.unwritten == record {
			n.powered.unwritten = nil
		}
		n.mutex.Unlock()
	}
}

// powerResult is how one application ended. stopped says the
// application was cancelled or the adapter left, and nothing is
// recorded.
type powerResult struct {
	verdict verdict
	stopped bool
	// log is the outcome as the node workload's log states it.
	log string
}

// commandNames are the words a message states for each command, in
// the names the CEC specification gives them.
var commandNames = map[cec.PowerStatus]string{cec.PowerOn: "Image View On", cec.PowerStandby: "Standby"}

// towards is the transition a TV reports on its way to a state.
var towards = map[cec.PowerStatus]cec.PowerStatus{cec.PowerOn: cec.PowerToOn, cec.PowerStandby: cec.PowerToStandby}

// send transmits one command for a generation, unless the application
// was cancelled or the generation already had cecPowerSends commands.
// It answers the transmit's result, whether it sent, and the result
// that ends the application when the adapter left or refused the call.
// name is the command's name, for the line of a refused call.
func (n *cecNode) send(ctx context.Context, key powerKey, command cec.Message, name string) (cec.Result, bool, *powerResult) {
	if ctx.Err() != nil {
		return cec.Result{}, false, &powerResult{stopped: true}
	}
	n.mutex.Lock()
	if n.powered.sent != key {
		n.powered.sent, n.powered.sends = key, 0
	}
	if n.powered.sends >= cecPowerSends {
		n.mutex.Unlock()
		return cec.Result{}, false, nil
	}
	n.powered.sends++
	n.powered.lastCommand = time.Now()
	n.mutex.Unlock()
	sent, err := n.device.Transmit(command, 0, 0)
	switch {
	case err != nil && cec.IsGone(err):
		n.fail(err)
		return sent, true, &powerResult{stopped: true}
	case err != nil:
		return sent, true, &powerResult{
			verdict: verdict{ConditionFalse, reasonRefused, fmt.Sprintf("the adapter on %s: %v", n.machine, err)},
			log:     fmt.Sprintf("the adapter on %s could not send %s: %v", n.machine, name, err),
		}
	}
	return sent, true, nil
}

// sendsFor answers how many commands a generation had.
func (n *cecNode) sendsFor(key powerKey) int {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	if n.powered.sent != key {
		return 0
	}
	return n.powered.sends
}

// confirmPower reads the TV's power first and sends nothing when the
// TV already reports the state, because Image View On also switches a
// TV's input. It skips that read when the adapter sent the TV a command
// within cecPowerSettle, because the TV may still answer its old state.
// Otherwise it sends the command and reads the power until the TV
// reports the state. At the end of each window in which no read showed
// the state or the transition toward it, it sends the command again.
func (n *cecNode) confirmPower(ctx context.Context, key powerKey, own cec.LogicalAddress, target cec.PowerStatus) powerResult {
	began := time.Now()
	n.mutex.Lock()
	settled := time.Since(n.powered.lastCommand) >= cecPowerSettle
	n.mutex.Unlock()
	power := cec.PowerUnknown
	if settled {
		var err error
		if power, err = n.askPower(own); err != nil {
			return powerResult{stopped: true}
		}
		if power == target {
			message := fmt.Sprintf("the TV already reported %s, so the adapter on %s sent no command", target, n.machine)
			return powerResult{verdict: verdict{ConditionTrue, reasonConfirmed, message}, log: message}
		}
	}
	command := cec.PowerCommand(own, target == cec.PowerOn)
	name := commandNames[target]
	// last is this application's last transmit, and nil when the
	// generation had its cecPowerSends commands before it started.
	var last *cec.Result
	sent, did, ended := n.send(ctx, key, command, name)
	if ended != nil {
		return *ended
	}
	if did {
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
		if power, err = n.askPower(own); err != nil {
			return powerResult{stopped: true}
		}
		if power == target {
			sends := times(n.sendsFor(key))
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
			sent, did, ended := n.send(ctx, key, command, name)
			if ended != nil {
				return *ended
			}
			if did {
				last = &sent
			}
		}
		progressed = false
	}
	reported := "the TV last reported " + power.String()
	if power == cec.PowerUnknown {
		reported = "the TV did not answer Give Device Power Status"
	}
	message := fmt.Sprintf("the adapter on %s sent %s %s, and %s", n.machine, name, times(n.sendsFor(key)), reported)
	if last != nil && !last.Acked {
		message += "; the TV did not acknowledge the last command: " + last.Status
	}
	return powerResult{verdict: verdict{ConditionFalse, reasonUnconfirmed, message}, log: fmt.Sprintf("%s; the application ended after %s", message, elapsed(time.Since(began)))}
}

// times writes a count of sends the way a sentence reads it.
func times(count int) string {
	if count == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", count)
}
