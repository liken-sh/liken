package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// A runner that waits for a lock gives up when its step's deadline
// passes, and the lock goes to the next runner after a release.
func TestAWaiterGivesUpOnALockAtItsDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := newLock()
		if err := l.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		began := time.Now()
		err := l.acquire(ctx)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(began) != time.Minute {
			t.Errorf("acquire answered %v after %v, want the deadline after 1m0s", err, time.Since(began))
		}
		l.release()
		if err := l.acquire(t.Context()); err != nil {
			t.Errorf("acquire after a release answered %v", err)
		}
	})
}
