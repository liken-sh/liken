package main

// These tests run a real child process: the test binary itself acts as
// a fake btmon, which TestMain selects with an environment variable.
// They run outside a synctest bubble because a process exit comes from
// the kernel, which a fake clock does not control. Each wait has a
// timeout.

import (
	"os"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"
)

// fakeVar selects the fake btmon in a child of the test binary.
const fakeVar = "START_BTMON_FAKE"

// TestMain runs the fake btmon when the test binary starts as a child.
// "wait" runs until TERM, as btmon does. "exit" exits at once with a
// failure, as btmon does when it cannot bind the monitor channel.
func TestMain(m *testing.M) {
	switch os.Getenv(fakeVar) {
	case "wait":
		terms := make(chan os.Signal, 1)
		signal.Notify(terms, syscall.SIGTERM)
		<-terms
		os.Exit(0)
	case "exit":
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// childTimeout is how long expectExit waits for a child to exit.
const childTimeout = 5 * time.Second

// startFake starts the test binary as the fake btmon in mode.
func startFake(t *testing.T, mode string) trace {
	t.Helper()
	t.Setenv(fakeVar, mode)
	child, err := startProcess(os.Args[0])
	if err != nil {
		t.Fatalf("starting the fake btmon: %v", err)
	}
	t.Cleanup(child.stop)
	return child
}

func expectExit(t *testing.T, child trace) {
	t.Helper()
	select {
	case <-child.done():
	case <-time.After(childTimeout):
		t.Fatalf("the fake btmon did not exit within %s", childTimeout)
	}
}

func TestStopEndsTheChild(t *testing.T) {
	child := startFake(t, "wait")

	child.stop()

	expectExit(t, child)
}

// The supervisor starts btmon again after done closes.
func TestDoneClosesWhenTheChildExitsOnItsOwn(t *testing.T) {
	child := startFake(t, "exit")

	expectExit(t, child)
}

func TestStartProcessReturnsAnErrorForAMissingProgram(t *testing.T) {
	if _, err := startProcess("/nonexistent/btmon"); err == nil {
		t.Error("startProcess(\"/nonexistent/btmon\") returned no error, want one for a missing program")
	}
}

func TestBtmonRunsWithItsPathAndArguments(t *testing.T) {
	want := []string{"--no-pager", "--color", "never", "--no-time", "--columns", "160"}
	if btmonPath != "/usr/bin/btmon" || !slices.Equal(btmonArgs, want) {
		t.Errorf("btmon is %s %v, want /usr/bin/btmon %v", btmonPath, btmonArgs, want)
	}
}
