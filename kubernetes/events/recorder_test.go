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

type recording struct {
	recorder *events.Recorder
	server   *apiservertest.Server
	stored   *eventstest.Events
	log      *syncBuffer
}

// syncBuffer is a log that the test reads while the recorder's
// goroutine writes it.
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

var notFound = http.NotFoundHandler()

// record starts a recorder against a fake API server that serves
// Events, and passes every other request to next.
func record(t *testing.T, next http.Handler) *recording {
	stored := &eventstest.Events{}
	server := apiservertest.Start(t, stored.Around(next))
	log := &syncBuffer{}
	client := apiclient.New(apiservertest.Host, server.Client(), "")
	recorder := events.New(t.Context(), client, "test-operator", events.Options{Instance: "node-1", Log: log})
	return &recording{recorder: recorder, server: server, stored: stored, log: log}
}

var mount = events.ObjectReference{APIVersion: "observatory.liken.sh/v1alpha1", Kind: "Mount", Namespace: "observatory", Name: "east-mount", UID: "uid-1"}

// Normal and Warning each write one core/v1 Event about the object, in
// the object's namespace, with the component and instance as source.
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
				if got := r.stored.List(); len(got) != 1 || got[0] != want {
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

		got := r.stored.About("Machine", "node-1")
		if len(got) != 1 || got[0].Metadata.Namespace != "default" || got[0].InvolvedObject.Namespace != "" {
			t.Errorf("the server holds %+v, want one Event in default about the cluster-scoped Machine", got)
		}
	})
}

// A repeat within the window patches the Event's count and last
// timestamp, so `kubectl describe` prints one line. A repeat after the
// window creates a new Event.
func TestARepeatWithinTheWindowCountsOnOneEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		first := time.Now().UTC()

		for range 3 {
			r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
			synctest.Wait()
			time.Sleep(time.Minute)
		}
		time.Sleep(11 * time.Minute)
		r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
		synctest.Wait()

		got := r.stored.List()
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

// An Event is a repeat only when its object, type, reason, and message
// all match.
func TestADifferentEventIsNotARepeat(t *testing.T) {
	other := mount
	other.Name, other.UID = "west-mount", "uid-3"
	cases := []struct {
		name string
		post func(*events.Recorder)
	}{
		{"another object", func(r *events.Recorder) { r.Warning(other, "Unreachable", "The mount does not respond") }},
		{"another type", func(r *events.Recorder) { r.Normal(mount, "Unreachable", "The mount does not respond") }},
		{"another reason", func(r *events.Recorder) { r.Warning(mount, "Refused", "The mount does not respond") }},
		{"another message", func(r *events.Recorder) { r.Warning(mount, "Unreachable", "The mount refused.") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := record(t, notFound)

				r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
				c.post(r.recorder)
				synctest.Wait()

				if got := r.stored.List(); len(got) != 2 || got[0].Count != 1 || got[1].Count != 1 {
					t.Errorf("the server holds %+v, want two Events that count 1 each", got)
				}
			})
		})
	}
}

// A repeat of an Event that the API server deleted after its TTL
// creates a new Event.
func TestARepeatAfterTheTTLCreatesANewEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
		synctest.Wait()
		r.stored.Expire()

		r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
		synctest.Wait()

		if got := r.stored.List(); len(got) != 1 || got[0].Count != 1 {
			t.Errorf("the server holds %+v, want one new Event that counts 1", got)
		}
	})
}

// A write that the API server refuses twice is sent a third time, so an
// API server restart loses no Event and no count.
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
					r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
					synctest.Wait()
				}
				r.stored.Refuse(2)

				r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
				time.Sleep(time.Minute)
				synctest.Wait()

				if got := r.stored.List(); len(got) != 1 || got[0].Count != c.count || r.log.String() != "" {
					t.Errorf("the server holds %+v and the log says %q, want one Event that counts %d and no log", got, r.log.String(), c.count)
				}
			})
		})
	}
}

// A write that fails on every attempt logs one line, and the next Event
// is still written.
func TestAWriteThatFailsEveryAttemptIsLoggedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		r.stored.Refuse(3)

		r.recorder.Warning(mount, "Unreachable", "The mount does not respond")
		r.recorder.Normal(mount, "Reachable", "The mount responds")
		time.Sleep(time.Minute)
		synctest.Wait()

		lines := strings.Split(strings.TrimSpace(r.log.String()), "\n")
		if len(lines) != 1 || !strings.HasPrefix(lines[0], "test-operator: writing the Warning Event Unreachable on the Mount observatory/east-mount: ") {
			t.Errorf("the log says %q, want one line about the Unreachable Event", lines)
		}
		if got := r.stored.List(); len(got) != 1 || got[0].Reason != "Reachable" {
			t.Errorf("the server holds %+v, want only the Reachable Event", got)
		}
	})
}

var node = events.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "node-1", UID: "uid-2"}

// The log line of a failed write about a cluster-scoped object has no
// namespace in it.
func TestAFailedWriteAboutAClusterScopedObjectNamesNoNamespace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		r.stored.Refuse(3)

		r.recorder.Warning(node, "Unreachable", "The node does not respond")
		time.Sleep(time.Minute)
		synctest.Wait()

		if log := r.log.String(); !strings.HasPrefix(log, "test-operator: writing the Warning Event Unreachable on the Node node-1: ") {
			t.Errorf("the log says %q, want one line about the Node node-1", log)
		}
	})
}

// An Event in a namespace that does not exist, or that the API server
// is deleting, is dropped immediately with no log line, and the next
// Event is still written.
func TestAnEventInANamespaceThatIsGoingIsDroppedQuietly(t *testing.T) {
	cases := []struct {
		name  string
		leave func(*eventstest.Events, string)
	}{
		{"terminating", (*eventstest.Events).Terminate},
		{"deleted", (*eventstest.Events).DeleteNamespace},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := record(t, notFound)
				c.leave(r.stored, "observatory")

				r.recorder.Warning(mount, "ParentMissing", "Missing Telescope east.")
				r.recorder.Normal(node, "Rebooted", "The node rebooted.")
				synctest.Wait()

				if log := r.log.String(); log != "" {
					t.Errorf("the log says %q, want nothing", log)
				}
				if got := r.stored.List(); len(got) != 1 || got[0].Reason != "Rebooted" {
					t.Errorf("the server holds %+v, want only the Rebooted Event", got)
				}
			})
		})
	}
}

// While the API server does not respond, the queue fills, and the
// recorder drops each further Event without waiting. It logs the first
// drop and counts every drop.
func TestAFullQueueDropsAndCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stalled := make(chan struct{})
		stored := &eventstest.Events{}
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-stalled
			stored.ServeHTTP(w, r)
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
			t.Errorf("the recorder dropped %d Events and logged %q, want 4 dropped and one log line", recorder.Dropped(), log.String())
		}
	})
}

// A nil recorder posts nothing, so a program that has no API server can
// still pass one.
func TestANilRecorderPostsNothing(t *testing.T) {
	var recorder *events.Recorder

	recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
	recorder.Warning(mount, "Unreachable", "The mount does not respond")

	if recorder.Dropped() != 0 {
		t.Errorf("a nil recorder reports %d dropped Events, want 0", recorder.Dropped())
	}
}

// The recorder cuts a reason to 128 bytes and a message to 1024 bytes,
// at a character boundary, the limits that the API server enforces on
// an Event with eventTime.
func TestALongReasonAndMessageAreCut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		reason := strings.Repeat("Moved", 30)
		message := strings.Repeat("é", 600)

		r.recorder.Normal(mount, reason, message)
		r.recorder.Normal(mount, "Short", "Short.")
		synctest.Wait()

		got := r.stored.List()
		if len(got) != 2 {
			t.Fatalf("the server holds %d Events, want 2", len(got))
		}
		if len(got[0].Reason) > 128 || !strings.HasPrefix(got[0].Reason, "MovedMoved") || !strings.HasSuffix(got[0].Reason, "…") {
			t.Errorf("the reason is %d bytes: %q, want at most 128 bytes that start with MovedMoved and end in …", len(got[0].Reason), got[0].Reason)
		}
		if len(got[0].Message) > 1024 || !strings.HasPrefix(got[0].Message, "éé") || !strings.HasSuffix(got[0].Message, "é…") {
			t.Errorf("the message is %d bytes: %q, want at most 1024 bytes that start with éé and end in é…", len(got[0].Message), got[0].Message)
		}
		if got[1].Reason != "Short" || got[1].Message != "Short." {
			t.Errorf("a short Event became %q: %q, want Short: Short.", got[1].Reason, got[1].Message)
		}
	})
}

// The recorder remembers at most 4096 series. A repeat of a series that
// it forgot creates a new Event instead of patching one.
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

		got := r.stored.List()
		if len(got) != 4098 || got[1].Count != 2 || got[4097].Message != "Moved to step 0" || got[4097].Count != 1 {
			t.Errorf("the server holds %d Events; step 1 counts %d; the last is %q and counts %d; want 4098, 2, step 0, and 1",
				len(got), got[1].Count, got[len(got)-1].Message, got[len(got)-1].Count)
		}
	})
}

// A recorder whose context ends during the retry wait stops at once and
// logs nothing, because the program is exiting.
func TestARecorderStopsDuringTheRetryWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stored := &eventstest.Events{}
		stored.Refuse(1)
		server := apiservertest.Start(t, stored.Around(notFound))
		ctx, cancel := context.WithCancel(t.Context())
		log := &syncBuffer{}
		recorder := events.New(ctx, apiclient.New(apiservertest.Host, server.Client(), ""), "test-operator", events.Options{Log: log})

		recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()
		cancel()
		synctest.Wait()

		if got := stored.List(); len(got) != 0 || log.String() != "" {
			t.Errorf("the server holds %+v and the log is %q, want nothing", got, log.String())
		}
	})
}

// With no instance set, the recorder uses the host name, which is the
// pod's name inside a pod.
func TestTheInstanceIsTheHostNameByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stored := &eventstest.Events{}
		server := apiservertest.Start(t, stored.Around(notFound))
		recorder := events.New(t.Context(), apiclient.New(apiservertest.Host, server.Client(), ""), "test-operator", events.Options{})
		host, _ := os.Hostname()

		recorder.Normal(mount, "PodCreated", "Created pod east-mount.")
		synctest.Wait()

		if got := stored.List(); len(got) != 1 || got[0].ReportingInstance != host || got[0].Source.Host != host {
			t.Errorf("the server holds %+v, want one Event from %q", got, host)
		}
	})
}
