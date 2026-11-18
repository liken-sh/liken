package main

// These tests run a whole copy of the operator, from the election to
// the release of the Lease, against the fake API server, the picture
// server, and the fake Lease server from electiontest. Each test runs
// in a synctest bubble, so the election's waits use the fake clock.

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
)

// anotherCopy is the Lease identity of a copy running in another pod.
const anotherCopy = "people-operator-1_0a0b0c0d"

// processWorld is one copy of the operator and the servers it talks to.
type processWorld struct {
	api      *fakeAPI
	pictures *pictureServer
	lease    *electiontest.Server

	stop context.CancelFunc
	done chan struct{}
	err  error
}

func newProcessWorld(t *testing.T) *processWorld {
	return &processWorld{api: startFakeAPI(t), pictures: startPictureServer(t), lease: electiontest.New(leaseName, operatorNS)}
}

// start runs the operator as the named pod, until shutdown or the end
// of the test.
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
		transport: w.pictures.server,
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

// shutdown stops the operator the way the kubelet's SIGTERM does, and
// returns what operate returned.
func (w *processWorld) shutdown() error {
	w.stop()
	<-w.done
	return w.err
}

func (w *processWorld) heldByThisProcess() bool {
	return strings.HasPrefix(w.lease.Holder(), operatorPodName+"_")
}

// A copy that is waiting for the Lease writes nothing. Stopping it
// while it waits ends it without an error and leaves the Lease alone.
func TestAWaitingCopyDoesNotAct(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherCopy)
		w.api.putPerson(ada(""))
		w.start(t, operatorPodName)
		time.Sleep(20 * time.Second)
		synctest.Wait()

		if err := w.shutdown(); err != nil {
			t.Errorf("the waiting copy returned %v, want nil", err)
		}
		if got := w.api.person(t, "ada").Status.Thumbnail; got != "" {
			t.Error("the waiting copy wrote a status, want none")
		}
		if got := w.lease.Holder(); got != anotherCopy {
			t.Errorf("the Lease names %q, want %q", got, anotherCopy)
		}
	})
}

// A copy that is waiting takes the Lease when the holder stops
// renewing it, and then starts acting.
func TestAWaitingCopyActsWhenTheLeaseExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherCopy)
		w.api.putPerson(ada(""))
		w.start(t, operatorPodName)
		time.Sleep(time.Minute)
		synctest.Wait()

		if !w.heldByThisProcess() {
			t.Errorf("the Lease names %q, want this process", w.lease.Holder())
		}
		if !isInitials(w.api.person(t, "ada").Status.Thumbnail) {
			t.Error("the copy that acquired the Lease wrote no status, want the initials thumbnail")
		}
	})
}

// A stop during a status write waits for the write and the Events it
// posts to finish, and only then releases the Lease.
func TestTheLeaseIsReleasedAfterTheLastWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.start(t, operatorPodName)
		time.Sleep(time.Second)
		synctest.Wait()
		heldAtStatusWrite := make(chan bool, 1)
		w.api.slowStatusWrites(5*time.Second, func() { heldAtStatusWrite <- w.heldByThisProcess() })
		var mu sync.Mutex
		var heldAtEventWrites []bool
		w.api.slowEventWrites(2*time.Second, func() {
			mu.Lock()
			defer mu.Unlock()
			heldAtEventWrites = append(heldAtEventWrites, w.heldByThisProcess())
		})
		w.api.putPerson(ada(""))
		time.Sleep(time.Second)

		if err := w.shutdown(); err != nil {
			t.Errorf("the process returned %v, want nil", err)
		}
		if held := <-heldAtStatusWrite; !held {
			t.Error("the status write happened after the Lease was released, want it before")
		}
		mu.Lock()
		defer mu.Unlock()
		if len(heldAtEventWrites) == 0 || slices.Contains(heldAtEventWrites, false) {
			t.Errorf("the process held the Lease at each Event write: %v, want at least one write, each while it held the Lease", heldAtEventWrites)
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease names %q after the shutdown, want no holder", got)
		}
		if !isInitials(w.api.person(t, "ada").Status.Thumbnail) {
			t.Error("the status write did not happen, want the initials thumbnail")
		}
	})
}

// A fetch that the stop interrupts does not write FetchFailed, and the
// stop ends the pass before it starts the next Person.
func TestAFetchThatTheStopCutsOffWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.pictures.serve("/ada.png", servedPicture{neverResponds: true})
		w.pictures.serve("/grace.png", servedPicture{neverResponds: true})
		w.api.putPerson(ada("https://pictures.example/ada.png"))
		grace := ada("https://pictures.example/grace.png")
		grace.Metadata.Name = "grace"
		w.api.putPerson(grace)
		w.start(t, operatorPodName)
		time.Sleep(time.Second)
		synctest.Wait()

		if err := w.shutdown(); err != nil {
			t.Errorf("the process returned %v, want nil", err)
		}
		for _, name := range []string{"ada", "grace"} {
			if got := w.api.person(t, name).Status.Conditions; len(got) != 0 {
				t.Errorf("the Person %s has the conditions %+v, want none", name, got)
			}
		}
		if got := len(w.pictures.requests()); got != 1 {
			t.Errorf("the picture server received %d requests, want 1", got)
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease names %q after the shutdown, want no holder", got)
		}
	})
}

// A copy that cannot read its own pod has written nothing yet, so it
// releases the Lease before it returns the error.
func TestACopyThatCannotReadItsPodReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.start(t, "people-operator-gone")
		<-w.done

		if w.err == nil {
			t.Error("the process returned nil, want the error from reading its pod")
		}
		if got := w.lease.Count(http.MethodPost); got != 1 {
			t.Errorf("the process created the Lease %d times, want 1", got)
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease names %q, want no holder", got)
		}
	})
}

// A copy with no pod name has no identity to put in the Lease, so the
// election refuses to start.
func TestAProcessWithNoPodDoesNotStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.start(t, "")
		<-w.done

		if w.err == nil {
			t.Error("the process returned nil, want the election's error")
		}
	})
}
