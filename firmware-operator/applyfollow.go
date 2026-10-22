package main

// The operator follows each apply pod from its start to its end
// (applypod.go). The signal to write divides the pod's life in two.
// Before the signal the pod cannot have written, because `scan apply`
// reaches the variable store only after the signal, so any end of the
// pod is Failed, and the machine leaves its turn with no reboot. After
// the signal only the pod's own answer says whether it wrote, and every
// other end is Uncertain (apply.go).

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// createApplyPod creates an apply pod. A pod that exists under the name
// answers 409, which is the same result.
func (o *operator) createApplyPod(p pod) error {
	err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(p), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// requestWhenReady files the request for the turn of a machine whose
// apply pod is Ready, which means its prepare is done. A pod that ended
// is left to applyEnds, which fails the record. A pod that is gone
// before it was Ready wrote nothing, and its machine needs no turn. The
// pass asks the API server whether the pod is gone, because the watch
// can lag a pod that the operator created a moment ago.
func (o *operator) requestWhenReady(f *firmware, r applyRecord, pods []pod) (*applyStep, error) {
	name := applyPodName(attemptOfApply(f.Metadata.Name, r))
	if i := slices.IndexFunc(pods, func(p pod) bool { return p.Metadata.Name == name }); i >= 0 {
		p := pods[i]
		if p.Metadata.DeletionTimestamp != "" || p.over() || !p.ready() {
			return nil, nil
		}
		d := buildDisruption(f, r.Machine, updateSubject(f, r.Machine))
		if err := o.fileDisruption(d); err != nil {
			return nil, fmt.Errorf("requesting the turn of %s: %w", r.Machine, err)
		}
		r.Disruption = d.Metadata.Name
		s := step(r, resultWaiting, fmt.Sprintf("the apply pod %s prepared the write, and the Disruption %s asks for the machine's reboot turn, which holds after the drain for the write",
			name, r.Disruption), resultWaiting)
		return &s, nil
	}
	gone, err := o.podGone(name)
	if err != nil || !gone {
		return nil, err
	}
	s := step(r, resultFailed, "the apply pod is gone before it prepared, so nothing was written, and the operator asked for no turn", resultWaiting)
	return &s, nil
}

// signal sends the apply pod of a machine whose turn holds the signal
// to write, and answers the step to Writing. A pod that ended, or whose
// deletion started, is left to applyEnds and to the next pass. A pod
// that already carries the signal, because an operator stopped after it
// sent it, gets no second one, so the deadline keeps its start.
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
		return o.startSignalled(f, r, catalogs, now)
	}
	if err != nil {
		return nil, fmt.Errorf("sending the apply pod %s the signal to write: %w", name, err)
	}
	s := step(r, resultWriting, fmt.Sprintf("the machine is drained, and the operator sent the apply pod %s the signal to write the append", name), resultWaiting)
	return &s, nil
}

// startSignalled starts the apply pod again, with the signal, when the
// one that waited is gone. A drain that does not skip the pod evicts
// it, and nothing was written, because the pod had no signal. The new
// pod prepares on the drained machine and writes at once.
func (o *operator) startSignalled(f *firmware, r applyRecord, catalogs map[string]*catalog, now time.Time) (*applyStep, error) {
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
	p := buildApplyPod(f, attemptOfApply(f.Metadata.Name, r), rh, c, o.discovery.scanImage, o.namespace, now)
	if err := o.createApplyPod(p); err != nil {
		return nil, fmt.Errorf("starting the apply on %s again: %w", r.Machine, err)
	}
	s := step(r, resultWriting, fmt.Sprintf("the machine is drained, and the apply pod that waited is gone, so the apply pod %s starts with the signal to write the append to %s",
		p.Metadata.Name, rh.Append.Variable), resultWaiting)
	return &s, nil
}

// applyEnds follows a Firmware's apply pods, and answers the steps of
// those that ended, the pods to delete once their results are written,
// and the earliest deadline of an apply that writes. A Writing record
// whose pod is gone ends Uncertain. A Waiting record whose pod is gone
// waits for its turn, and signal starts the pod again.
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
		r := applyRecord{Machine: a.machine, Boot: a.boot, Version: a.version, CabinetSHA256: a.cabinet, SHA256: a.sha256}
		if held, found := applyRecordOf(f.Status.Applies, a.machine); found && a.is(held) {
			r.Disruption = held.Disruption
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
				"the apply pod is gone, and the operator cannot tell whether it wrote, so the machine reboots and its next scan reads what the firmware holds", resultWriting))
		}
	}
	return steps, finished, due, errors.Join(problems...)
}

// writeDeadline answers the earliest deadline of a Writing record that
// is still ahead. No event marks a deadline, and the pass that sends the
// signal reads a copy of the pod from before it, so the pass after the
// signal is timed from the records. The record's time is the signal's,
// or later after a restart, so the pass comes no sooner than the
// deadline. A deadline already past is left to the pod's next change,
// so a pod that the operator cannot judge yet does not run the pass
// again and again.
func writeDeadline(records []applyRecord, now time.Time) time.Time {
	var due time.Time
	for _, r := range records {
		if at := parseTime(r.Time).Add(scanDeadline); r.Result == resultWriting && at.After(now) {
			due = earliest(due, at)
		}
	}
	return due
}

// signalSent answers when the operator sent an apply pod the signal, or
// the zero time when it has not. The watch's copy of the pod can lag
// the patch that sent it, so a Writing record of the pod's attempt
// counts as the signal too, from the time of the record.
func signalSent(f *firmware, a applyAttempt, p pod) time.Time {
	if sent := signalOf(p); !sent.IsZero() {
		return sent
	}
	if r, held := applyRecordOf(f.Status.Applies, a.machine); held && a.is(r) && r.Result == resultWriting {
		return parseTime(r.Time)
	}
	return time.Time{}
}

// judgeApply decides the result of an apply pod, the way
// judgeRehearsal decides a rehearsal's. sent is when the operator sent
// the signal to write, or the zero time.
//
// Before the signal the pod waits with no deadline, and any end is
// Failed. After it, only the pod's own answer says whether it wrote.
// Every other end, a deadline, a crash, a log that does not answer, or
// an answer the operator does not know, is Uncertain, because the pod
// may have written before it stopped. The deadline counts from the
// signal, because the pod can wait for its turn far longer than a write
// takes.
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
		uncertain.Message = "the operator could not read the apply's log: " + err.Error()
		return uncertain, true, time.Time{}, nil
	}
	answer, why := answerOf(p, log)
	if why != "" {
		uncertain.Message = why
		return uncertain, true, time.Time{}, nil
	}
	return answer, true, time.Time{}, nil
}

// judgeUnsignalled decides the end of an apply pod that had no signal.
// It wrote nothing, so the log only explains the end, and a log that
// cannot be read changes nothing.
func (o *operator) judgeUnsignalled(p pod) (applyResult, bool, time.Time, error) {
	if !p.over() {
		return applyResult{}, false, time.Time{}, nil
	}
	log, err := o.readPodLog(p, applyContainer)
	if errors.Is(err, apiclient.ErrNotFound) {
		return applyResult{}, false, time.Time{}, nil
	}
	answer, why := answerOf(p, log)
	switch {
	case err != nil:
		why = "the operator could not read the apply's log: " + err.Error()
	case why == "" && answer.Result == resultFailed:
		return answer, true, time.Time{}, nil
	case why == "":
		why = fmt.Sprintf("the apply answered the result %q", answer.Result)
	}
	return applyResult{Result: resultFailed, Message: "the apply pod ended before the signal to write, so nothing was written: " + why}, true, time.Time{}, nil
}

// answerOf reads the log of an apply pod that ended: the pod's own
// answer, or why there is none. The progress line that the pod prints
// before it waits has no result, so it is never the answer.
func answerOf(p pod, log string) (applyResult, string) {
	result, last, found := parseResultLine[applyResult](log, func(r applyResult) string { return r.Result })
	switch {
	case !found && last != "":
		return applyResult{}, "the apply wrote no result; its last line was: " + last
	case !found:
		return applyResult{}, podFailure(p, "apply", applyContainer)
	case result.Result == resultWritten || result.Result == resultRefused || result.Result == resultFailed:
		return result, ""
	}
	return applyResult{}, fmt.Sprintf("the apply answered the result %q, which the operator does not know", result.Result)
}

// retireApplyPods deletes each apply pod that waits for a signal no
// record will send: its approval was withdrawn, its request ended
// without a hold, or its machine is no longer pending. It runs after
// the status write, against the records the write left. A pod with the
// signal may be writing, so it is never deleted before it ends. The
// watch's copy can lag the patch that sent the signal, so the pod is
// read again from the API server before its delete.
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
		held, err := apiclient.Get[pod](o.client, podPath(p.Metadata.Namespace, p.Metadata.Name))
		if errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("reading the apply pod %s: %w", p.Metadata.Name, err))
			continue
		}
		if signalOf(*held).IsZero() {
			problems = append(problems, o.remove(p))
		}
	}
	return errors.Join(problems...)
}
