package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// A waiter returns the context's cause when its deadline passes, and a
// later acquire succeeds after the holder releases.
func TestAWaiterGivesUpOnALockAtItsDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := newLock()
		if err := l.acquire(t.Context()); err != nil {
			t.Fatalf("the first acquire: %v", err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		began := time.Now()
		err := l.acquire(ctx)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(began) != time.Minute {
			t.Errorf("acquire returned %v after %v, want the deadline after 1m0s", err, time.Since(began))
		}
		l.release()
		if err := l.acquire(t.Context()); err != nil {
			t.Errorf("acquire after a release returned %v, want no error", err)
		}
	})
}
