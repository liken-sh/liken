package main

// The repeated write beside the echo wait: it runs until the echo or the
// timeout, a failure does not end the wait, and each write moves the
// finish time.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// shorterNudge shortens the repeat period for one test.
func shorterNudge(t *testing.T, period time.Duration) {
	t.Helper()
	was := echoNudge
	t.Cleanup(func() { echoNudge = was })
	echoNudge = period
}

// countingNudge is a repeat that counts its calls, signals each one, and
// answers with the error given.
func countingNudge(calls *atomic.Int64, nudged chan<- struct{}, err error) func(context.Context) error {
	return func(context.Context) error {
		calls.Add(1)
		select {
		case nudged <- struct{}{}:
		default:
		}
		return err
	}
}

// With no echo, the repeat runs until the timeout.
func TestTheWaitNudgesUntilItsTimeout(t *testing.T) {
	shorterNudge(t, 20*time.Millisecond)
	wait, _, bus := waitingJob(t, workerScan, "scan-1")
	var calls atomic.Int64
	wait.nudge = countingNudge(&calls, nil, nil)

	err := wait.wait(t.Context(), bus, 100*time.Millisecond)

	if err == nil {
		t.Fatal("the wait ended with no error, want the timeout")
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("the wait nudged %d times in five periods, want at least three", got)
	}
}

// The echo ends the wait, and no repeat follows it.
func TestTheEchoEndsTheNudges(t *testing.T) {
	shorterNudge(t, 20*time.Millisecond)
	wait, accepted, bus := waitingJob(t, workerScan, "scan-1")
	var calls atomic.Int64
	nudged := make(chan struct{}, 1)
	wait.nudge = countingNudge(&calls, nudged, nil)
	done := make(chan error, 1)
	go func() { done <- wait.wait(t.Context(), bus, scanTestTimeout) }()

	echoAfterANudge(t, accepted, wait, nudged)

	if err := <-done; err != nil {
		t.Fatalf("the wait ended with %v, want the echo", err)
	}
	ended := calls.Load()
	time.Sleep(60 * time.Millisecond)
	if got := calls.Load(); got != ended {
		t.Errorf("the wait nudged %d times after the echo, want none", got-ended)
	}
}

// A failed repeat writes one log line, and the wait stands until the echo.
func TestAFailedNudgeDoesNotEndTheWait(t *testing.T) {
	shorterNudge(t, 20*time.Millisecond)
	wait, accepted, bus := waitingJob(t, workerScan, "scan-1")
	log := &bytes.Buffer{}
	wait.log = log
	var calls atomic.Int64
	nudged := make(chan struct{}, 1)
	wait.nudge = countingNudge(&calls, nudged, errors.New("the agent refused the run"))
	done := make(chan error, 1)
	go func() { done <- wait.wait(t.Context(), bus, scanTestTimeout) }()

	echoAfterANudge(t, accepted, wait, nudged)

	if err := <-done; err != nil {
		t.Fatalf("the wait ended with %v, want the echo", err)
	}
	if !strings.Contains(log.String(), "the agent refused the run") {
		t.Errorf("the log reads %q, want the refused nudge", log.String())
	}
}

// echoAfterANudge waits for the subscription and the first repeat, then
// publishes the echo.
func echoAfterANudge(t *testing.T, accepted <-chan *fakeBroker, wait *echoWaiter, nudged <-chan struct{}) {
	t.Helper()
	broker := waitForBroker(t, accepted)
	if got := waitForString(t, broker.subs); got != wait.topic {
		t.Fatalf("the Job subscribed to %q, want %q", got, wait.topic)
	}
	select {
	case <-nudged:
	case <-time.After(scanTestTimeout):
		t.Fatal("the wait never nudged")
	}
	broker.push(wait.topic, reportOf(t, libraryRun{
		Worker: wait.worker, Job: wait.job,
		Started: time.Unix(10, 0), Finished: time.Unix(20, 0),
	}))
}

// Every repeat moves the finish time by at least one second, because the
// runs table holds seconds.
func TestTheNextFinishAlwaysMoves(t *testing.T) {
	was := time.Unix(1_700_000_000, 0).UTC()
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{name: "a later second", now: was.Add(10 * time.Second), want: was.Add(10 * time.Second)},
		{name: "the same second", now: was.Add(20 * time.Millisecond), want: was.Add(time.Second)},
		{name: "an earlier second", now: was.Add(-time.Minute), want: was.Add(time.Second)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := nextFinish(was, testCase.now); !got.Equal(testCase.want) {
				t.Errorf("nextFinish = %v, want %v", got, testCase.want)
			}
		})
	}
}

// The scan Job writes its finished run again while it waits, with a later
// finish time each time.
func TestTheScanJobNudgesItsFinishedRun(t *testing.T) {
	shorterNudge(t, 20*time.Millisecond)
	scan, recorder, accepted := scanJob(t, "testdata/movies", libraryKindMovies, "")
	done := make(chan error, 1)
	go func() { done <- scan.runJob(t.Context()) }()

	broker := waitForBroker(t, accepted)
	if got := waitForString(t, broker.subs); got != scan.echo.topic {
		t.Fatalf("the Job subscribed to %q, want %q", got, scan.echo.topic)
	}
	runs := runsAfterANudge(t, recorder)
	broker.push(scan.echo.topic, reportOf(t, libraryRun{
		Worker: workerScan, Job: "scan-1",
		Started: time.Unix(10, 0), Finished: time.Unix(20, 0),
	}))
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	if finishOf(runs[1]) == 0 {
		t.Fatalf("the finished run carries no finish time: %v", runs[1].params)
	}
	if finishOf(runs[2]) <= finishOf(runs[1]) {
		t.Errorf("the nudge finished at %v, want a time past the finished run's %v",
			finishOf(runs[2]), finishOf(runs[1]))
	}
	if runs[2].params[1] != workerScan || runs[2].params[2] != "scan-1" {
		t.Errorf("the nudge names %v/%v, want the scan worker and this Job", runs[2].params[1], runs[2].params[2])
	}
}

// runsAfterANudge reads the posted runs once the third write has landed.
func runsAfterANudge(t *testing.T, recorder *catalogRecorder) []capturedStatement {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		if posted := runsPosted(recorder); len(posted) >= 3 {
			return posted
		}
		select {
		case <-deadline:
			t.Fatal("the job never nudged its finished run")
		case <-time.After(time.Millisecond):
		}
	}
}

// finishOf is the finish time one posted run carries, as the wire holds it.
func finishOf(statement capturedStatement) float64 {
	seconds, _ := statement.params[4].(float64)
	return seconds
}
