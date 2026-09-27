package main

// The node workload's standby of a Television's session. When a person
// presses the remote's power button to turn the room off, the
// Deployment writes the time in the Television's
// status.session.standbyAt and sets awake to false. The adapter that
// speaks for the session's Display then sends the TV Standby, at most
// once for each standbyAt, the way it applies a spec.power of Standby.
// Before its first command it writes that standbyAt in
// status.standbyAt as a started mark, so a node workload that restarts
// during the standby, or after it, sends nothing more for it.
//
// Only a press of the power button writes a standbyAt. A session that
// goes idle, a screen that sleeps, a session that ends, and an operator
// that restarts write none, because a TV in a living room shows other
// inputs, such as a streaming player, while the room's own player is
// idle. The node workload acts only on a standby it sees arrive: a
// standbyAt that is already in the status when the node workload
// starts is what it finds, so it sends nothing for it.

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// standbyMemory is what the node workload holds about its standbys.
// The node's mutex guards it. The fields mean what the fields of the
// same names in wakeMemory mean.
type standbyMemory struct {
	job          *standbyJob
	done         commandKey
	unwritten    *wakeRecord
	budget       commandBudget
	listed       bool
	foundAtStart commandKey
	seen         commandKey
	seenAt       time.Time
	waiting      commandKey
}

// standbyJob is one standby in progress.
type standbyJob struct {
	key        commandKey
	television Television
	cancel     func()
	done       chan struct{}
	// superseded says a new generation of spec.power stopped the
	// standby, and the pass that stopped it writes its result.
	superseded atomic.Bool
}

func standbyKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, "standby " + television.Status.Session.StandbyAt}
}

// passStandby starts, stops, or skips the standby of the bus's
// Television. A standby runs when the session is asleep, holds a
// standbyAt with no started mark in status.standbyAt, and names a
// Display this adapter speaks for. A session that wakes again removes
// the standbyAt, which stops a standby in progress, so the newer press
// wins.
//
// spec.power goes first, by the rule the wake follows: a standby does
// not start while a generation of spec.power is not applied yet, and a
// generation that arrives while a standby runs stops it for good.
func (n *cecNode) passStandby(bus *CECBus, television *Television) {
	n.writeStandby()
	session := sessionOf(television)
	live := session != nil && !session.Awake && session.StandbyAt != ""
	var key commandKey
	var own cec.LogicalAddress
	speaks := false
	if live {
		key = standbyKeyOf(television)
		own, _, speaks = n.speaksFor(bus, session.Display)
	}
	n.mutex.Lock()
	first := !n.standby.listed
	n.standby.listed = true
	if live && key != n.standby.seen {
		n.standby.seen, n.standby.seenAt = key, n.now()
		if first {
			n.standby.foundAtStart = key
		}
	}
	running, done, seenAt, found := n.standby.job, n.standby.done, n.standby.seenAt, n.standby.foundAtStart
	n.mutex.Unlock()
	powerPending := television != nil && television.Spec.Power != "" && television.Status.PowerGeneration != television.Metadata.Generation
	if running != nil && powerPending {
		running.superseded.Store(true)
		n.cancelStandby()
		n.supersedeStandby(running, television)
		return
	}
	wanted := live && speaks
	if running != nil && !(wanted && running.key == key) {
		n.cancelStandby()
		running = nil
	}
	if !wanted || running != nil || done == key || session.StandbyAt == television.Status.StandbyAt {
		return
	}
	asks := standbyWords(television)
	switch {
	case key == found:
		n.mutex.Lock()
		n.standby.done = key
		n.mutex.Unlock()
		fmt.Fprintf(n.log, "%s; the standby was in the status when the node workload started, so the adapter on %s sends nothing for it\n", asks, n.machine)
	case n.now().Sub(seenAt) > cecWakeFresh:
		message := fmt.Sprintf("the adapter on %s first saw the standby %s ago, which is more than %s, so it sent nothing", n.machine, elapsed(n.now().Sub(seenAt)), elapsed(cecWakeFresh))
		fmt.Fprintf(n.log, "%s; %s\n", asks, message)
		n.recordStandby(television, verdict{ConditionFalse, reasonTooLate, message})
	case powerPending:
		n.mutex.Lock()
		logged := n.standby.waiting == key
		n.standby.waiting = key
		n.mutex.Unlock()
		if !logged {
			fmt.Fprintf(n.log, "%s; generation %d of spec.power asks %s and is not applied yet, so the standby waits for it\n",
				asks, television.Metadata.Generation, television.Spec.Power)
		}
	default:
		// A wake and a standby send the TV opposite commands, so a
		// standby never runs beside a wake. The session that asks for the
		// standby is asleep, so the wake pass already stopped its wake;
		// this stop covers a wake that ends in the same moment.
		n.cancelWake()
		n.startStandby(*television, own)
	}
}

// standbyWords starts every line about one standby.
func standbyWords(television *Television) string {
	session := television.Status.Session
	return fmt.Sprintf("Television %s: the remote's power button turned Player %s's room off at %s", television.Metadata.Name, session.Player, session.StandbyAt)
}

// startStandby runs one standby in the mode's work, so a change of mode
// stops it before the handle leaves Control. A result of an older
// standby that the API server has not accepted is dropped, because
// writing it later would move status.standbyAt back to the older one.
func (n *cecNode) startStandby(television Television, own cec.LogicalAddress) {
	ctx, cancel := context.WithCancel(n.modeContext)
	job := &standbyJob{key: standbyKeyOf(&television), television: television, cancel: cancel, done: make(chan struct{})}
	n.mutex.Lock()
	n.standby.job = job
	n.standby.unwritten = nil
	n.mutex.Unlock()
	n.modeWork.Go(func() {
		defer close(job.done)
		defer func() {
			n.mutex.Lock()
			if n.standby.job == job {
				n.standby.job = nil
			}
			n.mutex.Unlock()
		}()
		n.standbyTV(ctx, job, own)
	})
}

// cancelStandby stops the standby in progress, if one runs, and waits
// for it to end and write its result.
func (n *cecNode) cancelStandby() {
	n.mutex.Lock()
	job := n.standby.job
	n.mutex.Unlock()
	if job == nil {
		return
	}
	job.cancel()
	<-job.done
}

// recordStandby settles a standby and writes its result.
func (n *cecNode) recordStandby(television *Television, result verdict) {
	condition := stampCondition(conditionStandbyApplied, result, television.Metadata.Generation, television.Status.Conditions, n.now())
	n.mutex.Lock()
	n.standby.done = standbyKeyOf(television)
	n.standby.unwritten = &wakeRecord{television: *television, condition: condition}
	n.mutex.Unlock()
	n.writeStandby()
}

// supersedeStandby records a standby that a new generation of
// spec.power stopped, and writes its one line. A standby that ended on
// its own before the cancel reached it keeps its own result.
func (n *cecNode) supersedeStandby(job *standbyJob, television *Television) {
	n.mutex.Lock()
	ended := n.standby.done == job.key
	n.mutex.Unlock()
	if ended {
		return
	}
	message := fmt.Sprintf("generation %d of spec.power asks %s, so the standby stopped, and the adapter on %s sends nothing more for it",
		television.Metadata.Generation, television.Spec.Power, n.machine)
	fmt.Fprintf(n.log, "%s; %s\n", standbyWords(&job.television), message)
	n.recordStandby(&job.television, verdict{ConditionFalse, reasonSuperseded, message})
}

// standbyTV writes the started mark, sends the TV Standby the way
// spec.power does, and records the result. A mark the API server
// refuses sends nothing, and the next pass tries the standby again,
// because without the mark a restart would not know it had begun.
func (n *cecNode) standbyTV(ctx context.Context, job *standbyJob, own cec.LogicalAddress) {
	asks := standbyWords(&job.television)
	standbyAt := job.television.Status.Session.StandbyAt
	mark := stampCondition(conditionStandbyApplied, verdict{ConditionUnknown, reasonEnteringStandby,
		fmt.Sprintf("the adapter on %s started the standby", n.machine)}, job.television.Metadata.Generation, job.television.Status.Conditions, n.now())
	if err := ApplyTelevisionStandby(n.client, &job.television, n.machine, standbyAt, mark); err != nil {
		fmt.Fprintf(os.Stderr, "writing the started mark of Television %s's standby: %v\n", job.television.Metadata.Name, err)
		return
	}
	job.television.Status.Conditions = []Condition{mark}
	began := time.Now()
	result := n.confirmPower(ctx, &n.standby.budget, job.key, own, cec.PowerStandby)
	if result.stopped && job.superseded.Load() {
		return
	}
	if result.stopped {
		result.log = fmt.Sprintf("the standby stopped after %s, before the TV reported Standby", elapsed(time.Since(began)))
		if sent := n.sendsFor(&n.standby.budget, job.key); sent > 0 {
			result.log = fmt.Sprintf("the adapter on %s sent Standby to the TV %s, and the standby stopped after %s, before the TV reported Standby",
				n.machine, times(sent), elapsed(time.Since(began)))
		}
		result.verdict = verdict{ConditionFalse, reasonStopped, result.log}
	}
	fmt.Fprintf(n.log, "%s; %s\n", asks, result.log)
	n.recordStandby(&job.television, result.verdict)
}

// writeStandby writes a result the API server has not accepted yet. A
// Television that is gone, or that was created again under the same
// name, refuses the write for good, so the result is dropped.
func (n *cecNode) writeStandby() {
	n.mutex.Lock()
	record := n.standby.unwritten
	n.mutex.Unlock()
	if record == nil {
		return
	}
	err := ApplyTelevisionStandby(n.client, &record.television, n.machine, record.television.Status.Session.StandbyAt, record.condition)
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing the standby of Television %s: %v\n", record.television.Metadata.Name, err)
	}
	if err == nil || err == ErrNotFound || err == ErrConflict {
		n.mutex.Lock()
		if n.standby.unwritten == record {
			n.standby.unwritten = nil
		}
		n.mutex.Unlock()
	}
}
