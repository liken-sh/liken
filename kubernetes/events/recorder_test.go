package events_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// recording is a recorder that writes to a fake API server, and what
// the server holds.
type recording struct {
	recorder *events.Recorder
	server   *apiservertest.Server
	held     *eventstest.Events
	log      *syncBuffer
}

// syncBuffer is a log that the recorder's goroutine writes while the
// test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// notFound answers every request that is not about an Event.
var notFound = http.NotFoundHandler()

// record starts a recorder against a fake API server that serves only
// Events. next serves the other requests.
func record(t *testing.T, next http.Handler) *recording {
	held := &eventstest.Events{}
	server := apiservertest.Start(t, held.Around(next))
	log := &syncBuffer{}
	client := apiclient.New(apiservertest.Host, server.Client(), "")
	recorder := events.New(t.Context(), client, "test-operator", events.Options{Instance: "node-1", Log: log})
	return &recording{recorder: recorder, server: server, held: held, log: log}
}

var mount = events.ObjectReference{APIVersion: "observatory.liken.sh/v1alpha1", Kind: "Mount", Namespace: "observatory", Name: "east-mount", UID: "uid-1"}

// A Normal and a Warning each become one core/v1 Event about the
// object, in its namespace, from the component and its instance.
func TestAnEventNamesItsObjectAndItsSource(t *testing.T) {
	cases := []struct {
		name string
		post func(*events.Recorder)
		kind string
	}{
		{"Normal", func(r *events.Recorder) { r.Normal(mount, "PodCreated", "Created pod east-mount.") }, events.TypeNormal},
		{"Warning", func(r *events.Recorder) { r.Warning(mount, "PodCreated", "Created pod east-mount.") }, events.TypeWarning},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := record(t, notFound)
				now := time.Now().UTC()

				c.post(r.recorder)
				synctest.Wait()

				want := events.Event{
					APIVersion: "v1", Kind: "Event",
					Metadata:       events.Metadata{Name: "east-mount.00001", GenerateName: "east-mount.", Namespace: "observatory", ResourceVersion: "1"},
					InvolvedObject: mount,
					Reason:         "PodCreated", Message: "Created pod east-mount.", Type: c.kind,
					Source:         events.Source{Component: "test-operator", Host: "node-1"},
					FirstTimestamp: now, LastTimestamp: now, Count: 1,
					ReportingComponent: "test-operator", ReportingInstance: "node-1",
				}
				if got := r.held.List(); len(got) != 1 || got[0] != want {
					t.Errorf("the server holds %+v\nwant %+v", got, want)
				}
			})
		})
	}
}

// The API server accepts an Event about a cluster-scoped object only in
// default or kube-system, so the recorder writes it in default.
func TestAnEventAboutAClusterScopedObjectGoesInDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		machine := events.ObjectReference{APIVersion: "liken.sh/v1alpha1", Kind: "Machine", Name: "node-1", UID: "uid-2"}

		r.recorder.Warning(machine, "MachineLost", "The heartbeat stopped.")
		synctest.Wait()

		got := r.held.About("Machine", "node-1")
		if len(got) != 1 || got[0].Metadata.Namespace != "default" || got[0].InvolvedObject.Namespace != "" {
			t.Errorf("the server holds %+v, want one Event in default about the cluster-scoped Machine", got)
		}
	})
}

// A repeat within the window patches the Event's count and last time,
// so `kubectl describe` prints one line. A repeat after the window
// starts a new Event.
func TestARepeatWithinTheWindowCountsOnOneEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		first := time.Now().UTC()

		for range 3 {
			r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
			synctest.Wait()
			time.Sleep(time.Minute)
		}
		time.Sleep(11 * time.Minute)
		r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
		synctest.Wait()

		got := r.held.List()
		if len(got) != 2 {
			t.Fatalf("the server holds %d Events, want 2", len(got))
		}
		if got[0].Count != 3 || !got[0].FirstTimestamp.Equal(first) || !got[0].LastTimestamp.Equal(first.Add(2*time.Minute)) {
			t.Errorf("the first Event counts %d from %v to %v, want 3 from %v to %v", got[0].Count, got[0].FirstTimestamp, got[0].LastTimestamp, first, first.Add(2*time.Minute))
		}
		if got[1].Count != 1 || !got[1].FirstTimestamp.Equal(first.Add(14*time.Minute)) {
			t.Errorf("the second Event counts %d from %v, want 1 from %v", got[1].Count, got[1].FirstTimestamp, first.Add(14*time.Minute))
		}
	})
}

// Only the same object, type, reason, and message is a repeat.
func TestADifferentEventIsNotARepeat(t *testing.T) {
	other := mount
	other.Name, other.UID = "west-mount", "uid-3"
	cases := []struct {
		name string
		post func(*events.Recorder)
	}{
		{"another object", func(r *events.Recorder) { r.Warning(other, "Unreachable", "The mount did not answer.") }},
		{"another type", func(r *events.Recorder) { r.Normal(mount, "Unreachable", "The mount did not answer.") }},
		{"another reason", func(r *events.Recorder) { r.Warning(mount, "Refused", "The mount did not answer.") }},
		{"another message", func(r *events.Recorder) { r.Warning(mount, "Unreachable", "The mount refused.") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := record(t, notFound)

				r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
				c.post(r.recorder)
				synctest.Wait()

				if got := r.held.List(); len(got) != 2 || got[0].Count != 1 || got[1].Count != 1 {
					t.Errorf("the server holds %+v, want two Events that count 1 each", got)
				}
			})
		})
	}
}

// A repeat after the TTL deleted the Event creates a new one.
func TestARepeatAfterTheTTLCreatesANewEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
		synctest.Wait()
		r.held.Expire()

		r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
		synctest.Wait()

		if got := r.held.List(); len(got) != 1 || got[0].Count != 1 {
			t.Errorf("the server holds %+v, want one new Event that counts 1", got)
		}
	})
}

// A write that the API server refuses goes again after the retry wait,
// so an API server that restarts loses no Event and no count.
func TestARefusedWriteIsSentAgain(t *testing.T) {
	cases := []struct {
		name  string
		count int32
	}{
		{"a create", 1},
		{"a patch", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := record(t, notFound)
				for range c.count - 1 {
					r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
					synctest.Wait()
				}
				r.held.Refuse(2)

				r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
				time.Sleep(time.Minute)
				synctest.Wait()

				if got := r.held.List(); len(got) != 1 || got[0].Count != c.count || r.log.String() != "" {
					t.Errorf("the server holds %+v and the log says %q, want one Event that counts %d and no log", got, r.log.String(), c.count)
				}
			})
		})
	}
}

// A write that fails every attempt costs one log line, and the next
// Event is written.
func TestAWriteThatFailsEveryAttemptIsLoggedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		r.held.Refuse(3)

		r.recorder.Warning(mount, "Unreachable", "The mount did not answer.")
		r.recorder.Normal(mount, "Reachable", "The mount answered.")
		time.Sleep(time.Minute)
		synctest.Wait()

		lines := strings.Split(strings.TrimSpace(r.log.String()), "\n")
		if len(lines) != 1 || !strings.HasPrefix(lines[0], "test-operator: writing the Warning Event Unreachable on the Mount east-mount: ") {
			t.Errorf("the log says %q, want one line about the Unreachable Event", lines)
		}
		if got := r.held.List(); len(got) != 1 || got[0].Reason != "Reachable" {
			t.Errorf("the server holds %+v, want only the Reachable Event", got)
		}
	})
}

// While the API server does not answer, the queue fills, and the
// recorder drops each Event past it without waiting. It logs the first
// drop and counts each one.
func TestAFullQueueDropsAndCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stalled := make(chan struct{})
		held := &eventstest.Events{}
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-stalled
			held.ServeHTTP(w, r)
		}))
		log := &syncBuffer{}
		recorder := events.New(t.Context(), apiclient.New(apiservertest.Host, server.Client(), ""), "test-operator", events.Options{Instance: "node-1", Log: log})

		// One Event is in flight, 256 wait, and 4 are dropped.
		for i := range 261 {
			recorder.Normal(mount, "Moved", "Moved "+string(rune('a'+i%26)))
			synctest.Wait()
		}
		close(stalled)

		if recorder.Dropped() != 4 || strings.Count(log.String(), "dropped") != 1 {
			t.Errorf("dropped %d and logged %q, want 4 dropped and one line", recorder.Dropped(), log.String())
		}
	})
}

// A nil recorder posts nothing, so a program with no API server can
// pass one.
func TestANilRecorderPostsNothing(t *testing.T) {
	var recorder *events.Recorder

	recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
	recorder.Warning(mount, "Unreachable", "The mount did not answer.")

	if recorder.Dropped() != 0 {
		t.Errorf("a nil recorder dropped %d", recorder.Dropped())
	}
}

// The API server refuses a reason past 128 bytes and, with eventTime,
// a note past 1024 bytes. The recorder cuts each to fit, at a
// character boundary.
func TestALongReasonAndMessageAreCut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		reason := strings.Repeat("Moved", 30)
		message := strings.Repeat("é", 600)

		r.recorder.Normal(mount, reason, message)
		r.recorder.Normal(mount, "Short", "Short.")
		synctest.Wait()

		got := r.held.List()
		if len(got) != 2 {
			t.Fatalf("the server holds %d Events, want 2", len(got))
		}
		if len(got[0].Reason) > 128 || !strings.HasPrefix(got[0].Reason, "MovedMoved") || !strings.HasSuffix(got[0].Reason, "…") {
			t.Errorf("the reason is %d bytes: %q", len(got[0].Reason), got[0].Reason)
		}
		if len(got[0].Message) > 1024 || !strings.HasPrefix(got[0].Message, "éé") || !strings.HasSuffix(got[0].Message, "é…") {
			t.Errorf("the message is %d bytes: %q", len(got[0].Message), got[0].Message)
		}
		if got[1].Reason != "Short" || got[1].Message != "Short." {
			t.Errorf("a short Event became %q: %q", got[1].Reason, got[1].Message)
		}
	})
}

// The recorder remembers at most 4096 series. A repeat of a series it
// forgot creates a new Event in place of a patch.
func TestTheOldestSeriesIsForgotten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		post := func(i int) {
			r.recorder.Normal(mount, "Moved", "Moved to step "+strconv.Itoa(i))
			synctest.Wait()
		}

		post(0)
		post(1)
		for i := range 4095 {
			post(i + 2)
		}
		post(1)
		post(0)

		got := r.held.List()
		if len(got) != 4098 || got[1].Count != 2 || got[4097].Message != "Moved to step 0" || got[4097].Count != 1 {
			t.Errorf("the server holds %d Events; step 1 counts %d; the last is %q and counts %d; want 4098, 2, step 0, and 1",
				len(got), got[1].Count, got[len(got)-1].Message, got[len(got)-1].Count)
		}
	})
}

// A recorder whose context ends during the retry wait stops at once,
// and logs nothing: the program is stopping.
func TestARecorderStopsDuringTheRetryWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		held := &eventstest.Events{}
		held.Refuse(1)
		server := apiservertest.Start(t, held.Around(notFound))
		ctx, cancel := context.WithCancel(t.Context())
		log := &syncBuffer{}
		recorder := events.New(ctx, apiclient.New(apiservertest.Host, server.Client(), ""), "test-operator", events.Options{Log: log})

		recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()
		cancel()
		synctest.Wait()

		if got := held.List(); len(got) != 0 || log.String() != "" {
			t.Errorf("the server holds %+v and the log says %q, want nothing", got, log.String())
		}
	})
}

// With no instance, the recorder names the host, which is the pod's
// name in a pod.
func TestTheInstanceIsTheHostNameByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		held := &eventstest.Events{}
		server := apiservertest.Start(t, held.Around(notFound))
		recorder := events.New(t.Context(), apiclient.New(apiservertest.Host, server.Client(), ""), "test-operator", events.Options{})
		host, _ := os.Hostname()

		recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()

		if got := held.List(); len(got) != 1 || got[0].ReportingInstance != host || got[0].Source.Host != host {
			t.Errorf("the server holds %+v, want one Event from %q", got, host)
		}
	})
}
