package main

// Tests of receiverLog: it holds a command's line until the receiver
// reports the result, and it formats the values in the lines.

import (
	"errors"
	"regexp"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// timeless replaces each measured duration in lines with "<time>".
func timeless(lines []string) []string {
	measured := regexp.MustCompile(`after \d+(\.\d)? m?s\b`)
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned = append(cleaned, measured.ReplaceAllString(line, "after <time>"))
	}
	return cleaned
}

// waitForLines waits until the log has count lines that contain text,
// and returns them with the durations replaced.
func waitForLines(t *testing.T, log *logBuffer, text string, count int) []string {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for len(linesWith(log, text)) < count {
		if time.Now().After(deadline) {
			t.Fatalf("the log holds %q, want %d lines with %q", log.lines(), count, text)
		}
		time.Sleep(time.Millisecond)
	}
	return timeless(linesWith(log, text))
}

// switchCheck returns a check that passes when the test sets confirmed.
func switchCheck(confirmed *atomic.Bool) func() (string, bool) {
	return func() (string, bool) {
		if confirmed.Load() {
			return "power on", true
		}
		return "power standby", false
	}
}

func TestACommandsLineWaitsForTheReportAndPrintsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}
		lines := newReceiverLog(log, "den")
		var confirmed atomic.Bool

		lines.confirm("generation 2 requests power on; sent power on", time.Now(), switchCheck(&confirmed))
		lines.observe()
		mustDeepEqual(t, linesWith(log, "Receiver"), []string(nil))

		confirmed.Store(true)
		lines.observe()
		lines.observe()

		mustDeepEqual(t, timeless(log.lines()), []string{
			"Receiver den: generation 2 requests power on; sent power on; the receiver reported power on after <time>",
		})
	})
}

func TestACommandTheReceiverAlreadyReportsPrintsAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}
		var confirmed atomic.Bool
		confirmed.Store(true)

		newReceiverLog(log, "den").confirm("generation 2 requests power on; sent power on", time.Now(), switchCheck(&confirmed))

		mustDeepEqual(t, timeless(log.lines()), []string{
			"Receiver den: generation 2 requests power on; sent power on; the receiver reported power on after <time>",
		})
	})
}

func TestACommandTheReceiverNeverReportsPrintsWhenTheWaitEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}
		lines := newReceiverLog(log, "den")
		var confirmed atomic.Bool

		lines.confirm("generation 2 requests power on; sent power on", time.Now(), switchCheck(&confirmed))
		time.Sleep(receiverConfirmWait - time.Millisecond)
		synctest.Wait()
		mustMatch(t, log.lines()[0], "")
		time.Sleep(time.Millisecond)

		mustDeepEqual(t, waitForLines(t, log, "Receiver den", 1), []string{
			"Receiver den: generation 2 requests power on; sent power on; the receiver did not report it in 15 s, and it reports power standby",
		})
		confirmed.Store(true)
		lines.observe()
		mustMatch(t, len(log.lines()), 1)
	})
}

// A report that arrives as the wait ends prints no second line. The
// report's check is still reading the receiver when the timer prints
// the line, and the report then finds the line already removed.
func TestAReportThatMeetsTheEndOfTheWaitPrintsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}
		lines := newReceiverLog(log, "den")
		reading, release := make(chan struct{}), make(chan struct{})
		var checks atomic.Int32
		check := func() (string, bool) {
			if checks.Add(1) == 2 {
				close(reading)
				<-release
				return "power on", true
			}
			return "power standby", false
		}
		lines.confirm("generation 2 requests power on; sent power on", time.Now(), check)
		reported := make(chan struct{})
		go func() {
			defer close(reported)
			lines.observe()
		}()
		<-reading

		time.Sleep(receiverConfirmWait)
		synctest.Wait()
		close(release)
		<-reported

		mustDeepEqual(t, log.lines(), []string{
			"Receiver den: generation 2 requests power on; sent power on; the receiver did not report it in 15 s, and it reports power standby",
		})
	})
}

func TestARefusedCommandStatesTheDriversError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		log := &logBuffer{}

		newReceiverLog(log, "den").refused("generation 2 requests power off; sent power standby", errors.New(`the receiver is not connected, so "PWSTANDBY" was not sent`))

		mustDeepEqual(t, log.lines(), []string{
			`Receiver den: generation 2 requests power off; sent power standby; the command was not applied: the receiver is not connected, so "PWSTANDBY" was not sent`,
		})
	})
}

// A block that a pass sends again with the same value is not logged
// again, and a new value is.
func TestFreshIsTrueOnlyForANewValue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestElapsedFormatsDurationsForALogLine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestDeclaredOmitsEmptyObjects(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
	})
}
