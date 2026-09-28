package main

// The assertion helpers the test files share. Each one fails the test
// with the value it read against the value it wanted, so a failure
// reads without a debugger.

import (
	"testing"
	"time"
)

// The bound on every wait in these tests: long enough that a loaded
// machine still passes, short enough that a broken program fails in
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
