package main

// The world a test runs the operator in: the fake API server, the fake
// INDI servers, and the operator's loop, all in one synctest bubble, so
// a wait of minutes takes no real time.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type world struct {
	t    *testing.T
	api  *fakeAPI
	indi *indiWorld
	stop func()
	done chan struct{}
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
	w := &world{t: t, api: api, indi: startIndiWorld(t, api)}
	w.start()
	t.Cleanup(w.halt)
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
	o := newOperator(testNamespace, client.WithContext(ctx), w.indi)
	w.stop, w.done = cancel, make(chan struct{})
	done := w.done
	go func() {
		defer close(done)
		o.run(ctx, func(ctx context.Context) *stores { return startWatches(ctx, watcher, testNamespace, o.changed) })
	}()
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

// until advances the fake clock in small steps until check holds, and
// fails the test after limit.
func (w *world) until(limit time.Duration, what string, check func() bool) {
	w.t.Helper()
	for waited := time.Duration(0); waited < limit; waited += 100 * time.Millisecond {
		synctest.Wait()
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	synctest.Wait()
	if !check() {
		w.t.Fatalf("after %v: %s", limit, what)
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
