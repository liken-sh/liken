package main

// The operator follows each scan pod to its result. When the pod ends,
// the operator reads its log, writes the result into the FirmwareCatalog's
// record of the machine, then writes the discovered Firmware objects,
// and then deletes the pod. An operator that stops between two of
// those steps reads the same pod again and writes the same result, so
// the pod is the record of a result until both writes landed.

import (
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// scanEnd is the result of one finished attempt.
type scanEnd struct {
	attempt scanAttempt
	result  string
	message string
	devices []scanDevice
	// scan is the pod to delete once the result is written, or nil for
	// a pod that is already gone.
	scan *pod
}

// judgeScan decides the result of a scan pod. A pod that has not ended
// answers false, with the time its deadline is due.
func (o *operator) judgeScan(p pod, now time.Time) (scanEnd, bool, time.Time, error) {
	end := scanEnd{attempt: scanOf(p), result: resultFailed, scan: &p}
	due := parseTime(p.Metadata.CreationTimestamp).Add(scanDeadline)
	switch p.Status.Phase {
	case podSucceeded, podFailed:
	default:
		if now.Before(due) {
			return scanEnd{}, false, due, nil
		}
		end.message = fmt.Sprintf("the scan pod did not finish within %s of its creation", scanDeadline)
		return end, true, time.Time{}, nil
	}
	log, err := o.readScanLog(p)
	if errors.Is(err, apiclient.ErrNotFound) {
		// The store can still hold a pod that this operator deleted after
		// it wrote the result. A pod that someone else deleted leaves a
		// Running record, which endsOf fails once the store drops the
		// pod.
		return scanEnd{}, false, time.Time{}, nil
	}
	if err != nil && p.Status.Phase == podSucceeded {
		// A scan that succeeded wrote its result, so the operator reads
		// the log again on the next pass rather than lose the result,
		// until the scan's deadline. A log that stays unreadable past it,
		// such as one the operator has no permission to read, fails the
		// scan, so the pass does not fail on it for good.
		if now.Before(due) {
			return scanEnd{}, false, time.Time{}, fmt.Errorf("reading the log of the scan pod %s: %w", p.Metadata.Name, err)
		}
		end.message = "the operator could not read the scan's log: " + err.Error()
		return end, true, time.Time{}, nil
	}
	result, last, found := parseScanLog(log)
	switch {
	case !found && last != "":
		end.message = "the scan wrote no result; its last line was: " + last
	case !found:
		end.message = scanPodFailure(p)
	case result.Result == resultScanned || result.Result == resultCatalogMoved || result.Result == resultFailed:
		end.result, end.message, end.devices = result.Result, result.Message, result.Devices
	default:
		end.message = fmt.Sprintf("the scan answered the result %q, which the operator does not know", result.Result)
	}
	return end, true, time.Time{}, nil
}

// scanPodFailure says why a scan pod with no log failed, such as the
// DeadlineExceeded of activeDeadlineSeconds before the container ran.
func scanPodFailure(p pod) string {
	message := "the scan pod wrote no log"
	for _, part := range []string{p.Status.Reason, p.Status.Message} {
		if part != "" {
			message += ": " + part
		}
	}
	if ended := p.ended(scanContainer); ended != nil {
		message += fmt.Sprintf(": the scan exited with code %d", ended.ExitCode)
		if line := lastLine(ended.Message); line != "" {
			message += ": " + line
		}
	}
	return message
}

// endsOf follows a FirmwareCatalog's scan pods, and answers the attempts that
// ended, the machines with a scan pod, and the earliest deadline of a
// scan in progress. A Running record whose pod is gone ends Failed, and
// the operator does not run that attempt again.
func (o *operator) endsOf(c *catalog, scans []pod, now time.Time) ([]scanEnd, map[string]bool, time.Time, error) {
	var ends []scanEnd
	var problems []error
	var due time.Time
	busy := map[string]bool{}
	for _, p := range scans {
		busy[scanOf(p).machine] = true
		if p.Metadata.DeletionTimestamp != "" {
			continue
		}
		end, decided, deadline, err := o.judgeScan(p, now)
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
	for _, r := range c.Status.Machines {
		if r.Result != resultRunning || busy[r.Machine] {
			continue
		}
		a := attemptOf(c.Metadata.Name, r)
		gone, err := o.podGone(scanPodName(a))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if gone {
			ends = append(ends, scanEnd{attempt: a, result: resultFailed,
				message: "the scan pod is gone; the operator does not run this scan again until the machine, its devices, or the catalog file change"})
		}
	}
	return ends, busy, due, errors.Join(problems...)
}

// recordOf answers the record that an attempt's end writes, after the
// machine's previous record. A scan that answered keeps the GUIDs of
// every device it reported, and the hash of their releases in the
// catalog file it ran with. Any other result keeps the GUIDs and the
// hash of the previous record, which are still the machine's last
// report.
func recordOf(end scanEnd, previous machineRecord, releases string, now time.Time) machineRecord {
	r := machineRecord{
		Machine: end.attempt.machine, Boot: end.attempt.boot, Devices: end.attempt.devices, File: end.attempt.file,
		Result: end.result, Message: end.message, Time: stamp(now),
		GUIDs: previous.GUIDs, Releases: previous.Releases,
	}
	if end.result == resultScanned {
		r.GUIDs, r.Releases = guidsOf(end.devices), releases
	}
	return r
}

// guidsOf answers every GUID of the devices a scan reported.
func guidsOf(devices []scanDevice) []string {
	var guids []string
	for _, d := range devices {
		guids = append(guids, d.GUIDs...)
	}
	if len(guids) == 0 {
		return nil
	}
	return sortedSet(guids)
}

// endNotice answers the Event of an attempt's end.
func endNotice(end scanEnd) notice {
	about := end.attempt.machine + ": " + end.message
	switch end.result {
	case resultScanned:
		return notice{reason: reasonScanned, message: about}
	case resultCatalogMoved:
		return notice{reason: reasonCatalogMoved, message: about}
	}
	return notice{warning: true, reason: reasonScanFailed, message: about}
}
