package main

// These tests run the operator's whole process, from the election to
// the release, against the fake API server, the picture server, and
// the fake Lease server of electiontest. Each one runs in a synctest
// bubble, so the election's waits take no real time.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
)

// anotherCopy is the identity of a copy of the operator in another pod.
const anotherCopy = "people-operator-1_0a0b0c0d"

// processWorld is one process of the operator and the servers it
// reaches.
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

// start runs the process of the operator in the pod, until shutdown
// or the end of the test.
func (w *processWorld) start(t *testing.T, pod string) {
	watcher, err := dynamic.NewForConfig(w.api.server.Config())
	if err != nil {
		t.Fatal(err)
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

// A copy that waits for the Lease writes nothing, and a stop while it
// waits ends it cleanly with the Lease untouched.
func TestAWaitingCopyDoesNotAct(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.lease.HoldAs(anotherCopy)
		w.api.putPerson(ada(""))
		w.start(t, operatorPodName)
		time.Sleep(20 * time.Second)
		synctest.Wait()

		if err := w.shutdown(); err != nil {
			t.Errorf("the waiting copy answered %v, want nil", err)
		}
		if got := w.api.person(t, "ada").Status.Thumbnail; got != "" {
			t.Error("the waiting copy wrote a status")
		}
		if got := w.lease.Holder(); got != anotherCopy {
			t.Errorf("the Lease names %q, want %q", got, anotherCopy)
		}
	})
}

// A copy that waits takes the Lease when its holder stops renewing,
// and then acts.
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
			t.Error("the copy that took the Lease wrote no status")
		}
	})
}

// A stop during a status write waits for the write to land, and only
// then releases the Lease.
func TestTheLeaseIsReleasedAfterTheLastWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.start(t, operatorPodName)
		time.Sleep(time.Second)
		synctest.Wait()
		landed := make(chan bool, 1)
		w.api.slowStatusWrites(5*time.Second, func() { landed <- w.heldByThisProcess() })
		w.api.putPerson(ada(""))
		time.Sleep(time.Second)

		if err := w.shutdown(); err != nil {
			t.Errorf("the process answered %v, want nil", err)
		}
		if held := <-landed; !held {
			t.Error("the status write landed after the release")
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease names %q after the shutdown, want no holder", got)
		}
		if !isInitials(w.api.person(t, "ada").Status.Thumbnail) {
			t.Error("the status write did not land")
		}
	})
}

// A fetch that the stop cuts off writes no FetchFailed, and the stop
// ends the pass before the next Person.
func TestAFetchThatTheStopCutsOffWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.pictures.serve("/ada.png", servedPicture{hold: true})
		w.pictures.serve("/grace.png", servedPicture{hold: true})
		w.api.putPerson(ada("https://pictures.example/ada.png"))
		grace := ada("https://pictures.example/grace.png")
		grace.Metadata.Name = "grace"
		w.api.putPerson(grace)
		w.start(t, operatorPodName)
		time.Sleep(time.Second)
		synctest.Wait()

		if err := w.shutdown(); err != nil {
			t.Errorf("the process answered %v, want nil", err)
		}
		for _, name := range []string{"ada", "grace"} {
			if got := w.api.person(t, name).Status.Conditions; len(got) != 0 {
				t.Errorf("the Person %s has the conditions %+v, want none", name, got)
			}
		}
		if got := len(w.pictures.requests()); got != 1 {
			t.Errorf("the picture server took %d requests, want 1", got)
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease names %q after the shutdown, want no holder", got)
		}
	})
}

// A copy that cannot read its own pod has written nothing, so it
// releases the Lease before it fails.
func TestACopyThatCannotReadItsPodReleasesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.start(t, "people-operator-gone")
		<-w.done

		if w.err == nil {
			t.Error("the process answered nil, want the failed read of its pod")
		}
		if got := w.lease.Count(http.MethodPost); got != 1 {
			t.Errorf("the process created the Lease %d times, want 1", got)
		}
		if got := w.lease.Holder(); got != "" {
			t.Errorf("the Lease names %q, want no holder", got)
		}
	})
}

// A process with no pod name has no identity to hold the Lease under.
func TestAProcessWithNoPodDoesNotStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newProcessWorld(t)
		w.start(t, "")
		<-w.done

		if w.err == nil {
			t.Error("the process answered nil, want the election's refusal")
		}
	})
}
