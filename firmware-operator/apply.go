package main

// After an approved update, a Secure Boot database update or a UEFI
// capsule, passes its rehearsal on a machine's current boot, the
// operator applies it during a reboot turn that machine-operator grants
// (disruption.go). Each step runs in
// its own pass:
//
//  1. The operator starts the apply pod (applypod.go) and records the
//     machine as Waiting. The pod prepares the write before the drain,
//     while the cluster's DNS still works, then waits for the signal to
//     write.
//  2. When the pod is Ready, the operator creates a Disruption with
//     spec.hold and stores its name in the record. A pod that has ended
//     or no longer exists by then gets no turn.
//  3. machine-operator drains the machine and sets Held on the
//     Disruption. The operator then sends the pod the signal
//     (applyfollow.go) and records Writing. When the drain evicted the
//     pod, the operator starts it again with the signal.
//  4. The pod ends. After Written or Uncertain, the operator removes
//     spec.hold, so the turn reboots the machine. After Refused or
//     Failed, which wrote nothing, it deletes the Disruption, so the
//     machine leaves its turn without a reboot.
//  5. The machine's next scan reads the approved version, the machine is
//     no longer pending, and Approved becomes Installed (approval.go).
//
// Uncertain means that the pod received the signal and then ended
// without a result, or reported that the firmware accepted some of its
// writes and not the rest. The pod may have written, and a force-deleted
// pod's container can outlive its API object. If the operator deleted
// the Disruption, the machine would run until an unplanned reboot with
// variables that its kernel does not yet enforce. So the machine reboots
// during its turn, and the next scan shows whether the write took
// effect. A pod that ends before the signal cannot have written, so its
// record is Failed.
//
// Every decision reads status.applies, the apply pods and their
// annotations, and the Disruptions, so an operator that restarts
// continues from the step where the last one stopped. A write cannot be
// undone, so the operator never writes again on its own. A Refused or
// Failed record stays until a person withdraws the approval and
// approves it again.

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
	// Disruption is the name of the Disruption that requests the turn.
	// It is empty until the apply pod is Ready and the operator creates
	// the Disruption.
	Disruption string `json:"disruption,omitempty"`
	// Capsule is true when the apply stages a capsule on the ESP. fwupd
	// sets BootNext to its updater, and the turn's reboot keeps it, so
	// the updater installs the capsule. SHA256 is then the digest of the
	// rehearsal's writes.
	Capsule bool   `json:"capsule,omitempty"`
	Result  string `json:"result"`
	Message string `json:"message,omitempty"`
	Time    string `json:"time"`
}

// The other results of an apply are resultRefused and resultFailed.
const (
	resultWaiting   = "Waiting"
	resultWriting   = "Writing"
	resultWritten   = "Written"
	resultUncertain = "Uncertain"
)

// needsDisruption reports whether the record still needs its Disruption.
func (r applyRecord) needsDisruption() bool {
	return r.Result == resultWaiting || r.Result == resultWriting || r.rebooting()
}

// keepsBootNext reports whether the reboot must keep the BootNext that
// fwupd set for its updater. After an Uncertain result it does not, so
// init points BootNext at the proven slot as on any reboot, and the
// capsule does not run.
func (r applyRecord) keepsBootNext() bool {
	return r.Capsule && r.Result == resultWritten
}

// rebooting reports whether the record waits for the reboot that ends
// the turn, because the pod wrote or may have written.
func (r applyRecord) rebooting() bool {
	return r.Result == resultWritten || r.Result == resultUncertain
}

const appliedCondition = "Applied"

// Applied also uses the results Written, Refused, and Failed as reasons.
const reasonApplying = "Applying"

// The reasons of the Events for the apply steps.
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

// applyStep is a change to one machine's record that a pass computes
// before it writes status. record is the record after the change, and
// from lists the results that the replaced record must have for the
// change to apply. A step with no from creates the machine's first
// record, and a step with drop set deletes the record.
type applyStep struct {
	record applyRecord
	from   []string
	notice notice
	drop   bool
}

// applyResult is the line that `scan apply` writes to its log.
type applyResult struct {
	Result  string `json:"result"`
	Message string `json:"message"`
}

// currentApply returns the machine's record, and whether it belongs to
// the version and cabinet that spec.update approves now.
func currentApply(f *firmware, machine string) (applyRecord, bool) {
	u := f.Spec.Update
	i := slices.IndexFunc(f.Status.Applies, func(r applyRecord) bool { return r.Machine == machine })
	if i < 0 || u == nil || u.Cabinet == nil {
		return applyRecord{}, false
	}
	r := f.Status.Applies[i]
	return r, r.Version == u.Version && r.CabinetSHA256 == u.Cabinet.SHA256
}

// step sets the record's result and message, and picks the Event that
// the change posts.
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
		// The apply wrote nothing because its catalog file is out of
		// date. Deleting the record lets startApply start the apply
		// again when the FirmwareCatalog has a current file.
		return applyStep{record: r, from: from, notice: notice{reason: reasonApplyCatalogMoved, message: about}, drop: true}
	}
	return applyStep{record: r, from: from, notice: n}
}

// advanceApplies returns the next step for each pending machine from its
// record, its apply pod, and its Disruption.
func (o *operator) advanceApplies(f *firmware, pending map[string]bool, w observed, catalogs map[string]*catalog, now time.Time) ([]applyStep, error) {
	var steps []applyStep
	var problems []error
	for _, name := range sortedKeys(pending) {
		var s *applyStep
		var err error
		r, found := currentApply(f, name)
		switch {
		case !found:
			s, err = o.startApply(f, name, w, catalogs)
		case r.Result == resultWaiting:
			s, err = o.nextWaitingStep(f, r, w.applies[f.Metadata.Name], catalogs, now)
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

// startApply starts the apply pod of a machine whose rehearsal passed on
// the boot that it runs now. It starts nothing when the cluster has no
// Disruption kind, or when the FirmwareCatalog has no current file for
// the pod to load.
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
	// An earlier apply pod of the machine may still be terminating, and
	// the same attempt has the same pod name, so creating the pod now
	// would return 409. The deletion of that pod starts another pass.
	if slices.ContainsFunc(w.applies[f.Metadata.Name], func(p pod) bool { return applyOf(p).machine == machine }) {
		return nil, nil
	}
	r := applyRecord{Machine: machine, Boot: rh.Boot, Version: u.Version, CabinetSHA256: u.Cabinet.SHA256}
	if rh.Capsule == nil {
		r.SHA256 = rh.Append.SHA256
	} else {
		// After fwupd's updater runs, the firmware boots the head of
		// BootOrder. So `liken`'s init leaves fwupd's BootNext in place
		// only when the firmware kept BootOrder through its last reset
		// (liken/init/handover.go). On any other machine init points
		// BootNext at the proven slot and the staged capsule never runs,
		// so the apply is refused.
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
	s := step(r, resultWaiting, fmt.Sprintf("the apply pod %s is preparing the write, and the operator requests the machine's reboot turn when the pod is Ready", p.Metadata.Name))
	return &s, nil
}

// nextWaitingStep returns the next step of a Waiting record. A
// machine-operator that does not support spec.hold reboots the machine
// at once and marks the Disruption Complete. So a Disruption that is
// Complete or deleted before the hold fails the record, with nothing
// written.
func (o *operator) nextWaitingStep(f *firmware, r applyRecord, pods []pod, catalogs map[string]*catalog, now time.Time) (*applyStep, error) {
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
		s = step(r, resultFailed, fmt.Sprintf("the Disruption %s no longer exists, so the machine left its reboot turn before machine-operator held it, and nothing was written. To try again, withdraw the approval and approve it again", r.Disruption), resultWaiting)
	case d.complete():
		s = step(r, resultFailed, fmt.Sprintf("the Disruption %s is Complete, so the machine left its reboot turn before machine-operator held it, and nothing was written. A machine-operator that does not support spec.hold reboots the machine immediately. To try again, withdraw the approval and approve it again", r.Disruption), resultWaiting)
	case d.held() && d.Spec.Hold:
		return o.signal(f, r, pods, catalogs, now)
	default:
		return nil, nil
	}
	return &s, nil
}

// checkReboot fails a Written or Uncertain record when the machine has
// booted again and a scan newer than the record still offers the
// approved version. A scan older than the record ran before the reboot
// and proves nothing.
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

// composeApplies returns the records of the pending machines after the
// steps of this pass, and the Event of each step that applied. After a
// 409 on the status write, f can be newer than the copy that the steps
// were decided from, so each step is checked again against f's record.
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
		previous, found := records[s.record.Machine]
		switch {
		case !current(s.record):
			continue
		case s.from == nil && found:
			continue
		case s.from != nil && (!found || !attemptOfApply("", s.record).is(previous) || !slices.Contains(s.from, previous.Result)):
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

// applied returns the Applied condition for the pending machines'
// records, and false when there are no records. The condition reports
// the worst result: Refused first, then Failed, then Uncertain, then
// machines that are still waiting or writing. A pending machine with no
// record counts as waiting.
func applied(records []applyRecord, pending map[string]bool) (condition, bool) {
	if len(pending) == 0 || len(records) == 0 {
		return condition{}, false
	}
	byResult := map[string][]string{}
	for _, name := range sortedKeys(pending) {
		r, found := applyRecordOf(records, name)
		switch {
		case !found || r.Result == resultWaiting || r.Result == resultWriting:
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
		c.Status, c.Reason, c.Message = conditions.False, resultRefused, strings.Join(byResult[resultRefused], ". ")
	case len(byResult[resultFailed]) > 0:
		c.Status, c.Reason, c.Message = conditions.False, resultFailed, strings.Join(byResult[resultFailed], ". ")
	case len(byResult[resultUncertain]) > 0:
		c.Status, c.Reason, c.Message = conditions.Unknown, resultUncertain, strings.Join(byResult[resultUncertain], ". ")
	case len(byResult[reasonApplying]) > 0:
		c.Status, c.Reason, c.Message = conditions.Unknown, reasonApplying,
			"The update is not written yet on "+strings.Join(byResult[reasonApplying], ", ")
	default:
		c.Status, c.Reason, c.Message = conditions.True, resultWritten,
			"The update is written on "+strings.Join(byResult[resultWritten], ", ")+", and takes effect when each machine reboots"
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

// updateSubject returns what the approval installs on the machine, for
// the reason of its Disruption: the catalog component, or else the
// device.
func updateSubject(f *firmware, machine string) string {
	what := f.Spec.Update.Component
	if e, offered := offeredEntry(f.Status.Machines, machine, f.Spec.Update); what == "" && offered {
		what = e.Device
	}
	return what
}

// reconcileDisruptions makes the Firmware's Disruptions match the
// records in its status. For a Written or Uncertain record, it removes
// spec.hold, so the turn reboots the machine. It deletes a Disruption
// that no record needs when the Disruption still has spec.hold or is
// Complete.
// It keeps a Disruption whose hold is removed and whose reboot has not
// happened, so a written update still gets its reboot after the record
// is gone.
//
// A person can delete the Disruption while the apply pod writes. A
// write takes effect only at a reboot, so while a Written or Uncertain
// record's machine still runs the boot of the write, the operator
// creates the Disruption again with no hold.
func (o *operator) reconcileDisruptions(f *firmware, records []applyRecord, requests []disruption, fleet map[string]machine) error {
	needing := map[string]applyRecord{}
	for _, r := range records {
		if r.needsDisruption() {
			needing[r.Disruption] = r
		}
	}
	var problems []error
	existing := map[string]bool{}
	for _, d := range requests {
		existing[d.Metadata.Name] = true
	}
	for _, r := range records {
		m, known := fleet[r.Machine]
		if !r.rebooting() || existing[r.Disruption] || !known || m.Status.Boot.Time != r.Boot {
			continue
		}
		d := buildDisruption(f, r.Machine, updateSubject(f, r.Machine))
		d.Spec.Hold, d.Spec.KeepBootNext = false, r.keepsBootNext()
		if err := o.createDisruption(d); err != nil {
			problems = append(problems, fmt.Errorf("creating the Disruption again for the reboot of %s: %w", r.Machine, err))
		}
	}
	for _, d := range requests {
		r, named := needing[d.Metadata.Name]
		switch {
		case d.Metadata.DeletionTimestamp != "":
		case named && r.rebooting() && d.Spec.Hold:
			problems = append(problems, o.releaseHold(d.Metadata.Name, r.keepsBootNext()))
		case !named && (d.Spec.Hold || d.complete()):
			problems = append(problems, o.deleteDisruption(d.Metadata.Name))
		}
	}
	return errors.Join(problems...)
}
