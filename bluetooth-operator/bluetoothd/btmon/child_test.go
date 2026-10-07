package main

// These tests run a real child process: the test binary itself, in the
// role of a fake btmon that TestMain selects with an environment
// variable. They run outside a synctest bubble, because a process exit
// arrives from the kernel, and each wait has a bound.

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
// "wait" runs until TERM, as btmon does, and "exit" ends at once, as a
// btmon that cannot bind the monitor channel does.
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

// childTimeout bounds each wait for a child to exit.
const childTimeout = 5 * time.Second

// startFake starts the test binary as the fake btmon in mode.
func startFake(t *testing.T, mode string) trace {
	t.Helper()
	t.Setenv(fakeVar, mode)
	child, err := startProcess(os.Args[0])
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}
	t.Cleanup(child.stop)
	return child
}

func expectExit(t *testing.T, child trace) {
	t.Helper()
	select {
	case <-child.done():
	case <-time.After(childTimeout):
		t.Fatalf("the child did not exit within %s", childTimeout)
	}
}

// stop sends TERM and returns once the child has exited.
func TestStopEndsTheChild(t *testing.T) {
	child := startFake(t, "wait")

	child.stop()

	expectExit(t, child)
}

// A child that exits on its own closes done, which is how the
// supervisor learns to start it again.
func TestAChildThatExitsClosesDone(t *testing.T) {
	child := startFake(t, "exit")

	expectExit(t, child)
}

// A program that is not there is a failed start, not a child.
func TestAMissingProgramIsAFailedStart(t *testing.T) {
	if _, err := startProcess("/nonexistent/btmon"); err == nil {
		t.Error("startProcess reported success for a missing program")
	}
}

// The trace runs btmon with the arguments the comment at btmonArgs
// explains.
func TestTheTraceRunsBtmonWithItsArguments(t *testing.T) {
	want := []string{"--no-pager", "--color", "never", "--no-time", "--columns", "160"}
	if btmonPath != "/usr/bin/btmon" || !slices.Equal(btmonArgs, want) {
		t.Errorf("btmon is %s %v, want /usr/bin/btmon %v", btmonPath, btmonArgs, want)
	}
}
