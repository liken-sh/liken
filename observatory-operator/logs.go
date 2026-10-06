package main

// The log of each reservation. `kubectl logs` tells the same story as
// the status: one line when the phase changes, one when a step starts,
// and one when a step ends, with what it did and how long it took.
// Each line leads with the reservation's name, so the lines of
// several reservations can be read apart.

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// logf writes one line to the operator's log, with the prefix that
// begins every line.
func (o *operator) logf(format string, args ...any) {
	fmt.Fprintf(o.logs, "observatory-operator: "+format+"\n", args...)
}

// timedOut begins the summary of a step that passed its deadline.
const timedOut = "Timed out"

// stepLine says how a step ended: "Prepare done in 19 s: cooled
// east-main to -10 °C", or "Connect failed: ..." for a failed step.
func stepLine(s *observatory.Step) string {
	if s.State == observatory.StepFailed {
		return failedMessage(s)
	}
	line := string(s.Name) + " " + strings.ToLower(string(s.State))
	if s.StartTime != nil && s.StopTime != nil {
		line += " in " + duration(s.StopTime.Sub(*s.StartTime))
	}
	if s.Summary != "" {
		line += ": " + lowerFirst(s.Summary)
	}
	return line
}

// failedMessage names a failed step and says why it failed. The Ready
// condition, the Warning Event, and the log carry it.
func failedMessage(s *observatory.Step) string {
	return string(s.Name) + " " + lowerFirst(s.Summary)
}

// phaseLine names a reservation's new phase, and the endpoint when it
// is Ready. The step line before it gives the reason for a failure.
func phaseLine(s *observatory.ReservationStatus) string {
	if s.Phase == observatory.ReservationReady {
		for _, c := range s.Conditions {
			if c.Type == observatory.ConditionReady && c.Message != "" {
				return c.Message
			}
		}
	}
	return string(s.Phase)
}

// sentence makes a message start with a capital letter. The operator
// writes its messages from fragments that start with a verb or a
// state, so the first letter is never part of a resource's name.
func sentence(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// lowerFirst makes a sentence fit after a colon. A word in capitals,
// such as INDI, keeps its case.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	next, _ := utf8.DecodeRuneInString(s[size:])
	if unicode.IsUpper(next) {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}
