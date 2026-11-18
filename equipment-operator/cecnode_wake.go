package main

// The wake of a Television's session. When a Receiver's session wakes
// the room, the Deployment writes the time to the Television's
// status.session.wokeAt. The adapter that speaks for the session's
// Display then wakes the TV and makes that Display the active source,
// at most once for each wokeAt. Before it sends anything, the adapter
// writes that wokeAt to status.wokeAt as a started mark, so a node
// workload that restarts during or after the wake sends nothing more
// for it.
//
// A streaming player on the same bus can claim Active Source for
// itself when the room wakes, and then the TV and the receiver show
// that player. After its own Active Source, the adapter listens for
// cecWakeGuard and sends Active Source again when another source
// claims the input. The guard and the limit on re-claims keep the
// adapter from fighting a person who switches the input on purpose. A
// claim that arrives after either one is left alone, and a route that
// a person changes through the TV or a switch ends the guard at once
// (cecnode_source.go).
//
// When a receiver is on the Display's path, the wake also sends the
// receiver System Audio Mode Request, once, after the guard
// (cecnode_systemaudio.go).
//
// The node workload acts only on a wake that arrives while it runs. A
// wokeAt that is already in the status when the node workload starts
// is not acted on, so a restart never wakes the room again.

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The wake's bounds. cecWakeGuard is how long the adapter listens for
// another source's claim after its first Active Source, and
// cecWakeReclaims is how many times it sends Active Source again in
// that time. A claim often arrives together with other messages from
// the claiming device, so the adapter waits cecWakeSettle after a
// claim before it sends its own.
//
// cecWakeFresh is how long after the node workload first sees a wake
// that the wake may still start. A wake that waited longer, for
// spec.power to be applied or for the adapter to join the bus, sends
// nothing, because the room may have gone dark since. The node
// workload measures that time on its own clock, because the clock of
// the machine that wrote wokeAt can differ from it.
//
// cecWakeGuard and cecWakeSettle are variables so that the tests on
// the kernel's vivid driver, which run on the real clock, can shorten
// them.
var (
	cecWakeGuard  = 30 * time.Second
	cecWakeSettle = 2 * time.Second
)

const (
	cecWakeReclaims = 2
	cecWakeFresh    = 2 * time.Minute
)

// For cecBootGrace after a wake's first Active Source, claimSource does
// not treat a Set Stream Path from the TV as a person's choice. A TV
// that has just come on can switch inputs by itself, or for a streaming
// player that woke with it, and it sends the same message that its
// source menu sends. A person cannot open that menu and pick an input
// this soon after the TV comes on.
const cecBootGrace = 10 * time.Second

// wakeTimeLayout is the format of status.session.wokeAt: RFC 3339 with
// milliseconds, so two wakes in one second have different values. The
// node workload never parses it. It compares the string to tell one
// wake from another.
const wakeTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// wakeState is the node workload's record of its wakes. The node's
// mutex guards it.
type wakeState struct {
	job *wakeJob
	// done is the last wake that ended, whether it ran, was stopped, or
	// was skipped. unwritten is a result that the API server has not
	// accepted yet.
	done      commandKey
	unwritten *wakeRecord
	// budget counts the Image View On messages and claims counts the
	// Active Source messages that the wake has sent, so a wake that runs
	// again does not start its count over.
	budget commandBudget
	claims commandBudget
	// listed is true after the node workload has listed the Televisions
	// once, and foundAtStart is the wake that the first list returned.
	listed       bool
	foundAtStart commandKey
	// seen is the last wake that arrived while the node workload ran,
	// and seenAt is when the node workload first saw it, on its own
	// clock.
	seen   commandKey
	seenAt time.Time
	// waiting is the wake that has already logged that it waits for
	// spec.power, so the line is written once.
	waiting commandKey
}

// wakeJob is a wake in progress.
type wakeJob struct {
	key        commandKey
	television Television
	cancel     func()
	done       chan struct{}
	// others maps the physical address of each other adapter on the bus
	// to its machine. An Active Source from one of them is a later wake
	// on the same bus, and it ends this wake's guard.
	others map[cec.PhysicalAddress]string
	// superseded is true when a new generation of spec.power stopped the
	// wake. The pass that stopped it writes the result.
	superseded atomic.Bool
	// claimed is true after the wake sends its first Active Source.
	// Until then the adapter answers a Request Active Source with the
	// wake's Display, whichever source the bus last named
	// (cecnode_answer.go).
	claimed atomic.Bool
}

// wakeRecord is one status write of a wake or a standby: the started
// mark or the result. audio is the SystemAudioApplied condition of a
// wake that sent System Audio Mode Request, and nil for any other.
type wakeRecord struct {
	television Television
	condition  Condition
	audio      *Condition
}

func (r *wakeRecord) conditions() []Condition {
	if r.audio == nil {
		return []Condition{r.condition}
	}
	return []Condition{r.condition, *r.audio}
}

func wakeKeyOf(television *Television) commandKey {
	return commandKey{television.Metadata.UID, "wake " + television.Status.Session.WokeAt}
}

// passWake starts, stops, or skips the wake of the bus's Television.
// A wake runs when the session is awake, its wokeAt differs from the
// started mark in status.wokeAt, and this adapter speaks for its
// Display. A new wokeAt, a session that goes to sleep, or an adapter
// that stops speaking for the Display stops a wake in progress.
//
// A wake does not start while a generation of spec.power is not
// applied yet, because the two would send the TV opposite commands at
// once. It starts when status.powerGeneration reaches the generation.
// A generation that arrives while a wake runs is newer than the wake,
// so it stops the wake for good. The check reads
// status.powerGeneration, so it also works when the adapter that
// applies spec.power is on another machine.
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
	prefix := wakeLogPrefix(television)
	switch {
	case key == found:
		n.settle(key)
		fmt.Fprintf(n.log, "%s; the wake was in the status when the node workload started, so the adapter on %s sends nothing for it\n", prefix, n.machine)
	case n.now().Sub(seenAt) > cecWakeFresh:
		message := fmt.Sprintf("the adapter on %s first saw the wake %s ago, which is more than %s, so it sent nothing", n.machine, elapsed(n.now().Sub(seenAt)), elapsed(cecWakeFresh))
		fmt.Fprintf(n.log, "%s; %s\n", prefix, message)
		n.record(television, verdict{ConditionFalse, reasonTooLate, message}, nil)
	case powerPending:
		n.mutex.Lock()
		logged := n.woken.waiting == key
		n.woken.waiting = key
		n.mutex.Unlock()
		if !logged {
			fmt.Fprintf(n.log, "%s; spec.power at generation %d is %s and is not applied yet, so the wake waits for it\n",
				prefix, television.Metadata.Generation, television.Spec.Power)
		}
	default:
		// A wake and a standby send the TV opposite commands, and the
		// wake is the newer request, so stop the standby first.
		n.cancelStandby()
		n.startWake(*television, own, physical, othersOf(bus, n.machine, n.liveness()))
	}
}

// wakeLogPrefix returns the start of every log line about one wake.
func wakeLogPrefix(television *Television) string {
	session := television.Status.Session
	return fmt.Sprintf("Television %s: Player %s's session woke the room at %s", television.Metadata.Name, session.Player, session.WokeAt)
}

func sessionOf(television *Television) *TelevisionSession {
	if television == nil {
		return nil
	}
	return television.Status.Session
}

// othersOf maps the physical address that each other adapter on the
// bus announces to its machine, using the current entries.
func othersOf(bus *CECBus, machine string, live podLiveness) map[cec.PhysicalAddress]string {
	others := map[cec.PhysicalAddress]string{}
	for other, entry := range reportedEntries(bus, live).current {
		if address, err := cec.ParsePhysicalAddress(entry.PhysicalAddress); err == nil && other != machine {
			others[address] = other
		}
	}
	return others
}

// speaksFor reports whether this adapter speaks for a Display on the
// bus, and returns the logical and physical address it sends from. The
// first adapter in spec.adapters that lists the Display and is in
// Control with a logical address speaks for it. The wake's Active
// Source must come from that adapter, because Active Source carries
// its sender's physical address, and that adapter announces the
// Display's. Image View On goes out from the same adapter, because CEC
// One Touch Play is one source that sends Image View On and then
// Active Source.
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
	entries := reportedEntries(bus, n.liveness())
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

// startWake runs a wake as mode work, so a change of mode stops it
// before the device handle leaves Control. It drops an older wake's
// result that the API server has not accepted yet, because writing
// that result later would move status.wokeAt back to the older wake.
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

// cancelWake stops the wake in progress, if there is one, and waits
// until it has ended and written its result.
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

// settle marks a wake as ended in memory, so no later pass runs it.
func (n *cecNode) settle(key commandKey) {
	n.mutex.Lock()
	n.woken.done = key
	n.mutex.Unlock()
}

// record settles a wake and writes its result. audio is the verdict of
// the wake's System Audio Mode Request, or nil when the wake sent none.
// A nil audio makes the write remove the SystemAudioApplied condition
// that an earlier wake left.
func (n *cecNode) record(television *Television, result verdict, audio *verdict) {
	written := &wakeRecord{television: *television,
		condition: stampCondition(conditionWakeApplied, result, television.Metadata.Generation, television.Status.Conditions, n.now())}
	if audio != nil {
		stamped := stampCondition(conditionSystemAudioApplied, *audio, television.Metadata.Generation, television.Status.Conditions, n.now())
		written.audio = &stamped
	}
	n.mutex.Lock()
	n.woken.done = wakeKeyOf(television)
	n.woken.unwritten = written
	n.mutex.Unlock()
	n.writeWake()
}

// supersede records a wake that a new generation of spec.power stopped,
// so no later pass runs it, and logs one line. A wake that had already
// ended before the cancel reached it keeps its own result.
func (n *cecNode) supersede(job *wakeJob, television *Television) {
	n.mutex.Lock()
	ended := n.woken.done == job.key
	n.mutex.Unlock()
	if ended {
		return
	}
	message := fmt.Sprintf("spec.power at generation %d is %s, so the wake stopped, and the adapter on %s sends nothing more for it",
		television.Metadata.Generation, television.Spec.Power, n.machine)
	fmt.Fprintf(n.log, "%s; %s\n", wakeLogPrefix(&job.television), message)
	n.record(&job.television, verdict{ConditionFalse, reasonSuperseded, message}, nil)
}

// wakeTV writes the started mark, runs the wake, and records the
// result. When the API server refuses the mark, wakeTV sends nothing
// and the next pass tries the wake again, because without the mark a
// restarted node workload could not tell that the wake had begun.
//
// The mark repeats the Television's SystemAudioApplied condition from
// the last wake. That keeps the condition while the wake runs, and it
// moves the condition's ownership from another machine's
// field manager to this machine's, so that the result's write can
// remove it when this wake sends no request.
func (n *cecNode) wakeTV(ctx context.Context, job *wakeJob, own cec.LogicalAddress, physical cec.PhysicalAddress) {
	prefix := wakeLogPrefix(&job.television)
	mark := stampCondition(conditionWakeApplied, verdict{ConditionUnknown, reasonWaking,
		fmt.Sprintf("the adapter on %s started the wake", n.machine)}, job.television.Metadata.Generation, job.television.Status.Conditions, n.now())
	marked := []Condition{mark}
	if kept := conditionOf(job.television.Status.Conditions, conditionSystemAudioApplied); kept.Type != "" {
		marked = append(marked, kept)
	}
	if err := ApplyTelevisionWake(n.client, &job.television, n.machine, job.television.Status.Session.WokeAt, marked...); err != nil {
		fmt.Fprintf(os.Stderr, "writing the started mark of Television %s's wake: %v\n", job.television.Metadata.Name, err)
		n.retryLater()
		return
	}
	n.postTelevision(&job.television, mark)
	job.television.Status.Conditions = marked
	fmt.Fprintf(n.log, "%s; the adapter on %s starts the wake\n", prefix, n.machine)
	result := n.runWake(ctx, job, own, physical)
	if result.stopped && job.superseded.Load() {
		return
	}
	fmt.Fprintf(n.log, "%s; %s\n", prefix, result.log)
	if result.stopped {
		result.verdict = verdict{ConditionFalse, reasonStopped, result.log}
	}
	var audio *verdict
	if result.audio != nil && !result.audio.stopped {
		fmt.Fprintf(n.log, "%s; %s\n", prefix, result.audio.log)
		audio = &result.audio.verdict
	}
	n.record(&job.television, result.verdict, audio)
}

// writeWake writes a result that the API server has not accepted yet.
// When the Television is gone, or was created again under the same
// name, the server refuses the write permanently, because the apply
// names the Television's UID, so the result is dropped.
func (n *cecNode) writeWake() {
	n.mutex.Lock()
	record := n.woken.unwritten
	n.mutex.Unlock()
	if record == nil {
		return
	}
	err := ApplyTelevisionWake(n.client, &record.television, n.machine, record.television.Status.Session.WokeAt, record.conditions()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing the wake of Television %s: %v\n", record.television.Metadata.Name, err)
		n.retryLater()
	}
	if err == nil || err == apiclient.ErrNotFound || err == apiclient.ErrConflict {
		n.mutex.Lock()
		settled := n.woken.unwritten == record
		if settled {
			n.woken.unwritten = nil
		}
		n.mutex.Unlock()
		// A pass and the wake's own work can write the same record at
		// the same time. Only the writer that clears it posts the
		// Events, so each Event is posted once.
		if settled && err == nil {
			for _, condition := range record.conditions() {
				n.postTelevision(&record.television, condition)
			}
		}
	}
}

// runWake powers the TV on the way spec.power does, then sends Image
// View On and Active Source, and takes the input back during the
// guard. At the end of the guard it reads the TV's power once more.
// When a receiver is on the Display's path, it then sends that
// receiver System Audio Mode Request.
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
	// When the TV already reported On, confirmPower sent no Image View
	// On. claimSource sends one with Active Source, so the message says
	// only that the TV was already On.
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
	if last, err := n.readTVPower(own); err == nil && last != cec.PowerOn {
		reported := "did not answer Give Device Power Status"
		if last != cec.PowerUnknown {
			reported = "reported " + last.String()
		}
		words := fmt.Sprintf("at the end of the guard the TV %s", reported)
		result.verdict = verdict{ConditionFalse, reasonUnconfirmed, result.verdict.message + "; " + words}
		result.log += "; " + words
	}
	// Send System Audio Mode Request after the guard, because a TV can
	// send its own request as it comes on and move the receiver to the
	// TV's sound input. Send it only when the Display still has the input
	// at the end of the guard (reasonConfirmed): the request switches the
	// receiver to the Display's input, and the adapter must not take the
	// input from a person or from a source that the guard left alone.
	if via := viaOf(&job.television, session.Display); via != nil && claim.verdict.reason == reasonConfirmed {
		audio := n.requestSystemAudio(ctx, own, physical, session.Display, via)
		result.audio = &audio
	}
	result.log += fmt.Sprintf("; the wake ended after %s", elapsed(time.Since(began)))
	return result
}
