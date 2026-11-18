package main

// This file puts a TV in standby when a status.session.powerAsk on the
// Receiver turns the room off. The Deployment records the standby as
// status.session.standbyAt, with awake set to false. The node workload
// whose adapter speaks for the session's Display then sends the TV
// Standby, at most once for each standbyAt. Before it sends anything, it
// copies standbyAt to status.standbyAt, so a node workload that
// restarts during or after the standby does not send it again.
//
// Only a powerAsk that turns the room off writes a standbyAt.
// media-operator writes a powerAsk of off when a Player's spec.power
// changes to Off. An idle session,
// a sleeping screen, or an ended session writes none, because a TV often
// shows another input, such as a streaming player, while the room's
// player is idle. The node workload also sends nothing for a standbyAt
// that is already in the status when it starts, because that standby
// may have been handled before a restart.

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// standbyState is the node workload's state for standbys, guarded by
// the node's mutex. Each field means what the field of the same name
// means in wakeState.
type standbyState struct {
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
	// superseded is set when a new generation of spec.power stopped the
	// standby. The pass that stopped it writes the result instead of the
	// job.
	superseded atomic.Bool
}

func standbyKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, "standby " + television.Status.Session.StandbyAt}
}

// passStandby starts, stops, or skips the standby of the bus's
// Television on each reconcile pass. A standby runs when the session is
// asleep, has a standbyAt that is not yet in status.standbyAt, and names
// a Display that this adapter speaks for. A press that wakes the session
// removes standbyAt, which stops a standby in progress.
//
// spec.power comes first, as it does for a wake. A standby waits while
// a generation of spec.power is not applied yet, and a new generation
// that arrives during a standby stops it for good.
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
	logPrefix := standbyLogPrefix(television)
	switch {
	case key == found:
		n.mutex.Lock()
		n.standby.done = key
		n.mutex.Unlock()
		fmt.Fprintf(n.log, "%s; the standby was in the status when the node workload started, so the adapter on %s sends nothing for it\n", logPrefix, n.machine)
	case n.now().Sub(seenAt) > cecWakeFresh:
		message := fmt.Sprintf("the adapter on %s first saw the standby %s ago, which is more than %s, so it sent nothing", n.machine, elapsed(n.now().Sub(seenAt)), elapsed(cecWakeFresh))
		fmt.Fprintf(n.log, "%s; %s\n", logPrefix, message)
		n.recordStandby(television, verdict{ConditionFalse, reasonTooLate, message})
	case powerPending:
		n.mutex.Lock()
		logged := n.standby.waiting == key
		n.standby.waiting = key
		n.mutex.Unlock()
		if !logged {
			fmt.Fprintf(n.log, "%s; spec.power at generation %d is %s and is not applied yet, so the standby waits for it\n",
				logPrefix, television.Metadata.Generation, television.Spec.Power)
		}
	default:
		// A wake and a standby send the TV opposite commands, so they must
		// not run together. The wake pass stops a wake when the session
		// sleeps, but a wake can still be ending at this moment.
		n.cancelWake()
		n.startStandby(*television, own)
	}
}

func standbyLogPrefix(television *Television) string {
	session := television.Status.Session
	return fmt.Sprintf("Television %s: a powerAsk turned Player %s's room off at %s", television.Metadata.Name, session.Player, session.StandbyAt)
}

// startStandby runs the standby in modeWork, so a change of mode stops
// it before the adapter leaves Control. It drops an unwritten result of
// an older standby, because writing it later would set status.standbyAt
// back to the older press.
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

// cancelStandby stops the standby in progress, if there is one, and
// waits until it has ended and written its result.
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

func (n *cecNode) recordStandby(television *Television, result verdict) {
	condition := stampCondition(conditionStandbyApplied, result, television.Metadata.Generation, television.Status.Conditions, n.now())
	n.mutex.Lock()
	n.standby.done = standbyKeyOf(television)
	n.standby.unwritten = &wakeRecord{television: *television, condition: condition}
	n.mutex.Unlock()
	n.writeStandby()
}

// supersedeStandby records a standby that a new generation of
// spec.power stopped. A standby that ended on its own before the cancel
// reached it keeps its own result.
func (n *cecNode) supersedeStandby(job *standbyJob, television *Television) {
	n.mutex.Lock()
	ended := n.standby.done == job.key
	n.mutex.Unlock()
	if ended {
		return
	}
	message := fmt.Sprintf("spec.power at generation %d is %s, so the standby stopped, and the adapter on %s sends nothing more for it",
		television.Metadata.Generation, television.Spec.Power, n.machine)
	fmt.Fprintf(n.log, "%s; %s\n", standbyLogPrefix(&job.television), message)
	n.recordStandby(&job.television, verdict{ConditionFalse, reasonSuperseded, message})
}

// standbyTV writes status.standbyAt, sends the TV Standby the way
// spec.power does, and records the result. When the API server refuses
// the status write, standbyTV sends nothing and a later pass tries
// again, because without that write a restarted node workload would not
// know that the standby began.
func (n *cecNode) standbyTV(ctx context.Context, job *standbyJob, own cec.LogicalAddress) {
	logPrefix := standbyLogPrefix(&job.television)
	standbyAt := job.television.Status.Session.StandbyAt
	mark := stampCondition(conditionStandbyApplied, verdict{ConditionUnknown, reasonEnteringStandby,
		fmt.Sprintf("the adapter on %s started the standby", n.machine)}, job.television.Metadata.Generation, job.television.Status.Conditions, n.now())
	if err := ApplyTelevisionStandby(n.client, &job.television, n.machine, standbyAt, mark); err != nil {
		fmt.Fprintf(os.Stderr, "writing the started mark of Television %s's standby: %v\n", job.television.Metadata.Name, err)
		n.retryLater()
		return
	}
	n.postTelevision(&job.television, mark)
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
	fmt.Fprintf(n.log, "%s; %s\n", logPrefix, result.log)
	n.recordStandby(&job.television, result.verdict)
}

// writeStandby writes the unwritten result, if there is one. When the
// Television was deleted, or deleted and created again with a new UID,
// the write cannot succeed, so the result is dropped.
func (n *cecNode) writeStandby() {
	n.mutex.Lock()
	record := n.standby.unwritten
	n.mutex.Unlock()
	if record == nil {
		return
	}
	err := ApplyTelevisionStandby(n.client, &record.television, n.machine, record.television.Status.Session.StandbyAt, record.condition)
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing the standby result of Television %s: %v\n", record.television.Metadata.Name, err)
		n.retryLater()
	}
	if err == nil || err == apiclient.ErrNotFound || err == apiclient.ErrConflict {
		n.mutex.Lock()
		settled := n.standby.unwritten == record
		if settled {
			n.standby.unwritten = nil
		}
		n.mutex.Unlock()
		// A pass and the job can both write the same record. Only the
		// one that clears it posts the Event.
		if settled && err == nil {
			n.postTelevision(&record.television, record.condition)
		}
	}
}
