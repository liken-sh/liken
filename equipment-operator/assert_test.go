package main

// The assertion helpers the test files share. Each one fails the test
// with the value it read against the value it wanted, so a failure
// reads without a debugger.

import (
	"testing"
	"time"
)

// The bound on every wait in these tests. A test in a synctest bubble
// waits on the fake clock, so there the bound only decides when a
// broken program fails. The tests against the vivid adapters wait on
// the real clock, so the bound is long enough that a loaded machine
// still passes, and short enough that a broken program fails in
// seconds.
const testTimeout = 2 * time.Second

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wanted no error, got %v", err)
	}
}

func mustFail(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("wanted an error, got none")
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
