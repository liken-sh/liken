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

// stalled is a recorder whose API server holds each Event request until
// the test lets it through.
type stalled struct {
	recording
	through chan struct{}
}

func stall(t *testing.T) *stalled {
	s := &stalled{through: make(chan struct{})}
	s.held = &eventstest.Events{}
	s.server = apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-s.through
		s.held.ServeHTTP(w, r)
	}))
	s.log = &syncBuffer{}
	client := apiclient.New(apiservertest.Host, s.server.Client(), "")
	s.recorder = events.New(t.Context(), client, "test-operator", events.Options{Instance: "node-1", Log: s.log})
	return s
}

// shutDown runs Shutdown in its own goroutine, so the test can look
// while it waits.
func shutDown(ctx context.Context, recorder *events.Recorder) <-chan error {
	returned := make(chan error, 1)
	go func() { returned <- recorder.Shutdown(ctx) }()
	return returned
}

// Shutdown returns only after the write in flight has returned, so an
// owner that releases its Lease after Shutdown writes nothing after the
// release.
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
		landed := len(s.held.List())
		synctest.Wait()

		if err != nil || landed != 1 || len(s.held.List()) != 1 {
			t.Errorf("Shutdown answered %v with %d Events held, and %d after; want nil, 1, and 1", err, landed, len(s.held.List()))
		}
	})
}

// Shutdown writes each Event still queued, the way client-go's
// broadcaster delivers what it queued before its Shutdown.
func TestShutdownWritesTheQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)

		r.recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
		r.recorder.Normal(mount, "Reachable", "The mount answered.")
		err := r.recorder.Shutdown(t.Context())

		if got := r.held.List(); err != nil || len(got) != 3 {
			t.Errorf("Shutdown answered %v and the server holds %d Events, want nil and 3", err, len(got))
		}
	})
}

// After Shutdown, a write is sent once. A refused write is logged, not
// sent again, so a shutdown does not wait out the retries.
func TestShutdownSendsEachWriteOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := stall(t)
		s.recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()
		s.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
		s.recorder.Normal(mount, "Reachable", "The mount answered.")
		s.held.Refuse(2)
		start := time.Now()

		returned := shutDown(t.Context(), s.recorder)
		synctest.Wait()
		close(s.through)
		err := <-returned

		got := s.held.List()
		if err != nil || len(got) != 1 || got[0].Reason != "Reachable" || time.Since(start) != 0 {
			t.Errorf("Shutdown answered %v after %v, and the server holds %+v; want nil at once and only the Reachable Event", err, time.Since(start), got)
		}
		for _, reason := range []string{"Normal Event PodCreated", "Warning Event Unreachable"} {
			if !strings.Contains(s.log.String(), "writing the "+reason) {
				t.Errorf("the log says %q, want the failure of the %s", s.log.String(), reason)
			}
		}
	})
}

// A Shutdown during the retry wait ends the wait, and gives up on the
// Event that waits, as client-go's broadcaster does.
func TestShutdownEndsTheRetryWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		r.held.Refuse(1)
		r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
		synctest.Wait()
		start := time.Now()

		err := r.recorder.Shutdown(t.Context())

		if err != nil || time.Since(start) != 0 || len(r.held.List()) != 0 {
			t.Errorf("Shutdown answered %v after %v with %d Events held, want nil at once and none", err, time.Since(start), len(r.held.List()))
		}
		if strings.Count(r.log.String(), "\n") != 1 {
			t.Errorf("the log says %q, want one line about the Event", r.log.String())
		}
	})
}

// An Event posted after Shutdown is dropped and logged, because nothing
// writes it.
func TestAnEventAfterShutdownIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		synctest.Wait()
		_ = r.recorder.Shutdown(t.Context())

		r.recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()

		if len(r.held.List()) != 0 || !strings.Contains(r.log.String(), "dropped the Normal Event PodCreated on the Mount east-mount: the recorder has shut down") {
			t.Errorf("the server holds %d Events and the log says %q, want none and one drop", len(r.held.List()), r.log.String())
		}
	})
}

// When the context of Shutdown ends first, Shutdown answers at once that
// a write can still be in flight, so the owner keeps its Lease. The
// recorder sends nothing after the write in flight returns, and logs how
// many Events it did not write.
func TestShutdownAnswersWhenItsContextEnds(t *testing.T) {
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
			t.Errorf("Shutdown answered %v after %v, want the deadline after %v", err, waited, events.ShutdownTimeout)
		}
		if got := s.held.List(); len(got) != 1 || got[0].Reason != "PodCreated" {
			t.Errorf("the server holds %+v, want only the Event that was in flight", got)
		}
		if !strings.Contains(s.log.String(), "queued Events were not written: 1") {
			t.Errorf("the log says %q, want the one Event not written", s.log.String())
		}
	})
}

// A recorder whose own context ended has stopped, so Shutdown answers at
// once. A nil recorder has nothing to shut down.
func TestShutdownOfAStoppedRecorder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		held := &eventstest.Events{}
		server := apiservertest.Start(t, held.Around(notFound))
		ctx, cancel := context.WithCancel(t.Context())
		recorder := events.New(ctx, apiclient.New(apiservertest.Host, server.Client(), ""), "test-operator", events.Options{})
		cancel()
		synctest.Wait()

		var none *events.Recorder
		if err, nilErr := recorder.Shutdown(t.Context()), none.Shutdown(t.Context()); err != nil || nilErr != nil {
			t.Errorf("Shutdown answered %v, and %v for a nil recorder, want nil", err, nilErr)
		}
	})
}

// The recorder's own context still ends its work during a Shutdown: the
// request in flight ends at once, and the queue is not written.
func TestTheRecordersContextEndsAShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		held := &eventstest.Events{}
		through := make(chan struct{})
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-through
			held.ServeHTTP(w, r)
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

		if got := held.List(); err != nil || len(got) != 1 || got[0].Reason != "PodCreated" {
			t.Errorf("Shutdown answered %v and the server holds %+v, want nil and only the Event that was in flight", err, got)
		}
	})
}
