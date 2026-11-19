package main

// When a scan pod ends, the operator reads the Result from its log,
// writes it into the FirmwareCatalog's record of the machine and into
// the discovered Firmware objects, and only then deletes the pod. So an
// operator that restarts between those writes reads the same pod again
// and writes the same result.

import (
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// scanEnd is the result of one finished scan attempt.
type scanEnd struct {
	attempt scanAttempt
	result  string
	message string
	devices []scanDevice
	// scan is the pod to delete once the result is written, or nil for
	// a pod that is already gone.
	scan *pod
}

// judgeScan returns the result of a scan pod that has ended or passed
// scanDeadline. For a pod still within its deadline it returns false
// and the deadline. It also returns false when the pod's log is gone.
// For a succeeded pod whose log it cannot read before scanDeadline, it
// returns an error.
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
	log, err := o.readPodLog(p, scanContainer)
	if errors.Is(err, apiclient.ErrNotFound) {
		// The pod is gone, so there is no result to read here. If the
		// operator deleted it after it wrote the result, the informer's
		// cache can still list it for a short time. If someone else
		// deleted it, its record stays Running until the cache drops
		// the pod, and then endsOf marks the record Failed.
		return scanEnd{}, false, time.Time{}, nil
	}
	if err != nil && p.Status.Phase == podSucceeded {
		// The result of a succeeded scan is in its log. Until
		// scanDeadline, return an error so that the next pass reads
		// the log again. After it, fail the scan, so that a log the
		// operator can never read, for example without permission,
		// does not fail every pass.
		if now.Before(due) {
			return scanEnd{}, false, time.Time{}, fmt.Errorf("reading the log of the scan pod %s: %w", p.Metadata.Name, err)
		}
		end.message = "the operator cannot read the log of the scan pod: " + err.Error()
		return end, true, time.Time{}, nil
	}
	result, last, found := parseScanLog(log)
	switch {
	case !found && last != "":
		end.message = "the scan pod wrote no result, and its last log line is: " + last
	case !found:
		end.message = podFailure(p, "scan", scanContainer)
	case result.Result == resultScanned || result.Result == resultCatalogMoved || result.Result == resultFailed:
		end.result, end.message, end.devices = result.Result, result.Message, result.Devices
	default:
		end.message = fmt.Sprintf("the scan pod reported the result %q, which the operator does not recognize", result.Result)
	}
	return end, true, time.Time{}, nil
}

// podFailure returns why a scan or rehearsal pod that wrote no log
// failed, for example DeadlineExceeded from activeDeadlineSeconds
// before its container ran. kind is "scan" or "rehearsal", and the
// message includes the exit code of container when it ended.
func podFailure(p pod, kind, container string) string {
	message := "the " + kind + " pod wrote no log"
	for _, part := range []string{p.Status.Reason, p.Status.Message} {
		if part != "" {
			message += ": " + part
		}
	}
	if ended := p.ended(container); ended != nil {
		message += fmt.Sprintf(": the %s exited with code %d", kind, ended.ExitCode)
		if line := lastLine(ended.Message); line != "" {
			message += ": " + line
		}
	}
	return message
}

// currentScan returns the attempt that a scan of the machine runs now,
// with the machine's boot and devices, the FirmwareCatalog's file, and
// the operator's scan image. A machine that no Machine names has none.
func (o *operator) currentScan(c *catalog, machine string, w observed) scanAttempt {
	m, exists := w.fleet[machine]
	if !exists {
		return scanAttempt{}
	}
	return scanAttempt{catalog: c.Metadata.Name, machine: machine, boot: m.Status.Boot.Time, devices: w.deviceSets[machine], file: c.Status.File, image: o.discovery.scanImage}
}

// scanRetryMessage is the record's message while a scan waits for its
// next pod and while that pod runs.
func scanRetryMessage(r retry, namespace string) string {
	return fmt.Sprintf("the kubelet ended the scan pod %s/%s: %s, so the operator scans the machine again in the scan pod %s/%s from %s",
		namespace, r.ended.Metadata.Name, r.ended.kubeletEnd(), namespace, nextTry(r.ended).Metadata.Name, stamp(r.at))
}

// endsOf returns a FirmwareCatalog's scan attempts that ended, the scan
// pods that the kubelet ended, the machines that have a scan pod, and
// the earliest deadline among the scans still running or waiting for
// their next pod. A Running record whose pod is gone ends as Failed,
// and the operator does not run that attempt again.
func (o *operator) endsOf(c *catalog, scans []pod, w observed, now time.Time) ([]scanEnd, []retry, map[string]bool, time.Time, error) {
	var ends []scanEnd
	var retries []retry
	var problems []error
	var due time.Time
	busy := map[string]bool{}
	for _, p := range scans {
		busy[scanOf(p).machine] = true
		if p.Metadata.DeletionTimestamp != "" {
			continue
		}
		// An attempt that the machine no longer needs, such as one of
		// an earlier boot or an older catalog file, is not run again. Its
		// end is judged, and schedule starts the attempt that the
		// machine needs after the pod is deleted.
		if p.retryable() && scanOf(p) == o.currentScan(c, scanOf(p).machine, w) {
			r := retryOf(p, now)
			if !r.due(now) {
				due = earliest(due, r.at)
			}
			retries = append(retries, r)
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
		end.message = stoppedAfter(end.message, p, "scan")
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
				message: "the scan pod no longer exists, and the operator does not run this scan again until the machine's boot, its devices, the catalog file, or the scan image changes"})
		}
	}
	return ends, retries, busy, due, errors.Join(problems...)
}

// recordOf returns the machine record for an attempt's end. A Scanned
// result sets the GUIDs that the scan reported and the hash of their
// releases. Any other result keeps the previous record's GUIDs and
// hash, because they are still the machine's last successful report.
func recordOf(end scanEnd, previous machineRecord, releases string, now time.Time) machineRecord {
	r := machineRecord{
		Machine: end.attempt.machine, Boot: end.attempt.boot, Devices: end.attempt.devices, File: end.attempt.file, Image: end.attempt.image,
		Result: end.result, Message: end.message, Time: stamp(now),
		GUIDs: previous.GUIDs, Releases: previous.Releases,
	}
	if end.result == resultScanned {
		r.GUIDs, r.Releases = guidsOf(end.devices), releases
	}
	return r
}

// guidsOf returns the distinct GUIDs of a scan's devices, or nil when
// there are none.
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
