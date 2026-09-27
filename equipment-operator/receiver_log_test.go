package main

// How a receiver's log holds a command's line until the receiver
// reports the result, and the words the lines use.

import (
	"errors"
	"regexp"
	"sync/atomic"
	"testing"
	"time"
)

// timeless puts one mark in place of each measured duration, because a
// test cannot fix how long the receiver takes to answer.
func timeless(lines []string) []string {
	measured := regexp.MustCompile(`after \d+(\.\d)? m?s\b`)
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned = append(cleaned, measured.ReplaceAllString(line, "after <time>"))
	}
	return cleaned
}

// waitForLines waits until the log holds count lines that contain text,
// and answers them without their durations.
func waitForLines(t *testing.T, log *logBuffer, text string, count int) []string {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for len(linesWith(log, text)) < count {
		if time.Now().After(deadline) {
			t.Fatalf("the log never held %d lines with %q; it holds %q", count, text, log.lines())
		}
		time.Sleep(time.Millisecond)
	}
	return timeless(linesWith(log, text))
}

// switchCheck is a check whose result a test sets.
func switchCheck(confirmed *atomic.Bool) func() (string, bool) {
	return func() (string, bool) {
		if confirmed.Load() {
			return "power on", true
		}
		return "power standby", false
	}
}

func TestACommandsLineWaitsForTheReportAndPrintsOnce(t *testing.T) {
	log := &logBuffer{}
	lines := newReceiverLog(log, "den")
	var confirmed atomic.Bool

	lines.confirm("generation 2 asks power on; sent power on", time.Now(), switchCheck(&confirmed))
	lines.observe()
	mustDeepEqual(t, linesWith(log, "Receiver"), []string(nil))

	confirmed.Store(true)
	lines.observe()
	lines.observe()

	mustDeepEqual(t, timeless(log.lines()), []string{
		"Receiver den: generation 2 asks power on; sent power on; the receiver reported power on after <time>",
	})
}

func TestACommandTheReceiverAlreadyReportsPrintsAtOnce(t *testing.T) {
	log := &logBuffer{}
	var confirmed atomic.Bool
	confirmed.Store(true)

	newReceiverLog(log, "den").confirm("generation 2 asks power on; sent power on", time.Now(), switchCheck(&confirmed))

	mustDeepEqual(t, timeless(log.lines()), []string{
		"Receiver den: generation 2 asks power on; sent power on; the receiver reported power on after <time>",
	})
}

func TestACommandTheReceiverNeverReportsPrintsWhenTheWaitEnds(t *testing.T) {
	shorten(t, &receiverConfirmWait, 20*time.Millisecond)
	log := &logBuffer{}
	lines := newReceiverLog(log, "den")
	var confirmed atomic.Bool

	lines.confirm("generation 2 asks power on; sent power on", time.Now(), switchCheck(&confirmed))

	mustDeepEqual(t, waitForLines(t, log, "Receiver den", 1), []string{
		"Receiver den: generation 2 asks power on; sent power on; the receiver did not report it in 20 ms, and it reports power standby",
	})
	confirmed.Store(true)
	lines.observe()
	mustMatch(t, len(log.lines()), 1)
}

func TestARefusedCommandStatesTheDriversError(t *testing.T) {
	log := &logBuffer{}

	newReceiverLog(log, "den").refused("generation 2 asks power off; sent power standby", errors.New(`no connection to send "PWSTANDBY"`))

	mustDeepEqual(t, log.lines(), []string{
		`Receiver den: generation 2 asks power off; sent power standby; the command failed: no connection to send "PWSTANDBY"`,
	})
}

// A block that a pass sends again with the same value is not a new
// line; a new value is.
func TestFreshAnswersOncePerValue(t *testing.T) {
	lines := newReceiverLog(&logBuffer{}, "den")
	steps := []struct {
		key   string
		value any
		want  bool
	}{
		{"spec.zones.zone2", ZoneSpec{Input: "CD"}, true},
		{"spec.zones.zone2", ZoneSpec{Input: "CD"}, false},
		{"spec.zones.zone3", ZoneSpec{Input: "CD"}, true},
		{"spec.zones.zone2", ZoneSpec{Input: "TV"}, true},
		{"spec.zones.zone2", ZoneSpec{Input: "CD"}, true},
	}
	for _, step := range steps {
		mustMatch(t, lines.fresh(step.key, step.value), step.want)
	}
}

func TestElapsedReadsTheWayAPersonDoes(t *testing.T) {
	cases := []struct {
		duration time.Duration
		want     string
	}{
		{40 * time.Millisecond, "40 ms"},
		{999 * time.Millisecond, "999 ms"},
		{2140 * time.Millisecond, "2.1 s"},
		{25400 * time.Millisecond, "25 s"},
	}
	for _, one := range cases {
		mustMatch(t, elapsed(one.duration), one.want)
	}
}

func TestDeclaredNamesOnlyTheDeclaredValues(t *testing.T) {
	volume, mute := 40.0, true
	cases := []struct {
		block any
		want  string
	}{
		{ZoneSpec{Power: "On", Volume: &volume, Mute: &mute}, `{"mute":true,"power":"On","volume":40}`},
		{map[string]any{"system": map[string]any{}, "tone": map[string]any{"bass": 3}}, `{"tone":{"bass":3}}`},
		{map[string]any{"number": 3}, `{"number":3}`},
	}
	for _, one := range cases {
		mustMatch(t, declared(one.block), one.want)
	}
}
