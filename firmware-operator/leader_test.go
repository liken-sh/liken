package main

// These tests run the whole operator process, from the election to the
// Lease release, against the fake API server and the fake Lease server
// of electiontest. Each test runs in a synctest bubble, so the
// election's waits take no real time.

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
)

// anotherCopy is the Lease identity of an operator copy in another pod.
const anotherCopy = "firmware-operator-1_0a0b0c0d"

// processWorld is one operator process and the fake servers it talks
// to.
type processWorld struct {
	api   *fakeAPI
	lease *electiontest.Server

	stop context.CancelFunc
	done chan struct{}
	err  error
}

func newProcessWorld(t *testing.T) *processWorld {
	return &processWorld{api: startFakeAPI(t), lease: electiontest.New(leaseName, operatorNS)}
}

// start runs the operator process as the named pod, until shutdown or
// the end of the test.
func (w *processWorld) start(t *testing.T, pod string) {
	watcher, err := dynamic.NewForConfig(w.api.server.Config())
	if err != nil {
		t.Fatalf("creating the dynamic client: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	w.stop, w.done = cancel, make(chan struct{})
	p := process{
		client:    apiclient.New(apiservertest.Host, w.api.server.Client(), "").WithWaitContext(ctx),
		watcher:   watcher,
		lease:     w.lease.Config(t),
		pod:       pod,
		namespace: operatorNS,
		report:    func(line string) { t.Log(line) },
		eventLog:  io.Discard,
		exit:      func(code int) { t.Errorf("the process exited with code %d", code) },
	}
	go func() {
		defer close(w.done)
		w.err = p.operate(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-w.done
	})
}

// shutdown stops the process as the kubelet's SIGTERM does, and returns
// the error that the process returned.
func (w *processWorld) shutdown() error {
	w.stop()
	<-w.done
	return w.err
}

// A copy that is waiting for the Lease starts no upload. A stop during
// the wait ends the copy without changing the Lease.
func TestAWaitingCopyDoesNotAct(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherCopy)
		w.api.putFirmware(qhy5lii())
		w.api.publish("liken-1", bareChip("1-4.1.2", 12))
		w.start(t, operatorPodName)
		time.Sleep(20 * time.Second)
		synctest.Wait()

		if err := w.shutdown(); err != nil {
			t.Errorf("the waiting copy returned %v, want nil", err)
		}
		if got := w.api.created(operatorPodsPath); got != 0 {
			t.Errorf("the waiting copy created %d upload pods", got)
		}
		if got := w.lease.Holder(); got != anotherCopy {
			t.Errorf("the Lease holder is %q, want %q", got, anotherCopy)
		}
	})
}

// A waiting copy takes the Lease when the holder stops renewing it,
// starts the upload, and releases the Lease when it stops.
func TestAWaitingCopyActsWhenTheLeaseExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherCopy)
		w.api.putFirmware(qhy5lii())
		w.api.publish("liken-1", bareChip("1-4.1.2", 12))
		w.start(t, operatorPodName)
		time.Sleep(time.Minute)
		synctest.Wait()

		if holder := w.lease.Holder(); !strings.HasPrefix(holder, operatorPodName+"_") {
			t.Errorf("the Lease holder is %q, want this process", holder)
		}
		if got := w.api.created(operatorPodsPath); got != 1 {
			t.Errorf("the copy that took the Lease created %d upload pods, want 1", got)
		}
		if err := w.shutdown(); err != nil {
			t.Errorf("the process returned %v, want nil", err)
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease holder is %q after the shutdown, want no holder", got)
		}
	})
}

// A process with no pod name has no identity to hold the Lease with.
func TestAProcessWithNoPodDoesNotStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.start(t, "")
		<-w.done

		if w.err == nil {
			t.Error("the process returned nil, want the election's refusal")
		}
	})
}
