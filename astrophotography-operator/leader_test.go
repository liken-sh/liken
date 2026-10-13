package main

// These tests run the operator's whole process, from the election to
// the release, against the fake API server and the fake Lease server of
// electiontest. Each one runs in a synctest bubble, so the election's
// waits take no real time.

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

// anotherCopy is the identity of a copy of the operator in another pod.
const anotherCopy = "astrophotography-operator-1_0a0b0c0d"

// processWorld is one process of the operator and the servers it
// reaches.
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

// start runs the process of the operator, until shutdown or the end of
// the test.
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

// shutdown is the kubelet's SIGTERM. It answers what the process
// returned.
func (w *processWorld) shutdown() error {
	w.stop()
	<-w.done
	return w.err
}

func (w *processWorld) heldByThisProcess() bool {
	return strings.HasPrefix(w.lease.Holder(), operatorPodName+"_")
}

// A copy that waits for the Lease opens no watch, and a stop while it
// waits ends it cleanly with the Lease untouched.
func TestAWaitingCopyDoesNotAct(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherCopy)
		w.start(t)
		time.Sleep(20 * time.Second)
		synctest.Wait()

		if err := w.shutdown(); err != nil {
			t.Errorf("the waiting copy answered %v, want nil", err)
		}
		if count := w.api.requestCount(); count != 0 {
			t.Errorf("the waiting copy sent %d requests, want none", count)
		}
		if got := w.lease.Holder(); got != anotherCopy {
			t.Errorf("the Lease names %q, want %q", got, anotherCopy)
		}
	})
}

// A copy that waits takes the Lease when its holder stops renewing, and
// then opens its watches.
func TestAWaitingCopyActsWhenTheLeaseExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherCopy)
		w.start(t)
		time.Sleep(time.Minute)
		synctest.Wait()

		if !w.heldByThisProcess() {
			t.Errorf("the Lease names %q, want this process", w.lease.Holder())
		}
		if len(w.api.requested(targetsPath)) == 0 {
			t.Error("the copy that took the Lease opened no watch of the Targets")
		}
	})
}

// A shutdown stops every watch and releases the Lease, so a waiting
// copy takes it at once instead of after it expires.
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
			t.Errorf("the process answered %v, want nil", err)
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease names %q after the shutdown, want no holder", got)
		}
	})
}
