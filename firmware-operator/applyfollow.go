package main

// The operator follows each apply pod (applypod.go) from its start to
// its end. `scan apply` writes nothing until the operator sends the pod
// the signal to write. So any end of the pod before the signal is
// Failed, and the machine leaves its reboot turn without a reboot.
// After the signal, only a result that the pod reports says whether it
// wrote, and any other end is Uncertain (apply.go).
//
// A pod that reports CatalogMoved found that its jcat lists a different
// file than the machine's scan read. It stopped during its prepare, so
// it wrote nothing, and no person needs to act. The operator reads the
// jcat again, deletes the apply's record, and starts the apply again
// with the new file.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// createApplyPod creates an apply pod. A 409 Conflict means the pod
// already exists, which is the state the caller needs, so it is not an error.
func (o *operator) createApplyPod(p pod) error {
	err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(p), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// requestWhenReady creates the Disruption that requests the reboot turn
// of a machine whose apply pod is Ready, which means the pod finished
// its prepare. It does nothing for a pod that has ended, because
// applyEnds fails the record. A pod that is gone before it was Ready
// wrote nothing, so the machine needs no turn and the step is Failed.
// It reads the pod from the API server before it treats the pod as
// gone, because the watch can lag behind a pod that the operator
// created a moment ago.
func (o *operator) requestWhenReady(f *firmware, r applyRecord, pods []pod) (*applyStep, error) {
	name := applyPodName(attemptOfApply(f.Metadata.Name, r))
	if i := slices.IndexFunc(pods, func(p pod) bool { return p.Metadata.Name == name }); i >= 0 {
		p := pods[i]
		if p.Metadata.DeletionTimestamp != "" || p.over() || !p.ready() {
			return nil, nil
		}
		d := buildDisruption(f, r.Machine, updateSubject(f, r.Machine))
		if err := o.createDisruption(d); err != nil {
			return nil, fmt.Errorf("creating the Disruption for the reboot turn of %s: %w", r.Machine, err)
		}
		r.Disruption = d.Metadata.Name
		s := step(r, resultWaiting, fmt.Sprintf("the apply pod %s is ready to write, and the Disruption %s requests the machine's reboot turn, so the operator signals the write after machine-operator drains the machine",
			name, r.Disruption), resultWaiting)
		return &s, nil
	}
	gone, err := o.podGone(name)
	if err != nil || !gone {
		return nil, err
	}
	s := step(r, resultFailed, "the apply pod no longer exists and did not finish preparing, so nothing was written and the operator created no Disruption", resultWaiting)
	return &s, nil
}

// signal sends the signal to write to the apply pod of a machine that
// holds its reboot turn, and returns a Writing step. It does nothing for
// a pod that has ended or is being deleted, which applyEnds and the next
// pass handle. A pod that already has the signal, because the operator
// restarted after it sent it, gets no second one, so the deadline keeps
// its original start.
func (o *operator) signal(f *firmware, r applyRecord, pods []pod, catalogs map[string]*catalog, now time.Time) (*applyStep, error) {
	name := applyPodName(attemptOfApply(f.Metadata.Name, r))
	i := slices.IndexFunc(pods, func(p pod) bool { return p.Metadata.Name == name })
	if i >= 0 {
		p := pods[i]
		switch {
		case p.Metadata.DeletionTimestamp != "" || p.over():
			return nil, nil
		case !signalOf(p).IsZero():
			s := step(r, resultWriting, fmt.Sprintf("the machine is drained, and the operator sent the apply pod %s the signal to write the append", name), resultWaiting)
			return &s, nil
		}
	}
	patch := mustJSON(map[string]any{"metadata": map[string]any{"annotations": signalAnnotations(now)}})
	err := o.client.Request(http.MethodPatch, podPath(o.namespace, name), "application/merge-patch+json", patch, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return o.startWithSignal(f, r, catalogs, now)
	}
	if err != nil {
		return nil, fmt.Errorf("patching the apply pod %s with the signal to write: %w", name, err)
	}
	s := step(r, resultWriting, fmt.Sprintf("the machine is drained, and the operator sent the apply pod %s the signal to write the append", name), resultWaiting)
	return &s, nil
}

// startWithSignal creates the apply pod again, with the signal already
// set, when the pod that was waiting is gone. A drain that does not
// skip the pod evicts it, and nothing was written, because the pod had
// no signal. The new pod prepares on the drained machine and writes as
// soon as the prepare ends.
func (o *operator) startWithSignal(f *firmware, r applyRecord, catalogs map[string]*catalog, now time.Time) (*applyStep, error) {
	rh, rehearsed := rehearsalRecord(f.Status.Rehearsals, r.Machine)
	if !rehearsed || rh.Append == nil || rh.Append.SHA256 != r.SHA256 {
		s := step(r, resultFailed, "the machine's rehearsal no longer records the append that the Disruption was created for, so nothing was written", resultWaiting)
		return &s, nil
	}
	c := catalogs[rh.Catalog]
	if c == nil || c.Status.SHA256 == "" {
		s := step(r, resultFailed, fmt.Sprintf("the FirmwareCatalog %s has no current file for the apply to load, so nothing was written", rh.Catalog), resultWaiting)
		return &s, nil
	}
	p := buildApplyPod(f, attemptOfApply(f.Metadata.Name, r), rh, c, o.discovery.scanImage, o.namespace, now)
	if err := o.createApplyPod(p); err != nil {
		return nil, fmt.Errorf("creating the apply pod again for %s: %w", r.Machine, err)
	}
	s := step(r, resultWriting, fmt.Sprintf("the machine is drained, and the apply pod that waited no longer exists, so the apply pod %s starts with the signal to write the append to %s",
		p.Metadata.Name, rh.Append.Variable), resultWaiting)
	return &s, nil
}

// applyEnds follows a Firmware's apply pods. It returns the steps of
// the pods that ended, the pods to delete once their results are
// written, and the earliest deadline among the applies that are
// writing. A Writing record whose pod is gone ends Uncertain. A Waiting
// record whose pod is gone keeps waiting for its turn, and signal
// creates the pod again.
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
		result, decided, deadline, err := o.judgeApply(p, signalSent(f, a, p), now)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !decided {
			due = earliest(due, deadline)
			continue
		}
		if result.Result == resultCatalogMoved {
			if rh, rehearsed := rehearsalRecord(f.Status.Rehearsals, a.machine); rehearsed {
				o.catalogMoved(rh.Catalog)
			}
			result.Message += ", so the operator reads the jcat again and starts the apply again"
		}
		r := applyRecord{Machine: a.machine, Boot: a.boot, Version: a.version, CabinetSHA256: a.cabinet, SHA256: a.sha256}
		// When no record in the status matches the pod, the step leaves
		// Capsule false. The reboot then uses the BootNext that `liken`
		// sets and not the one that fwupd set, so a capsule runs only for
		// a record that staged it.
		if recorded, found := applyRecordOf(f.Status.Applies, a.machine); found && a.is(recorded) {
			r.Disruption, r.Capsule = recorded.Disruption, recorded.Capsule
		}
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
			steps = append(steps, step(r, resultUncertain,
				"the apply pod no longer exists, and the operator cannot tell whether it wrote, so the machine reboots and its next scan reads the version that the firmware has", resultWriting))
		}
	}
	return steps, finished, due, errors.Join(problems...)
}

// writeDeadline returns the earliest deadline of a Writing record that
// is still in the future, so that the operator schedules a pass for it.
// No event arrives when a deadline passes. applyEnds reads deadlines from
// the pods, but the pass that sends the signal has a copy of the pod
// from before the signal, so this function reads them from the records.
// A record's time is the time of the signal or later, so the scheduled
// pass never runs before the pod's deadline. A deadline in the past is
// left out, so a pod that the operator cannot judge yet waits for its
// next change and does not run the pass again in a loop.
func writeDeadline(records []applyRecord, now time.Time) time.Time {
	var due time.Time
	for _, r := range records {
		if at := parseTime(r.Time).Add(scanDeadline); r.Result == resultWriting && at.After(now) {
			due = earliest(due, at)
		}
	}
	return due
}

// signalSent returns when the operator sent the apply pod the signal to
// write, or the zero time when it has not. The watch's copy of the pod
// can lag behind the patch that sent the signal, so a Writing record of
// the pod's attempt also counts as the signal, at the time of the
// record.
func signalSent(f *firmware, a applyAttempt, p pod) time.Time {
	if sent := signalOf(p); !sent.IsZero() {
		return sent
	}
	if r, found := applyRecordOf(f.Status.Applies, a.machine); found && a.is(r) && r.Result == resultWriting {
		return parseTime(r.Time)
	}
	return time.Time{}
}

// judgeApply returns the result of an apply pod, as judgeRehearsal does
// for a rehearsal. sent is when the operator sent the signal to write,
// or the zero time. After the signal, a missed deadline, a crash, a log
// with no result, and a result that the operator does not recognize all
// end Uncertain, because the pod may have written before it stopped.
// The deadline counts from the signal, because a pod can wait for its
// turn much longer than a write takes.
func (o *operator) judgeApply(p pod, sent time.Time, now time.Time) (applyResult, bool, time.Time, error) {
	if sent.IsZero() {
		return o.judgeUnsignalled(p)
	}
	uncertain := applyResult{Result: resultUncertain}
	due := sent.Add(scanDeadline)
	if !p.over() {
		if now.Before(due) {
			return applyResult{}, false, due, nil
		}
		uncertain.Message = fmt.Sprintf("the apply pod did not finish within %s of the signal to write", scanDeadline)
		return uncertain, true, time.Time{}, nil
	}
	log, err := o.readPodLog(p, applyContainer)
	if errors.Is(err, apiclient.ErrNotFound) {
		return applyResult{}, false, time.Time{}, nil
	}
	if err != nil && p.Status.Phase == podSucceeded {
		if now.Before(due) {
			return applyResult{}, false, time.Time{}, fmt.Errorf("reading the log of the apply pod %s: %w", p.Metadata.Name, err)
		}
		uncertain.Message = "the operator cannot read the log of the apply pod: " + err.Error()
		return uncertain, true, time.Time{}, nil
	}
	reported, why := applyResultOf(p, log)
	if why != "" {
		uncertain.Message = why
		return uncertain, true, time.Time{}, nil
	}
	return reported, true, time.Time{}, nil
}

// judgeUnsignalled returns the end of an apply pod that never got the
// signal. The pod wrote nothing, so the log only explains the end, and
// a log that cannot be read does not change the result.
func (o *operator) judgeUnsignalled(p pod) (applyResult, bool, time.Time, error) {
	if !p.over() {
		return applyResult{}, false, time.Time{}, nil
	}
	log, err := o.readPodLog(p, applyContainer)
	if errors.Is(err, apiclient.ErrNotFound) {
		return applyResult{}, false, time.Time{}, nil
	}
	reported, why := applyResultOf(p, log)
	switch {
	case err != nil:
		why = "the operator cannot read the log of the apply pod: " + err.Error()
	case why == "" && (reported.Result == resultFailed || reported.Result == resultCatalogMoved):
		return reported, true, time.Time{}, nil
	case why == "":
		why = fmt.Sprintf("the apply pod reported the result %q", reported.Result)
	}
	return applyResult{Result: resultFailed, Message: "the apply pod ended before the signal to write, so nothing was written: " + why}, true, time.Time{}, nil
}

// applyResultOf reads the log of an apply pod that ended. It returns
// the result that the pod reported, or the reason why the log has none.
// The pod reports Uncertain itself when the firmware accepted some of
// its writes and not the rest. The progress line that the pod prints
// before it waits has no result, so it never counts as the result.
// CatalogMoved is a valid result after the signal too, because a pod
// that the operator started with the signal prepares after the signal.
func applyResultOf(p pod, log string) (applyResult, string) {
	result, last, found := parseResultLine[applyResult](log, func(r applyResult) string { return r.Result })
	switch {
	case !found && last != "":
		return applyResult{}, "the apply pod wrote no result, and its last log line is: " + last
	case !found:
		return applyResult{}, podFailure(p, "apply", applyContainer)
	case slices.Contains([]string{resultWritten, resultUncertain, resultRefused, resultFailed, resultCatalogMoved}, result.Result):
		return result, ""
	}
	return applyResult{}, fmt.Sprintf("the apply pod reported the result %q, which the operator does not recognize", result.Result)
}

// retireApplyPods deletes each apply pod that is waiting for the signal
// when no Waiting or Writing record matches it, for example because its
// approval was withdrawn or its machine is no longer pending. It runs
// after the status write, against the records that the write left. A
// pod with the signal may be writing, so it is never deleted before it
// ends. The watch's copy can lag behind the patch that sent the signal,
// so the pod is read again from the API server before the delete.
func (o *operator) retireApplyPods(records []applyRecord, pods []pod) error {
	var problems []error
	for _, p := range pods {
		a := applyOf(p)
		if p.Metadata.DeletionTimestamp != "" || p.over() || !signalOf(p).IsZero() ||
			slices.ContainsFunc(records, func(r applyRecord) bool {
				return a.is(r) && (r.Result == resultWaiting || r.Result == resultWriting)
			}) {
			continue
		}
		current, err := apiclient.Get[pod](o.client, podPath(p.Metadata.Namespace, p.Metadata.Name))
		if errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("reading the apply pod %s: %w", p.Metadata.Name, err))
			continue
		}
		if signalOf(*current).IsZero() {
			problems = append(problems, o.remove(p))
		}
	}
	return errors.Join(problems...)
}
