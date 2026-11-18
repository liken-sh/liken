package events_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// stalled is a recorder whose API server blocks each Event request
// until the test releases it.
type stalled struct {
	recording
	through chan struct{}
}

func stall(t *testing.T) *stalled {
	s := &stalled{through: make(chan struct{})}
	s.stored = &eventstest.Events{}
	s.server = apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-s.through
		s.stored.ServeHTTP(w, r)
	}))
	s.log = &syncBuffer{}
	client := apiclient.New(apiservertest.Host, s.server.Client(), "")
	s.recorder = events.New(t.Context(), client, "test-operator", events.Options{Instance: "node-1", Log: s.log})
	return s
}

// shutDown runs Shutdown in its own goroutine, so the test can inspect
// the recorder while Shutdown waits.
func shutDown(ctx context.Context, recorder *events.Recorder) <-chan error {
	returned := make(chan error, 1)
	go func() { returned <- recorder.Shutdown(ctx) }()
	return returned
}

// Shutdown returns only after the in-flight write has returned, so an
// owner that releases its Lease after Shutdown has no Event write
// still running after the release.
func TestShutdownWaitsForTheWriteInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := stall(t)
		s.recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()

		returned := shutDown(t.Context(), s.recorder)
		synctest.Wait()
		select {
		case err := <-returned:
			t.Fatalf("Shutdown returned %v while a write was in flight", err)
		default:
		}
		close(s.through)
		err := <-returned
		written := len(s.stored.List())
		synctest.Wait()

		if err != nil || written != 1 || len(s.stored.List()) != 1 {
			t.Errorf("Shutdown returned %v with %d Events on the server, and %d after the write returned; want nil, 1, and 1", err, written, len(s.stored.List()))
		}
	})
}

// Shutdown writes each queued Event, as client-go's broadcaster
// delivers its queue before it shuts down.
func TestShutdownWritesTheQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)

		r.recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
		r.recorder.Normal(mount, "Reachable", "The mount responds")
		err := r.recorder.Shutdown(t.Context())

		if got := r.stored.List(); err != nil || len(got) != 3 {
			t.Errorf("Shutdown returned %v and the server has %d Events, want nil and 3", err, len(got))
		}
	})
}

// During Shutdown, each write is sent once. A rejected write is logged
// and not retried, so Shutdown does not wait through the retries.
func TestShutdownSendsEachWriteOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := stall(t)
		s.recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()
		s.recorder.Warning(mount, "Unreachable", "The mount does not respond")
		s.recorder.Normal(mount, "Reachable", "The mount responds")
		s.stored.Refuse(2)
		start := time.Now()

		returned := shutDown(t.Context(), s.recorder)
		synctest.Wait()
		close(s.through)
		err := <-returned

		got := s.stored.List()
		if err != nil || len(got) != 1 || got[0].Reason != "Reachable" || time.Since(start) != 0 {
			t.Errorf("Shutdown returned %v after %v, and the server has %+v, want nil at once and only the Reachable Event", err, time.Since(start), got)
		}
		for _, reason := range []string{"Normal Event PodCreated", "Warning Event Unreachable"} {
			if !strings.Contains(s.log.String(), "writing the "+reason) {
				t.Errorf("the log says %q, want the failure of the %s", s.log.String(), reason)
			}
		}
	})
}

// A Shutdown during the retry wait ends the wait and abandons the Event
// that was waiting to retry, as client-go's broadcaster does.
func TestShutdownEndsTheRetryWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		r.stored.Refuse(1)
		r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
		synctest.Wait()
		start := time.Now()

		err := r.recorder.Shutdown(t.Context())

		if err != nil || time.Since(start) != 0 || len(r.stored.List()) != 0 {
			t.Errorf("Shutdown returned %v after %v with %d Events on the server, want nil at once and none", err, time.Since(start), len(r.stored.List()))
		}
		if strings.Count(r.log.String(), "\n") != 1 {
			t.Errorf("the log says %q, want one line about the Event", r.log.String())
		}
	})
}

// An Event posted after Shutdown is dropped and logged, because the
// recorder no longer writes.
func TestAnEventAfterShutdownIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		synctest.Wait()
		_ = r.recorder.Shutdown(t.Context())

		r.recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()

		if len(r.stored.List()) != 0 || !strings.Contains(r.log.String(), "dropped the Normal Event PodCreated on the Mount observatory/east-mount: the recorder has shut down") {
			t.Errorf("the server holds %d Events and the log says %q, want none and one drop", len(r.stored.List()), r.log.String())
		}
	})
}

// When Shutdown's context ends first, Shutdown returns an error at
// once, because a write can still be in flight and the owner must keep
// its Lease. The recorder sends nothing after the in-flight write
// returns, and logs how many Events it did not write.
func TestShutdownReturnsWhenItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := stall(t)
		s.recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		s.recorder.Normal(mount, "PodDeleted", "Deleted pod east-mount.")
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), events.ShutdownTimeout)
		defer cancel()
		start := time.Now()

		err := s.recorder.Shutdown(ctx)
		waited := time.Since(start)
		close(s.through)
		synctest.Wait()

		if !errors.Is(err, context.DeadlineExceeded) || waited != events.ShutdownTimeout {
			t.Errorf("Shutdown returned %v after %v, want the deadline after %v", err, waited, events.ShutdownTimeout)
		}
		if got := s.stored.List(); len(got) != 1 || got[0].Reason != "PodCreated" {
			t.Errorf("the server holds %+v, want only the Event that was in flight", got)
		}
		if !strings.Contains(s.log.String(), "queued Events were not written: 1") {
			t.Errorf("the log says %q, want the one Event not written", s.log.String())
		}
	})
}

// A recorder whose own context has ended has already stopped, so
// Shutdown returns at once. Shutdown on a nil recorder does nothing.
func TestShutdownOfAStoppedRecorder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stored := &eventstest.Events{}
		server := apiservertest.Start(t, stored.Around(notFound))
		ctx, cancel := context.WithCancel(t.Context())
		recorder := events.New(ctx, apiclient.New(apiservertest.Host, server.Client(), ""), "test-operator", events.Options{})
		cancel()
		synctest.Wait()

		var none *events.Recorder
		if err, nilErr := recorder.Shutdown(t.Context()), none.Shutdown(t.Context()); err != nil || nilErr != nil {
			t.Errorf("Shutdown returned %v, and %v for a nil recorder, want nil", err, nilErr)
		}
	})
}

// The recorder's own context still stops its work during a Shutdown:
// the in-flight request is canceled, and the queued Events are not
// sent.
func TestTheRecordersContextEndsAShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stored := &eventstest.Events{}
		through := make(chan struct{})
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-through
			stored.ServeHTTP(w, r)
		}))
		ctx, cancel := context.WithCancel(t.Context())
		recorder := events.New(ctx, apiclient.New(apiservertest.Host, server.Client(), ""), "test-operator", events.Options{Log: &syncBuffer{}})
		recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		recorder.Normal(mount, "PodDeleted", "Deleted pod east-mount.")
		synctest.Wait()

		returned := shutDown(t.Context(), recorder)
		synctest.Wait()
		cancel()
		err := <-returned
		close(through)
		synctest.Wait()

		if got := stored.List(); err != nil || len(got) != 1 || got[0].Reason != "PodCreated" {
			t.Errorf("Shutdown returned %v and the server holds %+v, want nil and only the Event that was in flight", err, got)
		}
	})
}
