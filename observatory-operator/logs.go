package main

// The operator's log lines for reservations: one when the phase
// changes, one when a step starts or resumes, and one when a step ends,
// with its summary and how long it took.

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// logf writes one line to the operator's log. The prefix has the
// namespace, because resources in different namespaces can have the
// same names.
func (o *operator) logf(format string, args ...any) {
	fmt.Fprintf(o.logs, "observatory-operator: "+o.namespace+": "+format+"\n", args...)
}

// timedOut starts the summary of a step that passed its deadline.
const timedOut = "Timed out"

// stepLine returns the log line for a step that ended, such as
// "Activation done in 19 s: cooled Camera east-main to -10 °C".
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

// failedMessage returns the step's name and the reason it failed. The
// log line, the Warning Event, and the Ready condition all use it.
func failedMessage(s *observatory.Step) string {
	return string(s.Name) + " " + lowerFirst(s.Summary)
}

// phaseLine returns the log line for a reservation's new phase. For a
// Ready reservation it is the Ready condition's message, which has the
// endpoint. A Failed phase logs only "Failed", because the step line
// before it already has the reason.
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

// sentence capitalizes the first letter of a message. It is safe
// because the operator's message fragments begin with a verb or a
// state, never with a resource's name.
func sentence(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// lowerFirst lowercases the first letter of a sentence so that it can
// follow a colon. It leaves the sentence alone when its first word has
// another capital, as in INDI or a kind such as WeatherStation.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	word, _, _ := strings.Cut(s[size:], " ")
	if strings.ContainsFunc(word, unicode.IsUpper) {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}
