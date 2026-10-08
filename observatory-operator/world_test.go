package main

// The world a test runs the operator in: the fake API server, the fake
// INDI servers, and the operator's loop, all in one synctest bubble, so
// a wait of minutes takes no real time.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type world struct {
	t    *testing.T
	api  *fakeAPI
	indi *indiWorld
	// guiders runs a fake PHD2 in each guider pod.
	guiders *guiderWorld
	// n is the copy of the operator's process that runs now.
	n    *namespaces
	stop func()
	done chan struct{}
	// logs holds what every copy of the operator logged.
	logs *logBuffer
}

// logBuffer collects the operator's log. The operator's goroutines
// write to it at once, so a lock guards it.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// example answers the resources of examples/simulators.yaml, by kind,
// with the Reservation left out: each test makes its own.
func example(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile("examples/simulators.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, document := range bytes.Split(raw, []byte("\n---\n")) {
		body, err := yaml.YAMLToJSON(document)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(body, &object); err != nil {
			t.Fatal(err)
		}
		if object == nil || object["kind"] == observatory.ReservationKind.Name {
			continue
		}
		out = append(out, object)
	}
	return out
}

func kindNamed(name string) observatory.Kind {
	for _, kind := range observatory.Kinds {
		if kind.Name == name {
			return kind
		}
	}
	panic("no kind " + name)
}

// startWorld runs the operator on the example's resources until the
// test ends. It runs in a synctest bubble.
func startWorld(t *testing.T) *world {
	api := startFakeAPI(t)
	for _, object := range example(t) {
		api.put(kindCollection(kindNamed(object["kind"].(string))), object)
	}
	w := &world{t: t, api: api, indi: startIndiWorld(t, api), guiders: startGuiderWorld(t.Context(), api), logs: &logBuffer{}}
	w.start()
	t.Cleanup(w.halt)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the operator's log:\n%s", w.logs.String())
		}
	})
	return w
}

// start runs a new copy of the operator.
func (w *world) start() {
	client := apiclient.New(apiservertest.Host, w.api.server.Client(), "")
	watcher, err := dynamic.NewForConfig(w.api.server.Config())
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(w.t.Context())
	n := newNamespaces(client.WithContext(ctx), dialers{w.indi, w.guiders})
	n.logs = w.logs
	n.recorder = events.New(ctx, client, managedBy, events.Options{Instance: "observatory-operator-0", Log: w.logs})
	w.n = n
	w.stop, w.done = cancel, make(chan struct{})
	done := w.done
	go func() {
		defer close(done)
		n.run(ctx, func(ctx context.Context, changed *bell) *stores { return startWatches(ctx, watcher, changed) })
	}()
}

// operator answers the operator of the test's namespace, once it runs.
func (w *world) operator() *operator {
	w.t.Helper()
	w.until(time.Minute, "the operator of "+testNamespace+" does not run", func() bool {
		return w.n.operatorOf(testNamespace) != nil
	})
	return w.n.operatorOf(testNamespace)
}

// ring answers the bell that wakes until: the operator's of the test's
// namespace, which every watch event and every INDI event rings, or
// the process's, which every watch event rings, before that operator
// runs.
func (w *world) ring() <-chan struct{} {
	if o := w.n.operatorOf(testNamespace); o != nil {
		return o.changed.wait()
	}
	return w.n.changed.wait()
}

// halt stops the operator and waits until it has stopped, as a
// SIGTERM does.
func (w *world) halt() {
	if w.stop == nil {
		return
	}
	w.stop()
	<-w.done
	w.stop = nil
}

// restart stops the operator and runs a new copy, which reads only
// what the API server holds.
func (w *world) restart() {
	w.halt()
	w.start()
}

// reserve creates a Reservation of the east telescope.
func (w *world) reserve(name string, spec map[string]any) {
	w.api.put(kindCollection(observatory.ReservationKind), map[string]any{
		"apiVersion": observatory.APIVersion, "kind": observatory.ReservationKind.Name,
		"metadata": map[string]any{"name": name},
		"spec":     spec,
	})
}

func (w *world) reservation(name string) (observatory.Reservation, bool) {
	return decode[observatory.Reservation](w.t, w.api, kindCollection(observatory.ReservationKind), name)
}

// until waits until check holds, and fails the test after limit on the
// fake clock. It checks again after each change of the fake API server
// and each ring of the operator's bell, which every watch event and
// every INDI event rings, so it observes each state the operator
// passes through, and the clock moves only when the bubble waits for a
// timer.
func (w *world) until(limit time.Duration, what string, check func() bool) {
	w.t.Helper()
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	for {
		ring := w.ring()
		w.api.mu.Lock()
		changed := w.api.changed
		w.api.mu.Unlock()
		synctest.Wait()
		if check() {
			return
		}
		select {
		case <-ring:
		case <-changed:
		case <-deadline.C:
			synctest.Wait()
			if !check() {
				w.t.Fatalf("after %v: %s", limit, what)
			}
			return
		}
	}
}

// phase waits until a reservation reaches a phase.
func (w *world) phase(name string, phase observatory.ReservationPhase, limit time.Duration) observatory.Reservation {
	w.t.Helper()
	var r observatory.Reservation
	defer func() {
		if w.t.Failed() {
			w.t.Logf("the Reservation %s: %s", name, mustJSON(r.Status))
		}
	}()
	w.until(limit, "the Reservation "+name+" is not "+string(phase), func() bool {
		var ok bool
		r, ok = w.reservation(name)
		return ok && r.Status.Phase == phase
	})
	return r
}
