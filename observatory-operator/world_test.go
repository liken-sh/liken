package main

// The fixture that runs the operator for a test: the fake API server,
// the fake INDI servers, and the operator's loop, all in one synctest
// bubble, so a wait of minutes takes no real time.

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
	// n is the copy of the operator process that runs now.
	n    *namespaces
	stop func()
	done chan struct{}
	// logs holds what every copy of the operator logged.
	logs *logBuffer
}

// logBuffer collects the operator's log. Several goroutines write to
// it at once.
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

// example returns the resources of examples/simulators.yaml except the
// Reservation, because each test creates its own.
func example(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile("examples/simulators.yaml")
	if err != nil {
		t.Fatalf("reading examples/simulators.yaml: %v", err)
	}
	var out []map[string]any
	for _, document := range bytes.Split(raw, []byte("\n---\n")) {
		body, err := yaml.YAMLToJSON(document)
		if err != nil {
			t.Fatalf("converting a document of examples/simulators.yaml to JSON: %v", err)
		}
		var object map[string]any
		if err := json.Unmarshal(body, &object); err != nil {
			t.Fatalf("decoding a document of examples/simulators.yaml: %v", err)
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
// test ends. The caller must be inside a synctest bubble.
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

// start starts the operator.
func (w *world) start() {
	client := apiclient.New(apiservertest.Host, w.api.server.Client(), "")
	watcher, err := dynamic.NewForConfig(w.api.server.Config())
	if err != nil {
		w.t.Fatalf("creating the dynamic client: %v", err)
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
		n.run(ctx, func(ctx context.Context, changed *changeNotifier) *stores {
			return startWatches(ctx, watcher, changed, n.metrics)
		})
	}()
}

// operator returns the operator of the test's namespace, waiting until
// it runs.
func (w *world) operator() *operator {
	w.t.Helper()
	w.until(time.Minute, "the operator of "+testNamespace+" does not run", func() bool {
		return w.n.operatorOf(testNamespace) != nil
	})
	return w.n.operatorOf(testNamespace)
}

// nextChange returns a channel that closes at the next change the
// operator is notified of. Once the operator of the test's namespace
// runs, the channel is that operator's changed notifier, which every
// watch event and every INDI event notifies. Before then it is the
// process's notifier, which only watch events notify.
func (w *world) nextChange() <-chan struct{} {
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

// restart stops the operator and starts it again. The restarted
// operator has only the state that the API server stores.
func (w *world) restart() {
	w.halt()
	w.start()
}

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

// until waits until check returns true, and fails the test after limit
// on the fake clock. It checks again after each change in the fake API
// server and each notification of the operator's changed notifier, so it sees each
// state that the operator passes through.
func (w *world) until(limit time.Duration, what string, check func() bool) {
	w.t.Helper()
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	for {
		next := w.nextChange()
		w.api.mu.Lock()
		changed := w.api.changed
		w.api.mu.Unlock()
		synctest.Wait()
		if check() {
			return
		}
		select {
		case <-next:
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

// phase waits until the reservation reaches the phase, and returns it.
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
