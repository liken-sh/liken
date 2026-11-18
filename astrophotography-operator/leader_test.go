package main

// These tests run the whole operator process, from the election to the
// release of the Lease, against the fake API server and electiontest's
// Lease server.

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
)

// anotherPod is the Lease holder identity of another operator pod.
const anotherPod = "astrophotography-operator-1_0a0b0c0d"

// processWorld is one operator process and the fake servers it
// connects to.
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

func (w *processWorld) start(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	w.stop, w.done = cancel, make(chan struct{})
	p := process{
		client:    apiclient.New(apiservertest.Host, w.api.server.Client(), "").WithWaitContext(ctx),
		watcher:   w.api.watcher(t),
		lease:     w.lease.Config(t),
		pod:       operatorPodName,
		namespace: operatorNS,
		report:    func(line string) { t.Log(line) },
		logs:      io.Discard,
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

// shutdown cancels the process's context, as SIGTERM from the kubelet
// does, and returns the error that operate returned.
func (w *processWorld) shutdown() error {
	w.stop()
	<-w.done
	return w.err
}

func (w *processWorld) heldByThisProcess() bool {
	return strings.HasPrefix(w.lease.Holder(), operatorPodName+"_")
}

// A copy that waits for the Lease sends no request to the API server.
// When stop ends while it waits, operate returns nil and leaves the
// Lease to its holder.
func TestAWaitingCopyDoesNotAct(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherPod)
		w.start(t)
		time.Sleep(20 * time.Second)
		synctest.Wait()

		if err := w.shutdown(); err != nil {
			t.Errorf("the waiting process returned %v, want nil", err)
		}
		if count := w.api.requestCount(); count != 0 {
			t.Errorf("the waiting process sent %d requests, want none", count)
		}
		if got := w.lease.Holder(); got != anotherPod {
			t.Errorf("the Lease names %q, want %q", got, anotherPod)
		}
	})
}

func TestAWaitingCopyActsWhenTheLeaseExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherPod)
		w.start(t)
		time.Sleep(time.Minute)
		synctest.Wait()

		if !w.heldByThisProcess() {
			t.Errorf("the Lease names %q, want this process", w.lease.Holder())
		}
		if len(w.api.requested(targetsPath)) == 0 {
			t.Error("the process that took the Lease opened no watch of the Targets, want one")
		}
	})
}

// A shutdown releases the Lease, so a waiting process does not wait for it
// to expire.
func TestAShutdownReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.start(t)
		time.Sleep(time.Second)
		synctest.Wait()
		if !w.heldByThisProcess() {
			t.Fatalf("the Lease names %q, want this process", w.lease.Holder())
		}

		if err := w.shutdown(); err != nil {
			t.Errorf("the process returned %v, want nil", err)
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease names %q after the shutdown, want no holder", got)
		}
	})
}
