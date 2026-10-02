package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// A pass that fails runs again after a backoff that doubles, and a
// pass that succeeds waits for the next wake.
func TestAFailedPassRunsAgainAfterABackoffThatGrows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		wakes := make(chan struct{}, 1)
		var runs []time.Duration
		start := time.Now()
		failures := 3
		done := make(chan struct{})
		go func() {
			defer close(done)
			runPasses(ctx, wakes, func(context.Context) error {
				runs = append(runs, time.Since(start))
				if len(runs) <= failures {
					return errors.New("the API server refused the write")
				}
				return nil
			})
		}()

		wake(wakes)
		time.Sleep(time.Hour)
		synctest.Wait()
		cancel()
		<-done

		want := []time.Duration{0, time.Second, 3 * time.Second, 7 * time.Second}
		if len(runs) != len(want) {
			t.Fatalf("runs at %v, want %v", runs, want)
		}
		for i := range want {
			if runs[i] != want[i] {
				t.Errorf("run %d at %s, want %s", i, runs[i], want[i])
			}
		}
	})
}

func TestABurstOfWakesRunsOnePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		wakes := make(chan struct{}, 1)
		for range 5 {
			wake(wakes)
		}
		passes := 0
		done := make(chan struct{})
		go func() {
			defer close(done)
			runPasses(ctx, wakes, func(context.Context) error { passes++; return nil })
		}()
		synctest.Wait()
		cancel()
		<-done
		if passes != 1 {
			t.Errorf("%d passes, want one", passes)
		}
	})
}
