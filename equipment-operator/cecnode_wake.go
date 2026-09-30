package main

// The node workload's wake of a Television's session. When a
// Receiver's session wakes the room, the Deployment writes the time in
// the Television's status.session.wokeAt. The adapter that speaks for
// the session's Display then wakes the TV and makes that Display the
// active source, at most once for each wokeAt. Before its first
// command it writes that wokeAt in status.wokeAt as a started mark, so
// a node workload that restarts during the wake, or after it, sends
// nothing more for it.
//
// A streaming player on the same bus can claim Active Source for itself
// when the room wakes, and the TV and the receiver then show that
// player. So after its own Active Source the adapter listens for a
// guard time, and it sends Active Source again when another source
// claims the input. The guard and a bound on the claims keep it from
// fighting a person who switches the input on purpose: after the
// guard, and after the bound, a claim stands, and a route that a
// person moves through the TV or a switch ends the guard at once
// (cecnode_source.go).
//
// The node workload acts only on a wake it sees arrive. A wokeAt that
// is already in the status when the node workload starts is what it
// finds, not a change it observed, so it sends nothing for it.

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The wake's bounds. cecWakeGuard is how long the adapter listens for
// another source's claim after its first Active Source, and
// cecWakeReclaims is how many times it sends Active Source again in
// that time. A claim often comes in a burst with the claiming device's
// other messages, so the adapter waits cecWakeSettle after a claim
// before it sends its own. A wake that has not started cecWakeFresh
// after the node workload first saw it sends nothing, such as one that
// waited that long for spec.power or for the adapter to join: the room
// may have gone dark since. The node workload measures that time on
// its own clock, from when it first saw the wokeAt.
var (
	cecWakeGuard    = 30 * time.Second
	cecWakeReclaims = 2
	cecWakeSettle   = 2 * time.Second
	cecWakeFresh    = 2 * time.Minute
)

// wakeTimeLayout is the form of status.session.wokeAt: RFC 3339 with
// milliseconds, so two wakes in one second are two times. The node
// workload reads it only as a name for one wake.
const wakeTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// wakeMemory is what the node workload holds about its wakes. The
// node's mutex guards it.
type wakeMemory struct {
	job *wakeJob
	// done is the last wake the node workload settled: run, stopped, or
	// skipped. unwritten is a result the API server has not accepted.
	done      commandKey
	unwritten *wakeRecord
	// budget counts the Image View On, and claims the Active Source,
	// that each wake sent, so a wake that runs again sends no more.
	budget commandBudget
	claims commandBudget
	// listed says the node workload has read the Televisions once, and
	// foundAtStart is the wake that first read held.
	listed       bool
	foundAtStart commandKey
	// seen is the wake the node workload last saw arrive, and seenAt is
	// when it first saw it, on its own clock.
	seen   commandKey
	seenAt time.Time
	// waiting is the wake whose wait behind spec.power the log stated.
	waiting commandKey
}

// wakeJob is one wake in progress.
type wakeJob struct {
	key        commandKey
	television Television
	cancel     func()
	done       chan struct{}
	// others maps the physical address of each other adapter of the bus
	// to its machine. An Active Source from one of them is a later wake
	// of the same bus, and this wake's guard ends.
	others map[cec.PhysicalAddress]string
	// superseded says a new generation of spec.power stopped the wake,
	// and the pass that stopped it writes its result.
	superseded atomic.Bool
	// claimed says the wake sent its first Active Source. Until then the
	// adapter answers a Request Active Source for the wake's Display,
	// whichever source the bus last named (cecnode_answer.go).
	claimed atomic.Bool
}

// wakeRecord is one write of the wake's status: the started mark or
// the result.
type wakeRecord struct {
	television Television
	condition  Condition
}

func wakeKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, "wake " + television.Status.Session.WokeAt}
}

// passWake starts, stops, or skips the wake of the bus's Television.
// A wake runs when the session is awake, holds a wokeAt with no
// started mark in status.wokeAt, and names a Display this adapter
// speaks for. A new wokeAt, a session that goes to sleep, or an adapter
// that stops speaking for the Display stops a wake in progress.
//
// spec.power goes first. A wake does not start while a generation of
// spec.power is not applied yet, because the two would send the TV
// opposite commands at once; it starts when status.powerGeneration
// catches up. A generation that arrives while a wake runs is a person's
// edit, newer than the wake, so it stops the wake for good. The rule
// reads status.powerGeneration, which every node workload sees, so it
// holds when the adapter that sends spec.power is on another machine.
// passTelevisions runs this pass before the power pass, so the wake
// stops before the new generation's first command.
func (n *cecNode) passWake(bus *CECBus, television *Television) {
	n.writeWake()
	session := sessionOf(television)
	live := session != nil && session.Awake && session.WokeAt != ""
	var key commandKey
	var own cec.LogicalAddress
	var physical cec.PhysicalAddress
	speaks := false
	if live {
		key = wakeKeyOf(television)
		own, physical, speaks = n.speaksFor(bus, session.Display)
	}
	n.mutex.Lock()
	first := !n.woken.listed
	n.woken.listed = true
	if live && key != n.woken.seen {
		n.woken.seen, n.woken.seenAt = key, n.now()
		if first {
			n.woken.foundAtStart = key
		}
	}
	running, done, seenAt, found := n.woken.job, n.woken.done, n.woken.seenAt, n.woken.foundAtStart
	n.mutex.Unlock()
	powerPending := television != nil && television.Spec.Power != "" && television.Status.PowerGeneration != television.Metadata.Generation
	if running != nil && powerPending {
		running.superseded.Store(true)
		n.cancelWake()
		n.supersede(running, television)
		return
	}
	wanted := live && speaks
	if running != nil && !(wanted && running.key == key) {
		n.cancelWake()
		running = nil
	}
	if !wanted || running != nil || done == key || session.WokeAt == television.Status.WokeAt {
		return
	}
	asks := wakeWords(television)
	switch {
	case key == found:
		n.settle(key)
		fmt.Fprintf(n.log, "%s; the wake was in the status when the node workload started, so the adapter on %s sends nothing for it\n", asks, n.machine)
	case n.now().Sub(seenAt) > cecWakeFresh:
		message := fmt.Sprintf("the adapter on %s first saw the wake %s ago, which is more than %s, so it sent nothing", n.machine, elapsed(n.now().Sub(seenAt)), elapsed(cecWakeFresh))
		fmt.Fprintf(n.log, "%s; %s\n", asks, message)
		n.record(television, verdict{ConditionFalse, reasonTooLate, message})
	case powerPending:
		n.mutex.Lock()
		logged := n.woken.waiting == key
		n.woken.waiting = key
		n.mutex.Unlock()
		if !logged {
			fmt.Fprintf(n.log, "%s; generation %d of spec.power asks %s and is not applied yet, so the wake waits for it\n",
				asks, television.Metadata.Generation, television.Spec.Power)
		}
	default:
		// A wake is a newer press than a standby in progress, and the two
		// send the TV opposite commands, so the standby stops first.
		n.cancelStandby()
		n.startWake(*television, own, physical, othersOf(bus, n.machine, n.now()))
	}
}

// wakeWords starts every line about one wake.
func wakeWords(television *Television) string {
	session := television.Status.Session
	return fmt.Sprintf("Television %s: Player %s's session woke the room at %s", television.Metadata.Name, session.Player, session.WokeAt)
}

// sessionOf answers a Television's session, and nil for no Television.
func sessionOf(television *Television) *TelevisionSession {
	if television == nil {
		return nil
	}
	return television.Status.Session
}

// othersOf maps the physical address that each other adapter of the
// bus announces to its machine, from the current entries.
func othersOf(bus *CECBus, machine string, now time.Time) map[cec.PhysicalAddress]string {
	others := map[cec.PhysicalAddress]string{}
	for other, entry := range reportedEntries(bus, now).current {
		if address, err := cec.ParsePhysicalAddress(entry.PhysicalAddress); err == nil && other != machine {
			others[address] = other
		}
	}
	return others
}

// speaksFor answers whether this adapter speaks for a Display on the
// bus, with the logical and physical address it sends from. The first
// adapter in spec.adapters that names the Display and holds a logical
// address speaks for it. The wake's Active Source must come from that
// adapter, because the adapter announces the Display's physical
// address, and Active Source states its sender's own address. Image
// View On goes out from the same adapter, because one-touch play is one
// source's sequence: the wake is one intent with one sender.
func (n *cecNode) speaksFor(bus *CECBus, display string) (cec.LogicalAddress, cec.PhysicalAddress, bool) {
	n.mutex.Lock()
	control := n.applied.mode == CECControl
	logical := n.entry.LogicalAddress
	announced := n.entry.PhysicalAddress
	n.mutex.Unlock()
	physical, err := cec.ParsePhysicalAddress(announced)
	if !control || logical == nil || err != nil {
		return 0, 0, false
	}
	entries := reportedEntries(bus, n.now())
	for _, adapter := range bus.Spec.Adapters {
		if adapter.Display != display {
			continue
		}
		if adapter.Machine == n.machine {
			return cec.LogicalAddress(*logical), physical, true
		}
		if other, current := entries.current[adapter.Machine]; current && other.Mode == CECControl && other.LogicalAddress != nil {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// startWake runs one wake in the mode's work, so a change of mode
// stops it before the handle leaves Control. A result of an older wake
// that the API server has not accepted is dropped, because writing it
// later would move status.wokeAt back to the older wake.
func (n *cecNode) startWake(television Television, own cec.LogicalAddress, physical cec.PhysicalAddress, others map[cec.PhysicalAddress]string) {
	ctx, cancel := context.WithCancel(n.modeContext)
	job := &wakeJob{key: wakeKeyOf(&television), television: television, cancel: cancel, done: make(chan struct{}), others: others}
	n.mutex.Lock()
	n.woken.job = job
	n.woken.unwritten = nil
	n.mutex.Unlock()
	n.modeWork.Go(func() {
		defer close(job.done)
		defer func() {
			n.mutex.Lock()
			if n.woken.job == job {
				n.woken.job = nil
			}
			n.mutex.Unlock()
		}()
		n.wakeTV(ctx, job, own, physical)
	})
}

// cancelWake stops the wake in progress, if one runs, and waits for it
// to end and write its result.
func (n *cecNode) cancelWake() {
	n.mutex.Lock()
	job := n.woken.job
	n.mutex.Unlock()
	if job == nil {
		return
	}
	job.cancel()
	<-job.done
}

// settle records a wake as settled in memory, so no later pass runs it.
func (n *cecNode) settle(key commandKey) {
	n.mutex.Lock()
	n.woken.done = key
	n.mutex.Unlock()
}

// record settles a wake and writes its result.
func (n *cecNode) record(television *Television, result verdict) {
	condition := stampCondition(conditionWakeApplied, result, television.Metadata.Generation, television.Status.Conditions, n.now())
	n.mutex.Lock()
	n.woken.done = wakeKeyOf(television)
	n.woken.unwritten = &wakeRecord{television: *television, condition: condition}
	n.mutex.Unlock()
	n.writeWake()
}

// supersede records a wake that a new generation of spec.power stopped,
// so no later pass runs it, and writes its one line. A wake that ended
// on its own before the cancel reached it keeps its own result.
func (n *cecNode) supersede(job *wakeJob, television *Television) {
	n.mutex.Lock()
	ended := n.woken.done == job.key
	n.mutex.Unlock()
	if ended {
		return
	}
	message := fmt.Sprintf("generation %d of spec.power asks %s, so the wake stopped, and the adapter on %s sends nothing more for it",
		television.Metadata.Generation, television.Spec.Power, n.machine)
	fmt.Fprintf(n.log, "%s; %s\n", wakeWords(&job.television), message)
	n.record(&job.television, verdict{ConditionFalse, reasonSuperseded, message})
}

// wakeTV writes the started mark, runs the wake, and records the
// result. A mark the API server refuses sends nothing, and the next
// pass tries the wake again, because without the mark a restart would
// not know the wake had begun.
func (n *cecNode) wakeTV(ctx context.Context, job *wakeJob, own cec.LogicalAddress, physical cec.PhysicalAddress) {
	asks := wakeWords(&job.television)
	mark := stampCondition(conditionWakeApplied, verdict{ConditionUnknown, reasonWaking,
		fmt.Sprintf("the adapter on %s started the wake", n.machine)}, job.television.Metadata.Generation, job.television.Status.Conditions, n.now())
	if err := ApplyTelevisionWake(n.client, &job.television, n.machine, job.television.Status.Session.WokeAt, mark); err != nil {
		fmt.Fprintf(os.Stderr, "writing the started mark of Television %s's wake: %v\n", job.television.Metadata.Name, err)
		n.retryLater()
		return
	}
	job.television.Status.Conditions = []Condition{mark}
	fmt.Fprintf(n.log, "%s; the adapter on %s starts the wake\n", asks, n.machine)
	result := n.runWake(ctx, job, own, physical)
	if result.stopped && job.superseded.Load() {
		return
	}
	fmt.Fprintf(n.log, "%s; %s\n", asks, result.log)
	if result.stopped {
		result.verdict = verdict{ConditionFalse, reasonStopped, result.log}
	}
	n.record(&job.television, result.verdict)
}

// writeWake writes a result the API server has not accepted yet. A
// Television that is gone, or that was created again under the same
// name, refuses the write for good, so the result is dropped.
func (n *cecNode) writeWake() {
	n.mutex.Lock()
	record := n.woken.unwritten
	n.mutex.Unlock()
	if record == nil {
		return
	}
	err := ApplyTelevisionWake(n.client, &record.television, n.machine, record.television.Status.Session.WokeAt, record.condition)
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing the wake of Television %s: %v\n", record.television.Metadata.Name, err)
		n.retryLater()
	}
	if err == nil || err == apiclient.ErrNotFound || err == apiclient.ErrConflict {
		n.mutex.Lock()
		if n.woken.unwritten == record {
			n.woken.unwritten = nil
		}
		n.mutex.Unlock()
	}
}

// runWake wakes the TV the way spec.power wakes it, then sends Image
// View On and Active Source, guards the claim, and reads the TV's power
// once more at the end of the guard.
func (n *cecNode) runWake(ctx context.Context, job *wakeJob, own cec.LogicalAddress, physical cec.PhysicalAddress) powerResult {
	session := job.television.Status.Session
	began := time.Now()
	budget := &n.woken.budget
	power := n.confirmPower(ctx, budget, job.key, own, cec.PowerOn)
	if power.stopped {
		power.log = fmt.Sprintf("the wake stopped after %s, before it sent Active Source", elapsed(time.Since(began)))
		if n.sendsFor(budget, job.key) > 0 {
			power.log = fmt.Sprintf("the adapter on %s sent Image View On to the TV, and the wake stopped after %s", n.machine, elapsed(time.Since(began)))
		}
		return power
	}
	// A TV that already reports On got no Image View On from the power
	// step, and the claim sends it one with its Active Source, so the
	// line states the report alone.
	if power.verdict.status == ConditionTrue && n.sendsFor(budget, job.key) == 0 {
		power.verdict.message = "the TV already reported On"
		power.log = power.verdict.message
	}
	claim := n.claimSource(ctx, job, own, physical, session.Display)
	if claim.stopped {
		claim.log = fmt.Sprintf("%s; %s", power.log, claim.log)
		return claim
	}
	result := powerResult{verdict: claim.verdict, log: fmt.Sprintf("%s; %s", power.log, claim.log)}
	result.verdict.message = power.verdict.message + "; " + claim.verdict.message
	if power.verdict.status != ConditionTrue {
		result.verdict.status, result.verdict.reason = power.verdict.status, power.verdict.reason
	}
	if last, err := n.askPower(own); err == nil && last != cec.PowerOn {
		reported := "did not answer Give Device Power Status"
		if last != cec.PowerUnknown {
			reported = "reported " + last.String()
		}
		words := fmt.Sprintf("at the end of the guard the TV %s", reported)
		result.verdict = verdict{ConditionFalse, reasonUnconfirmed, result.verdict.message + "; " + words}
		result.log += "; " + words
	}
	result.log += fmt.Sprintf("; the wake ended after %s", elapsed(time.Since(began)))
	return result
}
