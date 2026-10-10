package main

// The election in front of the operator: a copy that waits for the
// Lease sends nothing to the API server, and a copy that leads
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
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const testPod = "observatory-operator-7d9f-x2k4q"

// candidate is one copy of the operator's process that competes for
// the Lease and runs the operator of every namespace while it leads,
// the way operate does.
type candidate struct {
	api *fakeAPI
	// acting closes when the copy starts to act, and done when
	// actWhileLeading returns.
	acting chan struct{}
	done   chan struct{}
	stop   context.CancelFunc
}

// startCandidate runs a copy of the operator behind the election, on
// the example's resources and a Reservation of the east telescope. It
// runs in a synctest bubble.
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
		t.Fatal(err)
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
		Exit:   func(code int) { t.Errorf("the election ended the process with code %d", code) },
	}
	ctx, stop := context.WithCancel(t.Context())
	c := &candidate{api: api, acting: make(chan struct{}), done: make(chan struct{}), stop: stop}
	go func() {
		defer close(c.done)
		err := actWhileLeading(ctx, leases.Config(t), options, func(mayWrite func() error) {
			close(c.acting)
			n := leadingNamespaces(ctx, client, mayWrite, dialer, logs, testPod)
			n.run(ctx, func(ctx context.Context, changed *bell) *stores { return startWatches(ctx, watcher, changed) })
		})
		if err != nil {
			t.Errorf("actWhileLeading = %v", err)
		}
	}()
	t.Cleanup(func() {
		stop()
		<-c.done
	})
	return c
}

// halt stops the copy as a SIGTERM does, and waits until it returns.
func (c *candidate) halt() {
	c.stop()
	<-c.done
}

// started answers whether the copy acts.
func (c *candidate) started() bool {
	select {
	case <-c.acting:
		return true
	default:
		return false
	}
}

// A copy that finds the Lease held waits, and sends nothing to the API
// server, so it creates no pod and writes no status. It acts once the
// holder's Lease expires.
func TestAWaitingCopyDoesNotAct(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		leases := electiontest.New(leaseName, "liken-system")
		leases.HoldAs("observatory-operator-5c4b-old_00000000")
		c := startCandidate(t, leases)
		var sent []string
		var mu sync.Mutex
		c.api.hear(func(r *http.Request) {
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

// A copy that leads writes only while the Lease names it, and a
// shutdown in the middle of a reservation's activation releases the
// Lease after the last write of every runner and of the status writer.
func TestTheLeaseIsReleasedAfterTheLastWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		leases := electiontest.New(leaseName, "liken-system")
		c := startCandidate(t, leases)
		var holders []string
		var mu sync.Mutex
		c.api.hear(func(r *http.Request) {
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
			t.Fatal("the operator wrote nothing before the shutdown")
		}
		for i, holder := range holders {
			if !strings.HasPrefix(holder, testPod+"_") {
				t.Errorf("write %d of %d went out with the Lease held by %q, want this copy", i+1, len(holders), holder)
			}
		}
		if leases.Holder() != "" {
			t.Errorf("the Lease names %q after the shutdown, want no holder", leases.Holder())
		}
	})
}

// A copy that cannot name its Lease fails, and does not act.
func TestACopyWithNoLeaseNameDoesNotAct(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		leases := electiontest.New(leaseName, "liken-system")
		acted := false

		err := actWhileLeading(t.Context(), leases.Config(t), election.Options{Name: leaseName, Namespace: "liken-system"},
			func(func() error) { acted = true })

		if err == nil || acted {
			t.Errorf("actWhileLeading = %v and acted = %v, want an error and no act", err, acted)
		}
	})
}

// A copy that stops while it waits returns without an error, never
// acts, and leaves the holder's Lease alone.
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
