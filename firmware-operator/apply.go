package main

// Once an approved Secure Boot database update passed its rehearsal on
// a machine's current boot, the operator applies it on that machine,
// inside a reboot turn that liken grants (disruption.go). The steps,
// each one in its own pass:
//
//  1. The operator starts the apply pod (applypod.go), which prepares
//     the write while the cluster is whole and then waits for the
//     signal to write, and records the machine as Waiting.
//  2. The pod's prepare is done, and its readiness probe turns it
//     Ready. The operator files a Disruption with spec.hold, and names
//     it in the record. A pod that ends or is gone before then asks for
//     no turn.
//  3. machine-operator drains the machine and sets Held. The operator
//     sends the apply pod the signal (applyfollow.go), and records
//     Writing. A pod that is gone by then, such as one that a drain
//     evicted, is started again with the signal.
//  4. The pod ends. Written removes spec.hold, so the turn reboots the
//     machine. Refused and Failed, which wrote nothing, delete the
//     Disruption, so the machine leaves its turn without a reboot it does
//     not need. Uncertain, an end that does not say whether the append
//     was written, or an append that the firmware accepted only in
//     part, removes spec.hold too.
//  5. The machine's next scan reads the approved version, the machine
//     is no longer pending, and Approved moves to Installed (approval.go).
//
// A pod that ends before the signal wrote nothing, so its record is
// Failed and the Disruption is deleted. status.applies holds one record
// for each pending machine that asked for a turn, and the Applied
// condition sums them. Every decision reads the records, the apply pods
// and their annotations, and the Disruptions, so an operator that
// restarts at any step takes up where the last one stopped. A write
// cannot be undone, so nothing here writes again on its own: a Refused
// or Failed record stays until a person withdraws the approval and
// approves it again, which removes the record.
//
// An apply pod with the signal that is gone, that ran past its
// deadline, that crashed, or whose log does not answer may have written
// before it stopped. A
// force-deleted pod's container can even outlive its API object. If
// the operator withdrew the turn, the machine would run with variables
// that its kernel does not enforce until a reboot nobody planned. So an
// uncertain end reboots the machine inside its turn, the same as a
// written one, and the next scan answers whether the firmware took the
// append.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// applyRecord is one machine's record in status.applies.
type applyRecord struct {
	Machine       string `json:"machine"`
	Boot          string `json:"boot"`
	Version       string `json:"version"`
	CabinetSHA256 string `json:"cabinetSHA256"`
	SHA256        string `json:"sha256"`
	// Disruption is empty until the apply pod is Ready and the
	// operator asked for the turn.
	Disruption string `json:"disruption,omitempty"`
	// Capsule marks the apply of a capsule, whose turn reboots with
	// BootNext at fwupd's updater once the capsule is staged. SHA256 is
	// then the digest of the rehearsal's writes.
	Capsule bool   `json:"capsule,omitempty"`
	Result  string `json:"result"`
	Message string `json:"message,omitempty"`
	Time    string `json:"time"`
}

// The results of an apply, beside resultRefused and resultFailed.
const (
	resultWaiting   = "Waiting"
	resultWriting   = "Writing"
	resultWritten   = "Written"
	resultUncertain = "Uncertain"
)

// live reports whether a record holds the machine's turn, or a write
// that waits for the machine's reboot.
func (r applyRecord) live() bool {
	return r.Result == resultWaiting || r.Result == resultWriting || r.rebooting()
}

// keepsBootNext reports whether the record's reboot must boot fwupd's
// updater: a capsule that the apply staged as rehearsed. An uncertain
// capsule reboots with liken's own BootNext, so the capsule does not
// run.
func (r applyRecord) keepsBootNext() bool {
	return r.Capsule && r.Result == resultWritten
}

// rebooting reports whether a record's write, or the write it cannot
// rule out, takes effect at the reboot of its turn.
func (r applyRecord) rebooting() bool {
	return r.Result == resultWritten || r.Result == resultUncertain
}

// appliedCondition is the condition of an approval's applies.
const appliedCondition = "Applied"

// The reasons of Applied. Written, Refused, and Failed are the results'.
const reasonApplying = "Applying"

// The reasons of the Events that change no condition.
const (
	reasonApplyPreparing    = "ApplyPreparing"
	reasonApplyCatalogMoved = "ApplyCatalogMoved"
	reasonApplyRequested    = "ApplyRequested"
	reasonApplyStarted      = "ApplyStarted"
	reasonApplyWritten      = "ApplyWritten"
	reasonApplyRefused      = "ApplyRefused"
	reasonApplyFailed       = "ApplyFailed"
	reasonApplyUncertain    = "ApplyUncertain"
)

// applyStep is one change of a machine's record that a pass decided
// before its status write: the record after the change, the results of
// the record it replaces, and its Event. A step with no from makes the
// machine's first record, and a step that drops removes the record.
type applyStep struct {
	record applyRecord
	from   []string
	notice notice
	drop   bool
}

// applyResult is the line `scan apply` writes.
type applyResult struct {
	Result  string `json:"result"`
	Message string `json:"message"`
}

// currentApply answers a machine's record of the approval that the spec
// holds now. A record of an earlier approval answers nothing.
func currentApply(f *firmware, machine string) (applyRecord, bool) {
	u := f.Spec.Update
	i := slices.IndexFunc(f.Status.Applies, func(r applyRecord) bool { return r.Machine == machine })
	if i < 0 || u == nil || u.Cabinet == nil {
		return applyRecord{}, false
	}
	r := f.Status.Applies[i]
	return r, r.Version == u.Version && r.CabinetSHA256 == u.Cabinet.SHA256
}

// step answers the change of a record to a result.
func step(r applyRecord, result, message string, from ...string) applyStep {
	r.Result, r.Message = result, message
	about := r.Machine + ": " + message
	n := notice{warning: true, reason: reasonApplyFailed, message: about}
	switch result {
	case resultWaiting:
		n = notice{reason: reasonApplyRequested, message: about}
		if r.Disruption == "" {
			n.reason = reasonApplyPreparing
		}
	case resultWriting:
		n = notice{reason: reasonApplyStarted, message: about}
	case resultWritten:
		n = notice{reason: reasonApplyWritten, message: about}
	case resultRefused:
		n.reason = reasonApplyRefused
	case resultUncertain:
		n.reason = reasonApplyUncertain
	case resultCatalogMoved:
		// The apply wrote nothing and the catalog is old, so the record
		// leaves, and the apply starts again on its own once the
		// operator has read the jcat.
		return applyStep{record: r, from: from, notice: notice{reason: reasonApplyCatalogMoved, message: about}, drop: true}
	}
	return applyStep{record: r, from: from, notice: n}
}

// advanceApplies decides the steps of each pending machine that its
// record and its turn allow: the apply pod of a machine whose rehearsal
// passed, the request of a machine whose apply pod is Ready, the signal
// to a machine whose turn holds, and the failure of a machine that left
// its turn or rebooted with the old version.
func (o *operator) advanceApplies(f *firmware, pending map[string]bool, w observed, catalogs map[string]*catalog, now time.Time) ([]applyStep, error) {
	var steps []applyStep
	var problems []error
	for _, name := range sortedKeys(pending) {
		var s *applyStep
		var err error
		r, held := currentApply(f, name)
		switch {
		case !held:
			s, err = o.startApply(f, name, w, catalogs)
		case r.Result == resultWaiting:
			s, err = o.followTurn(f, r, w.applies[f.Metadata.Name], catalogs, now)
		case r.rebooting():
			s = checkReboot(f, r, w)
		}
		if s != nil {
			steps = append(steps, *s)
		}
		problems = append(problems, err)
	}
	return steps, errors.Join(problems...)
}

// startApply starts the apply pod of a machine whose rehearsal passed
// on the boot it runs now. The request for the turn waits until the pod
// is Ready (applyfollow.go). A cluster with no Disruption kind has no
// turn to ask for, and the machine waits, as it does while its
// FirmwareCatalog has no current file for the pod to load.
func (o *operator) startApply(f *firmware, machine string, w observed, catalogs map[string]*catalog) (*applyStep, error) {
	u := f.Spec.Update
	rh, rehearsed := rehearsalRecord(f.Status.Rehearsals, machine)
	m, known := w.fleet[machine]
	if o.disruptions.Absent || !rehearsed || rh.Result != resultPassed || (rh.Append == nil && rh.Capsule == nil) || !known ||
		rh.Boot != m.Status.Boot.Time || rh.Version != u.Version || rh.CabinetSHA256 != u.Cabinet.SHA256 {
		return nil, nil
	}
	c := catalogs[rh.Catalog]
	if c == nil || c.Status.SHA256 == "" {
		return nil, nil
	}
	// An earlier pod of the machine may still stop, and the same attempt
	// has the same name, so a create would answer 409 for a pod that is
	// about to go. The pass waits, and the pod's delete wakes it again.
	if slices.ContainsFunc(w.applies[f.Metadata.Name], func(p pod) bool { return applyOf(p).machine == machine }) {
		return nil, nil
	}
	r := applyRecord{Machine: machine, Boot: rh.Boot, Version: u.Version, CabinetSHA256: u.Cabinet.SHA256}
	if rh.Capsule == nil {
		r.SHA256 = rh.Append.SHA256
	} else {
		// The boot after the updater's comes from the head of BootOrder,
		// with no one-shot to steer it, and liken's init hands BootNext
		// to the updater only where the firmware kept BootOrder through
		// its last reset. A capsule staged on any other machine would
		// wait for a reboot that never runs it.
		r.Capsule, r.SHA256 = true, rh.Capsule.SHA256
		if kept := m.Status.Firmware.BootOrderKept; kept == nil || !*kept {
			s := step(r, resultRefused, "the machine's status.firmware.bootOrderKept is not true, so liken would not keep BootNext for fwupd's updater at the capsule's reboot")
			return &s, nil
		}
	}
	p := buildApplyPod(f, attemptOfApply(f.Metadata.Name, r), rh, c, o.discovery.scanImage, o.namespace, time.Time{})
	if err := o.createApplyPod(p); err != nil {
		return nil, fmt.Errorf("starting the apply on %s: %w", machine, err)
	}
	s := step(r, resultWaiting, fmt.Sprintf("the apply pod %s prepares the write; the operator asks for the machine's reboot turn when the pod is Ready", p.Metadata.Name))
	return &s, nil
}

// followTurn answers the step of a machine that waits for its turn:
// the request once the apply pod is Ready, the signal once
// machine-operator holds the turn, and a failure when the turn ended
// with no hold. A machine-operator that does not support spec.hold
// reboots the machine at once and marks the request Complete, and the
// operator then writes nothing.
func (o *operator) followTurn(f *firmware, r applyRecord, pods []pod, catalogs map[string]*catalog, now time.Time) (*applyStep, error) {
	if r.Disruption == "" {
		return o.requestWhenReady(f, r, pods)
	}
	d, err := o.readDisruption(r.Disruption)
	if err != nil {
		return nil, fmt.Errorf("reading the Disruption %s: %w", r.Disruption, err)
	}
	var s applyStep
	switch {
	case d == nil:
		s = step(r, resultFailed, fmt.Sprintf("the machine left its turn before the hold took effect: the Disruption %s is gone, and nothing was written; withdraw the approval and approve it again to try again", r.Disruption), resultWaiting)
	case d.complete():
		s = step(r, resultFailed, fmt.Sprintf("the machine left its turn before the hold took effect: the Disruption %s is Complete, and nothing was written; a machine-operator that does not hold a turn reboots the machine at once; withdraw the approval and approve it again to try again", r.Disruption), resultWaiting)
	case d.held() && d.Spec.Hold:
		return o.signal(f, r, pods, catalogs, now)
	default:
		return nil, nil
	}
	return &s, nil
}

// checkReboot fails a written or uncertain record when the machine
// booted again and a scan of the new boot still offers the approved
// version, so the firmware does not hold the append. A scan from before
// the reboot is older than the record, and decides nothing.
func checkReboot(f *firmware, r applyRecord, w observed) *applyStep {
	m, known := w.fleet[r.Machine]
	e, offered := offeredEntry(f.Status.Machines, r.Machine, f.Spec.Update)
	if !known || m.Status.Boot.Time == r.Boot || !offered || !parseTime(e.Time).After(parseTime(r.Time)) {
		return nil
	}
	message := fmt.Sprintf("the machine rebooted after the write, and its next scan still reads version %s, not %s", e.Version, r.Version)
	if r.Result == resultUncertain {
		message = fmt.Sprintf("the machine rebooted, and its next scan still reads version %s, not %s, so the append was not written", e.Version, r.Version)
	}
	s := step(r, resultFailed, message, resultWritten, resultUncertain)
	return &s
}

// composeApplies answers the records of the pending machines after the
// steps of this pass, and the Events of each. The steps were decided
// from a copy that can be older than the one held here, after a 409,
// so each step is checked against the held record again.
func composeApplies(f *firmware, pending map[string]bool, steps []applyStep, now time.Time) ([]applyRecord, []notice) {
	u := f.Spec.Update
	if u == nil || u.Version == "" || u.Cabinet == nil {
		return nil, nil
	}
	current := func(r applyRecord) bool {
		return pending[r.Machine] && r.Version == u.Version && r.CabinetSHA256 == u.Cabinet.SHA256
	}
	records := map[string]applyRecord{}
	for _, r := range f.Status.Applies {
		if current(r) {
			records[r.Machine] = r
		}
	}
	var notices []notice
	for _, s := range steps {
		previous, held := records[s.record.Machine]
		switch {
		case !current(s.record):
			continue
		case s.from == nil && held:
			continue
		case s.from != nil && (!held || !attemptOfApply("", s.record).is(previous) || !slices.Contains(s.from, previous.Result)):
			continue
		}
		notices = append(notices, s.notice)
		if s.drop {
			delete(records, s.record.Machine)
			continue
		}
		next := s.record
		next.Time = stamp(now)
		records[next.Machine] = next
	}
	var out []applyRecord
	for _, name := range sortedKeys(records) {
		out = append(out, records[name])
	}
	return out, notices
}

// applied answers the Applied condition over the pending machines'
// records, and false when no pending machine has asked for a turn. A
// refusal outweighs a failure, both outweigh an uncertain write, and
// that outweighs a machine that waits or writes. A pending machine with
// no record waits for its rehearsal.
func applied(records []applyRecord, pending map[string]bool) (condition, bool) {
	if len(pending) == 0 || len(records) == 0 {
		return condition{}, false
	}
	byResult := map[string][]string{}
	for _, name := range sortedKeys(pending) {
		r, held := applyRecordOf(records, name)
		switch {
		case !held || r.Result == resultWaiting || r.Result == resultWriting:
			byResult[reasonApplying] = append(byResult[reasonApplying], name)
		case r.Result == resultWritten:
			byResult[resultWritten] = append(byResult[resultWritten], name)
		default:
			byResult[r.Result] = append(byResult[r.Result], name+": "+r.Message)
		}
	}
	c := condition{Type: appliedCondition}
	switch {
	case len(byResult[resultRefused]) > 0:
		c.Status, c.Reason, c.Message = conditions.False, resultRefused, strings.Join(byResult[resultRefused], "; ")
	case len(byResult[resultFailed]) > 0:
		c.Status, c.Reason, c.Message = conditions.False, resultFailed, strings.Join(byResult[resultFailed], "; ")
	case len(byResult[resultUncertain]) > 0:
		c.Status, c.Reason, c.Message = conditions.Unknown, resultUncertain, strings.Join(byResult[resultUncertain], "; ")
	case len(byResult[reasonApplying]) > 0:
		c.Status, c.Reason, c.Message = conditions.Unknown, reasonApplying,
			"the approval waits for the write on "+strings.Join(byResult[reasonApplying], ", ")
	default:
		c.Status, c.Reason, c.Message = conditions.True, resultWritten,
			"the update is written on "+strings.Join(byResult[resultWritten], ", ")+", and takes effect at each machine's reboot"
	}
	return c, true
}

func applyRecordOf(records []applyRecord, machine string) (applyRecord, bool) {
	i := slices.IndexFunc(records, func(r applyRecord) bool { return r.Machine == machine })
	if i < 0 {
		return applyRecord{}, false
	}
	return records[i], true
}

// updateSubject names what an approval installs on a machine, for the
// reason of its request: the catalog component, or the device.
func updateSubject(f *firmware, machine string) string {
	what := f.Spec.Update.Component
	if e, offered := offeredEntry(f.Status.Machines, machine, f.Spec.Update); what == "" && offered {
		what = e.Device
	}
	return what
}

// settleTurns brings each of a Firmware's requests in line with the
// records that its status write left: a written or uncertain record's
// request loses spec.hold, so the turn reboots the machine, and a
// request that no live record names is deleted while it holds a turn or
// after the turn ended. A request whose hold was removed and whose
// reboot has not happened stays, so a written update gets its reboot
// even when the record left first.
//
// A person can delete the request while the apply pod writes, which
// ends the turn with no reboot. The write takes effect only at a
// reboot, so a written or uncertain record whose machine still runs the
// boot of the write gets its request again, with no hold, and the turn
// reboots the machine under the budgets like any other.
func (o *operator) settleTurns(f *firmware, records []applyRecord, requests []disruption, fleet map[string]machine) error {
	live := map[string]applyRecord{}
	for _, r := range records {
		if r.live() {
			live[r.Disruption] = r
		}
	}
	var problems []error
	filed := map[string]bool{}
	for _, d := range requests {
		filed[d.Metadata.Name] = true
	}
	for _, r := range records {
		m, known := fleet[r.Machine]
		if !r.rebooting() || filed[r.Disruption] || !known || m.Status.Boot.Time != r.Boot {
			continue
		}
		d := buildDisruption(f, r.Machine, updateSubject(f, r.Machine))
		d.Spec.Hold, d.Spec.KeepBootNext = false, r.keepsBootNext()
		if err := o.fileDisruption(d); err != nil {
			problems = append(problems, fmt.Errorf("asking again for the reboot of %s: %w", r.Machine, err))
		}
	}
	for _, d := range requests {
		r, named := live[d.Metadata.Name]
		switch {
		case d.Metadata.DeletionTimestamp != "":
		case named && r.rebooting() && d.Spec.Hold:
			problems = append(problems, o.releaseHold(d.Metadata.Name, r.keepsBootNext()))
		case !named && (d.Spec.Hold || d.complete()):
			problems = append(problems, o.withdraw(d.Metadata.Name))
		}
	}
	return errors.Join(problems...)
}
