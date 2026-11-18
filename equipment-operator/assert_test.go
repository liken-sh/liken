package main

import (
	"testing"
	"time"
)

// testTimeout is the limit on a wait in these tests. In a synctest
// bubble the wait runs on the fake clock, so the value only determines
// when a broken test fails.
const testTimeout = 2 * time.Second

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("got error %v, want none", err)
	}
}

func mustFail(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("got no error, want one")
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
