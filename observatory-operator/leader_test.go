package main

// Tests of the election in front of the operator: a copy that waits for
// the Lease sends nothing to the API server, and a copy that leads
// releases the Lease only after its last write.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const testPod = "observatory-operator-7d9f-x2k4q"

// candidate is one copy of the operator process. It competes for the
// Lease and runs the operator of every namespace while it leads, as
// operate does.
type candidate struct {
	api *fakeAPI
	// acting closes when the copy starts to act, and done closes when
	// actWhileLeading returns. n is the operator of every namespace. It
	// is set before acting closes.
	acting chan struct{}
	n      *namespaces
	done   chan struct{}
	stop   context.CancelFunc
}

// startCandidate runs a copy of the operator behind the election, on
// the example's resources and a Reservation of the east Telescope. Call
// it inside a synctest bubble.
func startCandidate(t *testing.T, leases *electiontest.Server) *candidate {
	api := startFakeAPI(t)
	for _, object := range example(t) {
		api.put(kindCollection(kindNamed(object["kind"].(string))), object)
	}
	api.put(kindCollection(observatory.ReservationKind), map[string]any{
		"apiVersion": observatory.APIVersion, "kind": observatory.ReservationKind.Name,
		"metadata": map[string]any{"name": "east-tonight"},
		"spec":     map[string]any{"telescope": "east", "holder": "desktop"},
	})
	dialer := dialers{startIndiWorld(t, api), startGuiderWorld(t.Context(), api)}
	client := apiclient.New(apiservertest.Host, api.server.Client(), "")
	watcher, err := dynamic.NewForConfig(api.server.Config())
	if err != nil {
		t.Fatalf("creating the dynamic client: %v", err)
	}
	logs := &logBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the operator's log:\n%s", logs.String())
		}
	})
	options := election.Options{
		Name: leaseName, Namespace: "liken-system", Pod: testPod,
		Report: func(line string) { _, _ = logs.Write([]byte(line + "\n")) },
		Exit:   func(code int) { t.Errorf("the election ended the process with code %d, want it to keep running", code) },
	}
	ctx, stop := context.WithCancel(t.Context())
	c := &candidate{api: api, acting: make(chan struct{}), done: make(chan struct{}), stop: stop}
	go func() {
		defer close(c.done)
		err := actWhileLeading(ctx, leases.Config(t), options, func(mayWrite func() error) error {
			c.n = leadingNamespaces(client, mayWrite, dialer, logs, testPod)
			close(c.acting)
			c.n.run(ctx, func(ctx context.Context, changed *changeNotifier) *stores {
				return startWatches(ctx, watcher, changed, c.n.metrics)
			})
			return c.n.finish()
		})
		if err != nil {
			t.Errorf("actWhileLeading returned %v, want nil", err)
		}
	}()
	t.Cleanup(func() {
		stop()
		<-c.done
	})
	return c
}

// halt stops the copy, as a SIGTERM does, and waits for it to return.
func (c *candidate) halt() {
	c.stop()
	<-c.done
}

// started reports whether the copy has started to act.
func (c *candidate) started() bool {
	select {
	case <-c.acting:
		return true
	default:
		return false
	}
}

// A copy that finds the Lease held by another pod waits and sends
// nothing to the API server, so it creates no pod and writes no
// status. It starts to act when the holder's Lease expires.
func TestAWaitingCopyDoesNotAct(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		leases := electiontest.New(leaseName, "liken-system")
		leases.HoldAs("observatory-operator-5c4b-old_00000000")
		c := startCandidate(t, leases)
		var sent []string
		var mu sync.Mutex
		c.api.onEachRequest(func(r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, r.Method+" "+r.URL.Path)
		})

		time.Sleep(election.Duration - time.Second)
		synctest.Wait()

		mu.Lock()
		waited := len(sent)
		mu.Unlock()
		if c.started() || waited != 0 || len(c.api.names("/api/v1/namespaces/"+testNamespace+"/pods")) != 0 {
			t.Errorf("acting = %v after %d requests, want a copy that waits and sends nothing", c.started(), waited)
		}

		time.Sleep(election.Duration + 2*election.RetryPeriod)
		synctest.Wait()

		if !c.started() || !strings.HasPrefix(leases.Holder(), testPod+"_") {
			t.Errorf("acting = %v with the Lease held by %q, want this copy to lead once the Lease expires", c.started(), leases.Holder())
		}
	})
}

// A copy that leads writes only while the Lease names it as the
// holder. A shutdown in the middle of a Reservation's activation
// releases the Lease after the last write of every runner and of the
// status writer.
func TestTheLeaseIsReleasedAfterTheLastWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		leases := electiontest.New(leaseName, "liken-system")
		c := startCandidate(t, leases)
		var holders []string
		var mu sync.Mutex
		c.api.onEachRequest(func(r *http.Request) {
			if r.Method == http.MethodGet {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			holders = append(holders, leases.Holder())
		})
		pods := "/api/v1/namespaces/" + testNamespace + "/pods"
		deadline := time.Now().Add(10 * time.Minute)
		for len(c.api.names(pods)) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Second)
		}

		c.halt()
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		if len(holders) == 0 {
			t.Fatal("the operator wrote nothing before the shutdown, want at least one write")
		}
		for i, holder := range holders {
			if !strings.HasPrefix(holder, testPod+"_") {
				t.Errorf("write %d of %d was sent with the Lease held by %q, want this copy", i+1, len(holders), holder)
			}
		}
		if leases.Holder() != "" {
			t.Errorf("the Lease holder is %q after the shutdown, want none", leases.Holder())
		}
	})
}

// An Event that the recorder is writing when the shutdown starts is
// written while the Lease still names this copy as the holder, because
// the release waits for it.
func TestTheLeaseIsReleasedAfterTheRecordersLastWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		leases := electiontest.New(leaseName, "liken-system")
		c := startCandidate(t, leases)
		<-c.acting
		synctest.Wait()
		through := make(chan struct{})
		var landed []string
		var mu sync.Mutex
		c.api.onEachRequest(func(r *http.Request) {
			if !strings.Contains(r.URL.Path, "/events") {
				return
			}
			<-through
			mu.Lock()
			defer mu.Unlock()
			landed = append(landed, leases.Holder())
		})
		c.n.recorder.Normal(events.ObjectReference{Kind: "Telescope", Namespace: testNamespace, Name: "east"},
			"Activating", "Starting the equipment.")
		synctest.Wait()

		go c.halt()
		synctest.Wait()
		holderWhileWriting := leases.Holder()
		close(through)
		<-c.done

		if !strings.HasPrefix(holderWhileWriting, testPod+"_") {
			t.Errorf("the Lease holder is %q while an Event is being written, want this copy", holderWhileWriting)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(landed) == 0 {
			t.Fatal("no Event reached the API server, want at least one")
		}
		for i, holder := range landed {
			if !strings.HasPrefix(holder, testPod+"_") {
				t.Errorf("Event %d of %d reached the API server with the Lease held by %q, want this copy", i+1, len(landed), holder)
			}
		}
		if leases.Holder() != "" {
			t.Errorf("the Lease holder is %q after the shutdown, want none", leases.Holder())
		}
	})
}

// A copy whose election options have no pod name gets an error from
// actWhileLeading, and act never runs.
func TestACopyWithNoPodNameDoesNotAct(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		leases := electiontest.New(leaseName, "liken-system")
		acted := false

		err := actWhileLeading(t.Context(), leases.Config(t), election.Options{Name: leaseName, Namespace: "liken-system"},
			func(func() error) error {
				acted = true
				return nil
			})

		if err == nil || acted {
			t.Errorf("actWhileLeading = %v and acted = %v, want an error and no act", err, acted)
		}
	})
}

// A copy that stops while it waits returns no error, never acts, and
// does not change the Lease of the holder.
func TestACopyThatStopsWhileItWaitsLeavesTheLeaseAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		leases := electiontest.New(leaseName, "liken-system")
		holder := "observatory-operator-5c4b-old_00000000"
		leases.HoldAs(holder)
		c := startCandidate(t, leases)
		time.Sleep(10 * time.Second)

		c.halt()

		if c.started() || leases.Holder() != holder {
			t.Errorf("acting = %v with the Lease held by %q, want no act and the Lease still held by %s", c.started(), leases.Holder(), holder)
		}
	})
}
