package main

// Before any machine installs an approved release, the operator
// rehearses the install on each machine the approval waits for, and
// records exactly what it would write. A rehearsal starts when a
// machine is pending (approval.go), Ready, and its last scan read the
// FirmwareCatalog's current file, so fwupd in the rehearsal offers the
// releases the scan saw. Each attempt runs once.
//
// status.rehearsals holds one record for each pending machine, and the
// Rehearsed condition sums them: True when the install passed on every
// pending machine, False when it was refused or failed on one, and
// Unknown while one waits. A record leaves when its machine is no
// longer pending, or when the approval changes, because a rehearsal
// answers one release on one boot.

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

// rehearsal is one machine's record in status.rehearsals.
type rehearsal struct {
	Machine       string        `json:"machine"`
	Boot          string        `json:"boot"`
	DeviceID      string        `json:"deviceId"`
	Version       string        `json:"version"`
	CabinetSHA256 string        `json:"cabinetSHA256"`
	Catalog       string        `json:"catalog"`
	File          string        `json:"file"`
	Result        string        `json:"result"`
	Message       string        `json:"message,omitempty"`
	Append        *variableEdit `json:"append,omitempty"`
	Free          int64         `json:"free,omitempty"`
	Time          string        `json:"time"`
}

// variableEdit is the one authenticated append that a passed
// rehearsal recorded: the variable, and the bytes the write writes,
// which are the four attribute bytes and the signed payload.
type variableEdit struct {
	Variable string `json:"variable"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
}

// The results of a rehearsal, beside resultRunning and resultFailed.
const (
	resultPassed  = "Passed"
	resultRefused = "Refused"
)

// rehearsedCondition is the condition of an approval's rehearsals.
const rehearsedCondition = "Rehearsed"

// The reasons of Rehearsed. Refused and Failed are the results'.
const reasonRehearsing = "Rehearsing"

// The reasons of the Events that change no condition.
const (
	reasonRehearsalStarted = "RehearsalStarted"
	reasonRehearsalPassed  = "RehearsalPassed"
	reasonRehearsalRefused = "RehearsalRefused"
	reasonRehearsalFailed  = "RehearsalFailed"
)

// rehearsalEnd is the result of one finished rehearsal.
type rehearsalEnd struct {
	attempt rehearsalAttempt
	result  rehearsalResult
	// rehearsal is the pod to delete once the result is written, or nil
	// for a pod that is already gone.
	rehearsal *pod
}

// rehearsalResult is the line `scan rehearse` writes.
type rehearsalResult struct {
	Result   string `json:"result"`
	Message  string `json:"message"`
	Variable string `json:"variable"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Free     int64  `json:"free"`
}

// recordOfRehearsal answers the record that a rehearsal's end writes.
func recordOfRehearsal(a rehearsalAttempt, r rehearsalResult, now time.Time) rehearsal {
	record := runningRecord(a, now)
	record.Result, record.Message, record.Free = r.Result, r.Message, r.Free
	if r.Result == resultPassed {
		record.Append = &variableEdit{Variable: r.Variable, Bytes: r.Bytes, SHA256: r.SHA256}
	}
	return record
}

func runningRecord(a rehearsalAttempt, now time.Time) rehearsal {
	return rehearsal{
		Machine: a.machine, Boot: a.boot, DeviceID: a.device, Version: a.version, CabinetSHA256: a.cabinet,
		Catalog: a.catalog, File: a.file, Result: resultRunning, Time: stamp(now),
	}
}

func attemptOfRecord(firmware string, r rehearsal) rehearsalAttempt {
	return rehearsalAttempt{firmware: firmware, machine: r.Machine, boot: r.Boot, device: r.DeviceID,
		version: r.Version, cabinet: r.CabinetSHA256, catalog: r.Catalog, file: r.File}
}

// startRehearsals creates the pod of each attempt that a pending,
// Ready machine needs, and answers the attempts it started.
func (o *operator) startRehearsals(f *firmware, pending map[string]bool, w observed, catalogs map[string]*catalog, busy map[string]bool) ([]rehearsalAttempt, error) {
	u := f.Spec.Update
	var starts []rehearsalAttempt
	var problems []error
	for _, name := range sortedKeys(pending) {
		m, known := w.fleet[name]
		if !known || m.Status.Phase != machineReady || busy[name] {
			continue
		}
		e, offered := offeredEntry(f.Status.Machines, name, u)
		c := catalogs[e.Catalog]
		if !offered || c == nil || c.Status.File != e.File || c.Status.SHA256 == "" {
			continue
		}
		a := rehearsalAttempt{firmware: f.Metadata.Name, machine: name, boot: m.Status.Boot.Time, device: e.DeviceID,
			version: u.Version, cabinet: u.Cabinet.SHA256, catalog: e.Catalog, file: e.File}
		if r, held := rehearsalRecord(f.Status.Rehearsals, name); held && a.is(r) {
			continue
		}
		p := buildRehearsalPod(f, a, c, u.Cabinet.URL, o.discovery.scanImage, o.namespace)
		err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(p), nil)
		if err != nil && !errors.Is(err, apiclient.ErrConflict) {
			problems = append(problems, err)
			continue
		}
		starts = append(starts, a)
	}
	return starts, errors.Join(problems...)
}

// offeredEntry answers a machine's entry whose device a catalog offers
// the approved version in the pinned cabinet. An entry from before
// fwupd's device ID was recorded names no device to rehearse.
func offeredEntry(entries []machineEntry, machine string, u *update) (machineEntry, bool) {
	for _, e := range entries {
		if e.Machine != machine || e.DeviceID == "" {
			continue
		}
		for _, r := range e.Releases {
			if r.Version == u.Version && r.Cabinet != nil && r.Cabinet.SHA256 == u.Cabinet.SHA256 {
				return e, true
			}
		}
	}
	return machineEntry{}, false
}

func rehearsalRecord(records []rehearsal, machine string) (rehearsal, bool) {
	i := slices.IndexFunc(records, func(r rehearsal) bool { return r.Machine == machine })
	if i < 0 {
		return rehearsal{}, false
	}
	return records[i], true
}

// composeRehearsals answers the records of the pending machines after
// the ends and the starts of this pass, and the Events of each. It
// keeps no record of another machine or of another approval.
func composeRehearsals(f *firmware, pending map[string]bool, ends []rehearsalEnd, starts []rehearsalAttempt, now time.Time) ([]rehearsal, []notice) {
	u := f.Spec.Update
	if u == nil || u.Version == "" || u.Cabinet == nil {
		return nil, nil
	}
	current := func(a rehearsalAttempt) bool {
		return pending[a.machine] && a.version == u.Version && a.cabinet == u.Cabinet.SHA256
	}
	records := map[string]rehearsal{}
	for _, r := range f.Status.Rehearsals {
		if current(attemptOfRecord(f.Metadata.Name, r)) {
			records[r.Machine] = r
		}
	}
	var notices []notice
	for _, end := range ends {
		previous, held := records[end.attempt.machine]
		if !current(end.attempt) || (held && (!end.attempt.is(previous) || previous.Result != resultRunning)) {
			continue
		}
		records[end.attempt.machine] = recordOfRehearsal(end.attempt, end.result, now)
		notices = append(notices, rehearsalNotice(end))
	}
	for _, a := range starts {
		if previous, held := records[a.machine]; held && a.is(previous) {
			continue
		}
		records[a.machine] = runningRecord(a, now)
		notices = append(notices, notice{reason: reasonRehearsalStarted, message: fmt.Sprintf(
			"%s: the rehearsal pod %s rehearses %s against a copy of the machine's firmware variables", a.machine, rehearsalPodName(a), a.version)})
	}
	var out []rehearsal
	for _, name := range sortedKeys(records) {
		out = append(out, records[name])
	}
	return out, notices
}

func rehearsalNotice(end rehearsalEnd) notice {
	about := end.attempt.machine + ": " + end.result.Message
	switch end.result.Result {
	case resultPassed:
		return notice{reason: reasonRehearsalPassed, message: about}
	case resultRefused:
		return notice{warning: true, reason: reasonRehearsalRefused, message: about}
	}
	return notice{warning: true, reason: reasonRehearsalFailed, message: about}
}

// rehearsed answers the Rehearsed condition over the pending machines'
// records, and false when the approval waits for no machine. A refusal
// outweighs a failure, and both outweigh a machine that waits.
func rehearsed(records []rehearsal, pending map[string]bool) (condition, bool) {
	if len(pending) == 0 {
		return condition{}, false
	}
	byResult := map[string][]string{}
	for _, name := range sortedKeys(pending) {
		r, held := rehearsalRecord(records, name)
		switch {
		case !held || r.Result == resultRunning:
			byResult[resultRunning] = append(byResult[resultRunning], name)
		default:
			byResult[r.Result] = append(byResult[r.Result], name+": "+r.Message)
		}
	}
	c := condition{Type: rehearsedCondition}
	switch {
	case len(byResult[resultRefused]) > 0:
		c.Status, c.Reason, c.Message = conditions.False, resultRefused, strings.Join(byResult[resultRefused], "; ")
	case len(byResult[resultFailed]) > 0:
		c.Status, c.Reason, c.Message = conditions.False, resultFailed, strings.Join(byResult[resultFailed], "; ")
	case len(byResult[resultRunning]) > 0:
		c.Status, c.Reason, c.Message = conditions.Unknown, reasonRehearsing,
			"the approval waits for the rehearsal on "+strings.Join(byResult[resultRunning], ", ")
	default:
		c.Status, c.Reason, c.Message = conditions.True, resultPassed,
			"the install passed its rehearsal on "+strings.Join(sortedKeys(pending), ", ")
	}
	return c, true
}

// rehearsalEnds follows a Firmware's rehearsal pods, and answers the
// attempts that ended, the machines with a rehearsal pod, and the
// earliest deadline of a rehearsal in progress. A Running record whose
// pod is gone ends Failed.
func (o *operator) rehearsalEnds(f *firmware, pods []pod, now time.Time) ([]rehearsalEnd, map[string]bool, time.Time, error) {
	var ends []rehearsalEnd
	var problems []error
	var due time.Time
	busy := map[string]bool{}
	for _, p := range pods {
		busy[rehearsalOf(p).machine] = true
		if p.Metadata.DeletionTimestamp != "" {
			continue
		}
		end, decided, deadline, err := o.judgeRehearsal(p, now)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !decided {
			due = earliest(due, deadline)
			continue
		}
		ends = append(ends, end)
	}
	for _, r := range f.Status.Rehearsals {
		if r.Result != resultRunning || busy[r.Machine] {
			continue
		}
		a := attemptOfRecord(f.Metadata.Name, r)
		gone, err := o.podGone(rehearsalPodName(a))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if gone {
			ends = append(ends, rehearsalEnd{attempt: a, result: rehearsalResult{Result: resultFailed,
				Message: "the rehearsal pod is gone; the operator does not run this rehearsal again until the machine reboots, the approval changes, or the machine's next scan reads another catalog file"}})
		}
	}
	return ends, busy, due, errors.Join(problems...)
}

// judgeRehearsal decides the result of a rehearsal pod, the way
// judgeScan decides a scan's.
func (o *operator) judgeRehearsal(p pod, now time.Time) (rehearsalEnd, bool, time.Time, error) {
	end := rehearsalEnd{attempt: rehearsalOf(p), result: rehearsalResult{Result: resultFailed}, rehearsal: &p}
	due := parseTime(p.Metadata.CreationTimestamp).Add(scanDeadline)
	switch p.Status.Phase {
	case podSucceeded, podFailed:
	default:
		if now.Before(due) {
			return rehearsalEnd{}, false, due, nil
		}
		end.result.Message = fmt.Sprintf("the rehearsal pod did not finish within %s of its creation", scanDeadline)
		return end, true, time.Time{}, nil
	}
	log, err := o.readPodLog(p, rehearsalContainer)
	if errors.Is(err, apiclient.ErrNotFound) {
		return rehearsalEnd{}, false, time.Time{}, nil
	}
	if err != nil && p.Status.Phase == podSucceeded {
		if now.Before(due) {
			return rehearsalEnd{}, false, time.Time{}, fmt.Errorf("reading the log of the rehearsal pod %s: %w", p.Metadata.Name, err)
		}
		end.result.Message = "the operator could not read the rehearsal's log: " + err.Error()
		return end, true, time.Time{}, nil
	}
	result, last, found := parseResultLine[rehearsalResult](log, func(r rehearsalResult) string { return r.Result })
	switch {
	case !found && last != "":
		end.result.Message = "the rehearsal wrote no result; its last line was: " + last
	case !found:
		end.result.Message = podFailure(p, "rehearsal", rehearsalContainer)
	case result.Result == resultPassed || result.Result == resultRefused || result.Result == resultFailed:
		end.result = result
	default:
		end.result.Message = fmt.Sprintf("the rehearsal answered the result %q, which the operator does not know", result.Result)
	}
	return end, true, time.Time{}, nil
}
