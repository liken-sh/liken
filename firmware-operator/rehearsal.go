package main

// Before a machine installs an approved release, the operator runs the
// install in a rehearsal pod on the machine, against a copy of the
// machine's firmware variables, and records what the install would
// write. A rehearsal starts when the machine is pending (approval.go),
// its phase is Ready, and its last scan read the FirmwareCatalog's
// current file, so that fwupd in the rehearsal offers the same releases
// that the scan found. Each attempt runs once, except that an attempt
// whose pod the kubelet ended for a reason other than a node shutdown
// runs again in a next pod (retry.go). A new boot, a new approval, or a
// new catalog file is a new attempt.
//
// status.rehearsals has one record for each pending machine. The
// Rehearsed condition is True when the install passed on every pending
// machine, False when it was refused or failed on one, and Unknown
// while a rehearsal runs or waits to start. When the jcat lists another
// file than the one the machine's scan read, the rehearsal returns
// CatalogMoved, which is not a failure. The operator reads the jcat
// again at once, and the machine rehearses again against the new file,
// after a new scan if the file offers it other releases (scans.go). A
// record is removed when its machine is no longer pending or when the
// approval changes.

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
	Machine       string         `json:"machine"`
	Boot          string         `json:"boot"`
	DeviceID      string         `json:"deviceId"`
	Version       string         `json:"version"`
	CabinetSHA256 string         `json:"cabinetSHA256"`
	Catalog       string         `json:"catalog"`
	File          string         `json:"file"`
	Result        string         `json:"result"`
	Message       string         `json:"message,omitempty"`
	Append        *variableEdit  `json:"append,omitempty"`
	Capsule       *capsuleRecord `json:"capsule,omitempty"`
	Free          int64          `json:"free,omitempty"`
	Time          string         `json:"time"`
}

// variableEdit is the append that a passed rehearsal of a Secure Boot
// database update recorded. Bytes and SHA256 describe what the apply
// writes: for each authenticated write, its four attribute bytes
// followed by its signed payload.
type variableEdit struct {
	Variable string `json:"variable"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
}

// capsuleRecord is what a passed rehearsal of a capsule recorded. Writes
// lists each variable and file that fwupd wrote to the rehearsal's
// copies. Bytes is the total size of the files, and ESPFree is the free
// space on the ESP of the machine's next boot.
type capsuleRecord struct {
	Writes  []capsuleWrite `json:"writes"`
	Bytes   int64          `json:"bytes"`
	SHA256  string         `json:"sha256"`
	ESPFree int64          `json:"espFree"`
}

type capsuleWrite struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// The results of a rehearsal, in addition to resultRunning,
// resultCatalogMoved, and resultFailed.
const (
	resultPassed  = "Passed"
	resultRefused = "Refused"
)

// rehearsedCondition is the condition of an approval's rehearsals.
const rehearsedCondition = "Rehearsed"

// The Rehearsed condition's reason while it is Unknown. Its other
// reasons are the results Refused, Failed, and Passed.
const reasonRehearsing = "Rehearsing"

// The reasons of the Events for rehearsal steps that change no
// condition.
const (
	reasonRehearsalStarted      = "RehearsalStarted"
	reasonRehearsalCatalogMoved = "RehearsalCatalogMoved"
	reasonRehearsalPassed       = "RehearsalPassed"
	reasonRehearsalRefused      = "RehearsalRefused"
	reasonRehearsalFailed       = "RehearsalFailed"
	reasonRehearsalPodRecreated = "RehearsalPodRecreated"
)

// rehearsalEnd is the outcome of one finished rehearsal.
type rehearsalEnd struct {
	attempt rehearsalAttempt
	result  rehearsalResult
	// pod is the pod to delete after the result is written, or
	// nil when the pod is already gone.
	pod *pod
}

// rehearsalResult is the line of JSON that `scan rehearse` prints as
// its result.
type rehearsalResult struct {
	Result   string         `json:"result"`
	Message  string         `json:"message"`
	Variable string         `json:"variable"`
	Bytes    int64          `json:"bytes"`
	SHA256   string         `json:"sha256"`
	Free     int64          `json:"free"`
	Writes   []capsuleWrite `json:"writes"`
	ESPFree  int64          `json:"espFree"`
}

// recordOfRehearsal returns the status record for a finished rehearsal.
func recordOfRehearsal(a rehearsalAttempt, r rehearsalResult, now time.Time) rehearsal {
	record := runningRecord(a, now)
	record.Result, record.Message, record.Free = r.Result, r.Message, r.Free
	switch {
	case r.Result == resultPassed && r.Writes != nil:
		record.Capsule = &capsuleRecord{Writes: r.Writes, Bytes: r.Bytes, SHA256: r.SHA256, ESPFree: r.ESPFree}
	case r.Result == resultPassed:
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

// startRehearsals creates a rehearsal pod for each pending, Ready
// machine that needs one, and returns the attempts it started.
func (o *operator) startRehearsals(f *firmware, pending map[string]bool, w observed, catalogs map[string]*catalog, busy map[string]bool) ([]rehearsalAttempt, error) {
	u := f.Spec.Update
	var starts []rehearsalAttempt
	var problems []error
	for _, name := range sortedKeys(pending) {
		m := w.fleet[name]
		if m.Status.Phase != machineReady || busy[name] {
			continue
		}
		a, c, needed := currentRehearsal(f, name, w, catalogs)
		if !needed {
			continue
		}
		if r, found := rehearsalRecord(f.Status.Rehearsals, name); found && a.is(r) {
			continue
		}
		// A machine whose apply pod waits for its turn, writes, or waits
		// to reboot needs no more rehearsals. The apply pod already has
		// what it writes.
		if r, found := currentApply(f, name); found && r.needsDisruption() {
			continue
		}
		p := buildRehearsalPod(f, a, c, u.Cabinet.URL, o.discovery.scanImage, o.namespace, m.hasNextESP())
		err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(p), nil)
		if err != nil && !errors.Is(err, apiclient.ErrConflict) {
			problems = append(problems, err)
			continue
		}
		starts = append(starts, a)
	}
	return starts, errors.Join(problems...)
}

// currentRehearsal returns the attempt that a rehearsal of the machine
// runs now, with the machine's boot, the approval, and the catalog file
// of the machine's last scan, and the FirmwareCatalog of that scan. It
// returns false when the machine needs no rehearsal now: no Machine
// names it, no approval names a cabinet, the catalog offers the machine
// no approved release, or the machine's last scan read an older file
// than the FirmwareCatalog's.
func currentRehearsal(f *firmware, machine string, w observed, catalogs map[string]*catalog) (rehearsalAttempt, *catalog, bool) {
	u := f.Spec.Update
	m, known := w.fleet[machine]
	if !known || u == nil || u.Cabinet == nil {
		return rehearsalAttempt{}, nil, false
	}
	e, offered := offeredEntry(f.Status.Machines, machine, u)
	c := catalogs[e.Catalog]
	if !offered || c == nil || c.Status.File != e.File || c.Status.SHA256 == "" {
		return rehearsalAttempt{}, nil, false
	}
	return rehearsalAttempt{firmware: f.Metadata.Name, machine: machine, boot: m.Status.Boot.Time, device: e.DeviceID,
		version: u.Version, cabinet: u.Cabinet.SHA256, catalog: e.Catalog, file: e.File}, c, true
}

// offeredEntry returns the machine's entry for a device that the
// catalog offers the approved version for, in the pinned cabinet. An
// entry with no DeviceID never matches, because the rehearsal has no
// device to install on.
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

// composeRehearsals returns the records of the pending machines with
// this pass's ended, waiting, and started rehearsals applied, and an
// Event for each change other than a wait. It drops the records of
// machines that are not pending and the records of another approval.
func composeRehearsals(f *firmware, pending map[string]bool, ends []rehearsalEnd, retries []retry, starts []rehearsalAttempt, now time.Time) ([]rehearsal, []notice) {
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
		previous, found := records[end.attempt.machine]
		if !current(end.attempt) || (found && (!end.attempt.is(previous) || previous.Result != resultRunning)) {
			continue
		}
		records[end.attempt.machine] = recordOfRehearsal(end.attempt, end.result, now)
		notices = append(notices, rehearsalNotice(end))
	}
	for _, r := range retries {
		a := rehearsalOf(r.ended)
		previous, found := records[a.machine]
		if !current(a) || (found && (!a.is(previous) || previous.Result != resultRunning)) {
			continue
		}
		record := runningRecord(a, now)
		record.Message = rehearsalRetryMessage(r)
		if found && previous.Message == record.Message {
			continue
		}
		records[a.machine] = record
	}
	for _, a := range starts {
		if previous, found := records[a.machine]; found && a.is(previous) {
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
	case resultCatalogMoved:
		return notice{reason: reasonRehearsalCatalogMoved, message: about}
	case resultRefused:
		return notice{warning: true, reason: reasonRehearsalRefused, message: about}
	}
	return notice{warning: true, reason: reasonRehearsalFailed, message: about}
}

// rehearsed returns the Rehearsed condition for the pending machines'
// records, and false when no machine is pending. Refused takes
// precedence over Failed, and both take precedence over a rehearsal
// that runs or waits. A machine whose catalog moved counts as waiting,
// and the message gives the reason.
func rehearsed(records []rehearsal, pending map[string]bool) (condition, bool) {
	if len(pending) == 0 {
		return condition{}, false
	}
	byResult := map[string][]string{}
	var moved []string
	for _, name := range sortedKeys(pending) {
		r, found := rehearsalRecord(records, name)
		switch {
		case !found || r.Result == resultRunning:
			byResult[resultRunning] = append(byResult[resultRunning], name)
		case r.Result == resultCatalogMoved:
			byResult[resultRunning] = append(byResult[resultRunning], name)
			moved = append(moved, name+": "+r.Message)
		default:
			byResult[r.Result] = append(byResult[r.Result], name+": "+r.Message)
		}
	}
	c := condition{Type: rehearsedCondition}
	switch {
	case len(byResult[resultRefused]) > 0:
		c.Status, c.Reason, c.Message = conditions.False, resultRefused, strings.Join(byResult[resultRefused], ". ")
	case len(byResult[resultFailed]) > 0:
		c.Status, c.Reason, c.Message = conditions.False, resultFailed, strings.Join(byResult[resultFailed], ". ")
	case len(byResult[resultRunning]) > 0:
		c.Status, c.Reason, c.Message = conditions.Unknown, reasonRehearsing,
			"The rehearsal is not finished on "+strings.Join(byResult[resultRunning], ", ")
		if len(moved) > 0 {
			c.Message += ". " + strings.Join(moved, ". ")
		}
	default:
		c.Status, c.Reason, c.Message = conditions.True, resultPassed,
			"The install passed its rehearsal on "+strings.Join(sortedKeys(pending), ", ")
	}
	return c, true
}

// rehearsalEnds reads a Firmware's rehearsal pods and returns the
// attempts that ended, the rehearsal pods that the kubelet ended, the
// machines that have a rehearsal pod, and the earliest deadline among
// the rehearsals still running or waiting for their next pod. A Running
// record whose pod is gone ends as Failed.
func (o *operator) rehearsalEnds(f *firmware, pods []pod, w observed, catalogs map[string]*catalog, now time.Time) ([]rehearsalEnd, []retry, map[string]bool, time.Time, error) {
	var ends []rehearsalEnd
	var retries []retry
	var problems []error
	var due time.Time
	busy := map[string]bool{}
	for _, p := range pods {
		busy[rehearsalOf(p).machine] = true
		if p.Metadata.DeletionTimestamp != "" {
			continue
		}
		// An attempt that the machine no longer needs, such as one of
		// an earlier boot, another approval, or an older catalog file,
		// is not run again. Its end is judged, and startRehearsals
		// starts the attempt that the machine needs after the pod is
		// deleted.
		if a, _, needed := currentRehearsal(f, rehearsalOf(p).machine, w, catalogs); p.retryable() && needed && a == rehearsalOf(p) {
			r := retryOf(p, now)
			if !r.due(now) {
				due = earliest(due, r.at)
			}
			retries = append(retries, r)
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
		if end.result.Result == resultCatalogMoved {
			o.catalogMoved(end.attempt.catalog)
		}
		end.result.Message = stoppedAfter(end.result.Message, p, "rehearsal")
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
				Message: "the rehearsal pod no longer exists, and the operator does not run this rehearsal again until the machine reboots, the approval changes, or the machine's next scan reads another catalog file"}})
		}
	}
	return ends, retries, busy, due, errors.Join(problems...)
}

// rehearsalRetryMessage is the record's message while a rehearsal waits
// for its next pod and while that pod runs.
func rehearsalRetryMessage(r retry) string {
	return fmt.Sprintf("the kubelet ended the rehearsal pod %s: %s, so the operator rehearses again in the rehearsal pod %s from %s",
		r.ended.Metadata.Name, r.ended.kubeletEnd(), nextTry(r.ended).Metadata.Name, stamp(r.at))
}

// replaceEndedRehearsals creates the next pod of each rehearsal whose
// pod the kubelet ended and whose wait is over, and deletes the ended
// pod. records are the rehearsals that the status write left. The
// operator deletes the ended pod of a rehearsal that has no Running
// record there, such as one whose machine is no longer pending, and
// creates no next pod for it.
func (o *operator) replaceEndedRehearsals(f *firmware, records []rehearsal, retries []retry, now time.Time) error {
	var problems []error
	var notices []notice
	for _, r := range retries {
		a := rehearsalOf(r.ended)
		if record, found := rehearsalRecord(records, a.machine); !found || !a.is(record) || record.Result != resultRunning {
			problems = append(problems, o.remove(r.ended))
			continue
		}
		if !r.due(now) {
			continue
		}
		next := nextTry(r.ended)
		created, err := o.replaceEnded(r.ended, next)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if created && r.first() {
			notices = append(notices, notice{reason: reasonRehearsalPodRecreated, message: fmt.Sprintf(
				"%s: the kubelet ended the rehearsal pod %s: %s, so the operator created the rehearsal pod %s for the same rehearsal",
				a.machine, r.ended.Metadata.Name, r.ended.kubeletEnd(), next.Metadata.Name)})
		}
	}
	o.post(f, condition{}, notices, false)
	return errors.Join(problems...)
}

// judgeRehearsal reads the result of a rehearsal pod the way judgeScan
// reads a scan pod's. Its bool is false while the result is not known
// yet, with the pod's deadline when the pod is still running.
func (o *operator) judgeRehearsal(p pod, now time.Time) (rehearsalEnd, bool, time.Time, error) {
	end := rehearsalEnd{attempt: rehearsalOf(p), result: rehearsalResult{Result: resultFailed}, pod: &p}
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
		end.result.Message = "the operator cannot read the log of the rehearsal pod: " + err.Error()
		return end, true, time.Time{}, nil
	}
	result, last, found := parseResultLine[rehearsalResult](log, func(r rehearsalResult) string { return r.Result })
	switch {
	case !found && last != "":
		end.result.Message = "the rehearsal pod wrote no result, and its last log line is: " + last
	case !found:
		end.result.Message = podFailure(p, "rehearsal", rehearsalContainer)
	case result.Result == resultPassed || result.Result == resultRefused || result.Result == resultFailed:
		end.result = result
	case result.Result == resultCatalogMoved:
		end.result = result
		end.result.Message += ", so the operator reads the jcat again and rehearses the machine again against the new file"
	default:
		end.result.Message = fmt.Sprintf("the rehearsal pod reported the result %q, which the operator does not recognize", result.Result)
	}
	return end, true, time.Time{}, nil
}
