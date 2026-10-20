package main

// The operator follows each upload pod to its result. An upload
// succeeds when the pod exits 0 and the bare device leaves the
// machine's slice. The exit code alone is not enough, because a loader
// can exit 0 after it writes firmware that the chip does not run. The
// operator writes the result to status first and then deletes the pod,
// so an operator that stops between the two reads the same pod again
// and writes the same result.

import (
	"fmt"
	"strings"
	"time"
)

// uploadDeadline bounds an upload, from its pod's creation. The pod's
// own activeDeadlineSeconds has the same value, but counts only from
// the start of a pod that runs, and a pod whose claim never allocates
// never starts.
const uploadDeadline = 5 * time.Minute

// settleWindow is how long the operator waits for the bare device to
// leave the slice after the loader exits 0. A chip that took its
// firmware resets and enumerates again within seconds, so a minute
// leaves room for a slow hub without holding a failed upload open.
const settleWindow = time.Minute

// verdict is the result of one upload pod. A zero result means the
// upload is still in progress, and due is when the operator decides it
// at the latest.
type verdict struct {
	result  string
	message string
	due     time.Time
}

func (v verdict) decided() bool { return v.result != "" }

// follow decides an upload pod's result from the pod and from whether
// its appearance is still in the slice.
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

// failure says why a pod failed: the loader's exit code and the last
// line it wrote, or the pod's own reason when the loader never ended,
// such as the DeadlineExceeded of activeDeadlineSeconds.
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

// maxLineSize bounds the line of the loader's log that a status entry
// quotes. The kubelet already limits the message to the last 2048
// bytes of the log.
const maxLineSize = 256

// lastLine answers the last line of a log that holds text.
func lastLine(log string) string {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > maxLineSize {
		line = line[:maxLineSize] + "..."
	}
	return line
}

// parseTime reads a time the API server wrote. A time that does not
// parse is the zero time, which makes every deadline due.
func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}
