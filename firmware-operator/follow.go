package main

// An upload pod writes firmware to a bare device, a chip that has
// enumerated with no firmware loaded. A chip that runs its new firmware
// enumerates again with its loaded IDs, so the bare device leaves the
// machine's slice. The operator records an upload as Loaded only when
// the loader exits 0 and the bare device has left the slice, because a
// loader can exit 0 after it writes firmware that the chip does not run.
//
// The operator writes the result to status before it deletes the pod,
// so an operator that restarts between the two reads the same pod again
// and writes the same result.

import (
	"fmt"
	"strings"
	"time"
)

// uploadDeadline limits an upload, counted from the pod's creation. The
// pod's activeDeadlineSeconds has the same value, but the kubelet counts
// it from when the pod starts, and a pod whose claim is never allocated
// never starts.
const uploadDeadline = 5 * time.Minute

// settleWindow is how long the operator waits, after the loader exits
// 0, for the bare device to leave the slice. A chip that runs its new
// firmware enumerates again within seconds, and a minute leaves room
// for a slow USB hub.
const settleWindow = time.Minute

// verdict is the result of one upload pod. An empty result means the
// upload is in progress, and the operator checks the pod again by due.
type verdict struct {
	result  string
	message string
	due     time.Time
}

func (v verdict) decided() bool { return v.result != "" }

// follow returns an upload pod's result. present is whether the bare
// device is still in the slice.
func follow(p pod, present bool, now time.Time) verdict {
	loader := p.loader()
	switch p.Status.Phase {
	case podSucceeded:
		if !present {
			return verdict{result: resultLoaded, message: "the loader exited 0 and the bare device left the slice"}
		}
		finished := parseTime(p.Metadata.CreationTimestamp)
		if loader != nil && loader.FinishedAt != "" {
			finished = parseTime(loader.FinishedAt)
		}
		due := finished.Add(settleWindow)
		if now.Before(due) {
			return verdict{due: due}
		}
		return verdict{result: resultFailed, message: fmt.Sprintf(
			"the loader exited 0 and the device stayed in the slice for %s after it", settleWindow)}
	case podFailed:
		return verdict{result: resultFailed, message: failure(p, loader)}
	}
	due := parseTime(p.Metadata.CreationTimestamp).Add(uploadDeadline)
	if now.Before(due) {
		return verdict{due: due}
	}
	return verdict{result: resultFailed, message: fmt.Sprintf("the upload pod did not finish within %s of its creation", uploadDeadline)}
}

// failure describes why a pod failed: the loader's exit code and the
// last line of its log, or the pod's own reason when the loader never
// ran to the end, such as DeadlineExceeded from activeDeadlineSeconds.
func failure(p pod, loader *terminated) string {
	if loader == nil {
		message := "the upload pod failed"
		for _, part := range []string{p.Status.Reason, p.Status.Message} {
			if part != "" {
				message += ": " + part
			}
		}
		return message
	}
	message := fmt.Sprintf("the loader exited with code %d", loader.ExitCode)
	if loader.Reason != "" && loader.Reason != "Error" {
		message += " (" + loader.Reason + ")"
	}
	if line := lastLine(loader.Message); line != "" {
		message += ": " + line
	}
	return message
}

// maxLineSize is the most of the loader's last log line that a status
// entry quotes. The termination message that the kubelet copies from
// the log is already at most 2048 bytes or 80 lines.
const maxLineSize = 256

func lastLine(log string) string {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > maxLineSize {
		line = line[:maxLineSize] + "..."
	}
	return line
}

// parseTime reads a time written by the API server. A value that does
// not parse gives the zero time, so every deadline counted from it is
// already due.
func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}
