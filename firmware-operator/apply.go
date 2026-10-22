package main

// Once an approved Secure Boot database update passed its rehearsal on
// a machine's current boot, the operator applies it on that machine,
// inside a reboot turn that liken grants (disruption.go). The steps,
// each one in its own pass:
//
//  1. The operator files a Disruption with spec.hold, and records the
//     machine as Waiting.
//  2. machine-operator drains the machine and sets Held. The operator
//     starts the apply pod (applypod.go), and records Writing.
//  3. The pod ends. Written removes spec.hold, so the turn reboots the
//     machine. Refused and Failed delete the Disruption, so the machine
//     leaves its turn without a reboot it does not need.
//  4. The machine's next scan reads the approved version, the machine
//     is no longer pending, and Approved moves to Installed (approval.go).
//
// status.applies holds one record for each pending machine that asked
// for a turn, and the Applied condition sums them. Every decision reads
// the records, the apply pods, and the Disruptions, so an operator that
// restarts at any step takes up where the last one stopped. A write
// cannot be undone, so nothing here writes again on its own: a Refused
// or Failed record stays until a person withdraws the approval and
// approves it again, which removes the record.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// applyRecord is one machine's record in status.applies.
type applyRecord struct {
	Machine       string `json:"machine"`
	Boot          string `json:"boot"`
	Version       string `json:"version"`
	CabinetSHA256 string `json:"cabinetSHA256"`
	SHA256        string `json:"sha256"`
	Disruption    string `json:"disruption"`
	Result        string `json:"result"`
	Message       string `json:"message,omitempty"`
	Time          string `json:"time"`
}

// The results of an apply, beside resultRefused and resultFailed.
const (
	resultWaiting = "Waiting"
	resultWriting = "Writing"
	resultWritten = "Written"
)

// live reports whether a record holds the machine's turn, or a write
// that waits for the machine's reboot.
func (r applyRecord) live() bool {
	return r.Result == resultWaiting || r.Result == resultWriting || r.Result == resultWritten
}

// appliedCondition is the condition of an approval's applies.
const appliedCondition = "Applied"

// The reasons of Applied. Written, Refused, and Failed are the results'.
const reasonApplying = "Applying"

// The reasons of the Events that change no condition.
const (
	reasonApplyRequested = "ApplyRequested"
	reasonApplyStarted   = "ApplyStarted"
	reasonApplyWritten   = "ApplyWritten"
	reasonApplyRefused   = "ApplyRefused"
	reasonApplyFailed    = "ApplyFailed"
)

// applyStep is one change of a machine's record that a pass decided
// before its status write: the record after the change, the results of
// the record it replaces, and its Event. A step with no from makes the
// machine's first record.
type applyStep struct {
	record applyRecord
	from   []string
	notice notice
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
	case resultWriting:
		n = notice{reason: reasonApplyStarted, message: about}
	case resultWritten:
		n = notice{reason: reasonApplyWritten, message: about}
	case resultRefused:
		n.reason = reasonApplyRefused
	}
	return applyStep{record: r, from: from, notice: n}
}

// advanceApplies decides the steps of each pending machine that its
// record and its turn allow: a request for a machine whose rehearsal
// passed, the apply pod of a machine whose turn holds, and the failure
// of a machine that left its turn or rebooted with the old version.
func (o *operator) advanceApplies(f *firmware, pending map[string]bool, w observed, catalogs map[string]*catalog) ([]applyStep, error) {
	var steps []applyStep
	var problems []error
	for _, name := range sortedKeys(pending) {
		var s *applyStep
		var err error
		r, held := currentApply(f, name)
		switch {
		case !held:
			s, err = o.requestTurn(f, name, w)
		case r.Result == resultWaiting:
			s, err = o.followTurn(f, r, catalogs)
		case r.Result == resultWritten:
			s = checkReboot(f, r, w)
		}
		if s != nil {
			steps = append(steps, *s)
		}
		problems = append(problems, err)
	}
	return steps, errors.Join(problems...)
}

// requestTurn files the request of a machine whose rehearsal passed on
// the boot it runs now. A cluster with no Disruption kind has no turn
// to ask for, and the machine waits.
func (o *operator) requestTurn(f *firmware, machine string, w observed) (*applyStep, error) {
	u := f.Spec.Update
	rh, rehearsed := rehearsalRecord(f.Status.Rehearsals, machine)
	m, known := w.fleet[machine]
	if o.disruptions.Absent || !rehearsed || rh.Result != resultPassed || rh.Append == nil || !known ||
		rh.Boot != m.Status.Boot.Time || rh.Version != u.Version || rh.CabinetSHA256 != u.Cabinet.SHA256 {
		return nil, nil
	}
	what := u.Component
	if e, offered := offeredEntry(f.Status.Machines, machine, u); what == "" && offered {
		what = e.Device
	}
	d := buildDisruption(f, machine, what)
	if err := o.fileDisruption(d); err != nil {
		return nil, fmt.Errorf("requesting the turn of %s: %w", machine, err)
	}
	r := applyRecord{Machine: machine, Boot: rh.Boot, Version: u.Version, CabinetSHA256: u.Cabinet.SHA256,
		SHA256: rh.Append.SHA256, Disruption: d.Metadata.Name}
	s := step(r, resultWaiting, fmt.Sprintf("the Disruption %s asks for the machine's reboot turn, which holds after the drain for the write", d.Metadata.Name))
	return &s, nil
}

// followTurn answers the step of a machine that waits for its turn:
// the apply pod once machine-operator holds the turn, and a failure
// when the turn ended with no hold. A machine-operator that does not
// support spec.hold reboots the machine at once and marks the request
// Complete, and the operator then writes nothing.
func (o *operator) followTurn(f *firmware, r applyRecord, catalogs map[string]*catalog) (*applyStep, error) {
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
		return o.startApply(f, r, catalogs)
	default:
		return nil, nil
	}
	return &s, nil
}

// startApply creates the apply pod of a machine whose turn holds. A pod
// that exists under the name answers 409, which is the same result.
func (o *operator) startApply(f *firmware, r applyRecord, catalogs map[string]*catalog) (*applyStep, error) {
	rh, rehearsed := rehearsalRecord(f.Status.Rehearsals, r.Machine)
	if !rehearsed || rh.Append == nil || rh.Append.SHA256 != r.SHA256 {
		s := step(r, resultFailed, "the machine's rehearsal no longer records the append that the turn was asked for, so nothing was written", resultWaiting)
		return &s, nil
	}
	c := catalogs[rh.Catalog]
	if c == nil || c.Status.SHA256 == "" {
		s := step(r, resultFailed, fmt.Sprintf("the FirmwareCatalog %s has no current file for the apply to load, so nothing was written", rh.Catalog), resultWaiting)
		return &s, nil
	}
	a := attemptOfApply(f.Metadata.Name, r)
	p := buildApplyPod(f, a, rh, c, o.discovery.scanImage, o.namespace)
	err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(p), nil)
	if err != nil && !errors.Is(err, apiclient.ErrConflict) {
		return nil, fmt.Errorf("starting the apply on %s: %w", r.Machine, err)
	}
	s := step(r, resultWriting, fmt.Sprintf("the machine is drained, and the apply pod %s writes the append to %s", p.Metadata.Name, rh.Append.Variable), resultWaiting)
	return &s, nil
}

// checkReboot fails a written record when the machine booted again and
// a scan of the new boot still offers the approved version, so the
// firmware did not keep the append. A scan from before the reboot is
// older than the record, and decides nothing.
func checkReboot(f *firmware, r applyRecord, w observed) *applyStep {
	m, known := w.fleet[r.Machine]
	e, offered := offeredEntry(f.Status.Machines, r.Machine, f.Spec.Update)
	if !known || m.Status.Boot.Time == r.Boot || !offered || !parseTime(e.Time).After(parseTime(r.Time)) {
		return nil
	}
	s := step(r, resultFailed, fmt.Sprintf("the machine rebooted after the write, and its next scan still reads version %s, not %s", e.Version, r.Version), resultWritten)
	return &s
}

// applyEnds follows a Firmware's apply pods, and answers the steps of
// those that ended, the pods to delete once their results are written,
// and the earliest deadline of an apply in progress. A Writing record
// whose pod is gone ends Failed.
func (o *operator) applyEnds(f *firmware, pods []pod, now time.Time) ([]applyStep, []pod, time.Time, error) {
	var steps []applyStep
	var finished []pod
	var problems []error
	var due time.Time
	busy := map[string]bool{}
	for _, p := range pods {
		a := applyOf(p)
		busy[a.machine] = true
		if p.Metadata.DeletionTimestamp != "" {
			continue
		}
		result, decided, deadline, err := o.judgeApply(p, now)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !decided {
			due = earliest(due, deadline)
			continue
		}
		r := applyRecord{Machine: a.machine, Boot: a.boot, Version: a.version, CabinetSHA256: a.cabinet, SHA256: a.sha256,
			Disruption: disruptionName(a.firmware, a.machine)}
		steps = append(steps, step(r, result.Result, result.Message, resultWaiting, resultWriting))
		finished = append(finished, p)
	}
	for _, r := range f.Status.Applies {
		if r.Result != resultWriting || busy[r.Machine] {
			continue
		}
		gone, err := o.podGone(applyPodName(attemptOfApply(f.Metadata.Name, r)))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if gone {
			steps = append(steps, step(r, resultFailed,
				"the apply pod is gone, and the operator cannot tell whether it wrote; withdraw the approval and approve it again to try again", resultWriting))
		}
	}
	return steps, finished, due, errors.Join(problems...)
}

// judgeApply decides the result of an apply pod, the way
// judgeRehearsal decides a rehearsal's.
func (o *operator) judgeApply(p pod, now time.Time) (applyResult, bool, time.Time, error) {
	failed := applyResult{Result: resultFailed}
	due := parseTime(p.Metadata.CreationTimestamp).Add(scanDeadline)
	switch p.Status.Phase {
	case podSucceeded, podFailed:
	default:
		if now.Before(due) {
			return applyResult{}, false, due, nil
		}
		failed.Message = fmt.Sprintf("the apply pod did not finish within %s of its creation", scanDeadline)
		return failed, true, time.Time{}, nil
	}
	log, err := o.readPodLog(p, applyContainer)
	if errors.Is(err, apiclient.ErrNotFound) {
		return applyResult{}, false, time.Time{}, nil
	}
	if err != nil && p.Status.Phase == podSucceeded {
		if now.Before(due) {
			return applyResult{}, false, time.Time{}, fmt.Errorf("reading the log of the apply pod %s: %w", p.Metadata.Name, err)
		}
		failed.Message = "the operator could not read the apply's log: " + err.Error()
		return failed, true, time.Time{}, nil
	}
	result, last, found := parseResultLine[applyResult](log, func(r applyResult) string { return r.Result })
	switch {
	case !found && last != "":
		failed.Message = "the apply wrote no result; its last line was: " + last
	case !found:
		failed.Message = podFailure(p, "apply", applyContainer)
	case result.Result == resultWritten || result.Result == resultRefused || result.Result == resultFailed:
		return result, true, time.Time{}, nil
	default:
		failed.Message = fmt.Sprintf("the apply answered the result %q, which the operator does not know", result.Result)
	}
	return failed, true, time.Time{}, nil
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
		next := s.record
		next.Time = stamp(now)
		records[next.Machine] = next
		notices = append(notices, s.notice)
	}
	var out []applyRecord
	for _, name := range sortedKeys(records) {
		out = append(out, records[name])
	}
	return out, notices
}

// applied answers the Applied condition over the pending machines'
// records, and false when no pending machine has asked for a turn. A
// refusal outweighs a failure, and both outweigh a machine that waits
// or writes. A pending machine with no record waits for its rehearsal.
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

// settleTurns brings each of a Firmware's requests in line with the
// records that its status write left: a written record's request loses
// spec.hold, so the turn reboots the machine, and a request that no
// live record names is deleted while it holds a turn or after the turn
// ended. A request whose hold was removed and whose reboot has not
// happened stays, so a written update gets its reboot even when the
// record left first.
func (o *operator) settleTurns(records []applyRecord, requests []disruption) error {
	live := map[string]applyRecord{}
	for _, r := range records {
		if r.live() {
			live[r.Disruption] = r
		}
	}
	var problems []error
	for _, d := range requests {
		r, named := live[d.Metadata.Name]
		switch {
		case d.Metadata.DeletionTimestamp != "":
		case named && r.Result == resultWritten && d.Spec.Hold:
			problems = append(problems, o.releaseHold(d.Metadata.Name))
		case !named && (d.Spec.Hold || d.complete()):
			problems = append(problems, o.withdraw(d.Metadata.Name))
		}
	}
	return errors.Join(problems...)
}
