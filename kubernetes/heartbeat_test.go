package kubernetes

// These tests cover the heartbeat protocol from both sides: a machine
// keeping its own lease current, and the fleet's observer reading
// every renewal.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"
)

var heartbeatNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func testLease(name string, renewedAgo time.Duration) *Lease {
	l := &Lease{}
	l.Metadata.Name = name
	l.Spec.HolderIdentity = name
	if renewedAgo >= 0 {
		l.Spec.RenewTime = heartbeatNow.Add(-renewedAgo).UTC().Format(microTime)
	}
	return l
}

// leaseAPI is a small API server that holds one Lease the way the API
// server does. It answers GET requests with the current lease, or 404
// when there is none. A create or an update stores the lease at a new
// resourceVersion and answers with it, and an update from a stale
// resourceVersion answers 409. The fail field scripts a refusal: the
// server answers any request that uses that method with the given
// status, instead of serving the request. requests records the method
// of each request.
type leaseAPI struct {
	lease    *Lease
	version  int
	fail     map[string]int
	requests []string
}

func (fake *leaseAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.requests = append(fake.requests, r.Method)
		if status, refused := fake.fail[r.Method]; refused {
			w.WriteHeader(status)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if fake.lease == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(fake.lease)
		case http.MethodPost, http.MethodPut:
			l := &Lease{}
			_ = json.NewDecoder(r.Body).Decode(l)
			if r.Method == http.MethodPut && (fake.lease == nil || l.Metadata.ResourceVersion != fake.lease.Metadata.ResourceVersion) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			fake.store(l)
			_ = json.NewEncoder(w).Encode(fake.lease)
		}
	})
}

// store writes a lease at the next resourceVersion, the way another
// writer or the API server itself would.
func (fake *leaseAPI) store(l *Lease) {
	fake.version++
	l.Metadata.ResourceVersion = strconv.Itoa(fake.version)
	fake.lease = l
}

func TestHeartbeatCreatesTheFirstLease(t *testing.T) {
	fake := &leaseAPI{}
	client := testClient(t, fake.handler())
	NewHeartbeat("node-1").Renew(client, heartbeatNow)
	if fake.lease == nil || fake.lease.Spec.HolderIdentity != "node-1" {
		t.Fatalf("the first pass creates the machine's lease: %+v", fake.lease)
	}
}

func TestHeartbeatRenewsAnAgedLease(t *testing.T) {
	fake := &leaseAPI{}
	fake.store(testLease("node-1", 30*time.Second))
	client := testClient(t, fake.handler())
	NewHeartbeat("node-1").Renew(client, heartbeatNow)
	if fake.lease.Spec.RenewTime != heartbeatNow.UTC().Format(microTime) {
		t.Errorf("an aged lease should renew: %s", fake.lease.Spec.RenewTime)
	}
}

func TestHeartbeatLeavesAFreshLeaseAlone(t *testing.T) {
	fake := &leaseAPI{}
	fake.store(testLease("node-1", 5*time.Second))
	client := testClient(t, fake.handler())
	before := fake.lease.Spec.RenewTime
	NewHeartbeat("node-1").Renew(client, heartbeatNow)
	if fake.lease.Spec.RenewTime != before {
		t.Errorf("a fresh lease should not be rewritten: %s", fake.lease.Spec.RenewTime)
	}
}

// After the first renewal, the heartbeat renews from the lease it
// wrote. A ticker pass sends one update and no read, and a pass
// between two ticker passes sends nothing.
func TestAHeldLeaseRenewsWithNoRead(t *testing.T) {
	cases := []struct {
		name  string
		after time.Duration
		want  []string
	}{
		{"the next ticker pass", 10 * time.Second, []string{http.MethodPut}},
		{"a pass between ticker passes", 3 * time.Second, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &leaseAPI{}
			fake.store(testLease("node-1", 30*time.Second))
			client := testClient(t, fake.handler())
			h := NewHeartbeat("node-1")
			h.Renew(client, heartbeatNow)
			fake.requests = nil

			h.Renew(client, heartbeatNow.Add(c.after))

			if !slices.Equal(fake.requests, c.want) {
				t.Errorf("requests = %q, want %q", fake.requests, c.want)
			}
		})
	}
}

// A lease that something else wrote since the last renewal refuses the
// update with a conflict, and a deleted lease answers 404. The same
// pass reads the lease again and renews it.
func TestAHeldLeaseThatChangedIsReadAgain(t *testing.T) {
	cases := []struct {
		name   string
		change func(*leaseAPI)
		want   []string
	}{
		{"written by another client", func(f *leaseAPI) { f.store(testLease("node-1", 20*time.Second)) },
			[]string{http.MethodPut, http.MethodGet, http.MethodPut}},
		{"deleted", func(f *leaseAPI) { f.lease = nil },
			[]string{http.MethodPut, http.MethodGet, http.MethodPost}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &leaseAPI{}
			fake.store(testLease("node-1", 30*time.Second))
			client := testClient(t, fake.handler())
			h := NewHeartbeat("node-1")
			h.Renew(client, heartbeatNow)
			c.change(fake)
			fake.requests = nil
			later := heartbeatNow.Add(10 * time.Second)

			h.Renew(client, later)

			if !slices.Equal(fake.requests, c.want) || fake.lease.Spec.RenewTime != later.UTC().Format(microTime) {
				t.Errorf("requests = %q, renewed at %s; want %q and a renewal at %s",
					fake.requests, fake.lease.Spec.RenewTime, c.want, later.UTC().Format(microTime))
			}
		})
	}
}

// The heartbeat's failure handling follows one rule: report the
// failure and wait for the next pass. Each machine is the only
// writer of its own lease, so trying again in a few seconds loses
// nothing. These three tests refuse each of the protocol's requests
// in turn, and expect the current lease to remain untouched.

func TestHeartbeatSurvivesARefusedRead(t *testing.T) {
	fake := &leaseAPI{fail: map[string]int{http.MethodGet: http.StatusInternalServerError}}
	client := testClient(t, fake.handler())
	NewHeartbeat("node-1").Renew(client, heartbeatNow)
	if fake.lease != nil {
		t.Errorf("an unreadable lease must not be rewritten: %+v", fake.lease)
	}
}

func TestHeartbeatSurvivesARefusedCreate(t *testing.T) {
	fake := &leaseAPI{fail: map[string]int{http.MethodPost: http.StatusInternalServerError}}
	client := testClient(t, fake.handler())
	NewHeartbeat("node-1").Renew(client, heartbeatNow)
	if fake.lease != nil {
		t.Errorf("a refused create leaves no lease behind: %+v", fake.lease)
	}
}

func TestHeartbeatSurvivesARefusedRenewal(t *testing.T) {
	fake := &leaseAPI{fail: map[string]int{http.MethodPut: http.StatusInternalServerError}}
	fake.store(testLease("node-1", 30*time.Second))
	client := testClient(t, fake.handler())
	before := fake.lease.Spec.RenewTime
	NewHeartbeat("node-1").Renew(client, heartbeatNow)
	if fake.lease.Spec.RenewTime != before {
		t.Errorf("a refused renewal changes nothing: %s", fake.lease.Spec.RenewTime)
	}
}

// leaseListAPI answers a list request with a fixed set of leases.
type leaseListAPI struct {
	leases []*Lease
}

func (fake *leaseListAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var list struct {
			Items []*Lease `json:"items"`
		}
		list.Items = fake.leases
		_ = json.NewEncoder(w).Encode(&list)
	})
}

func TestListHeartbeatsReadsRenewals(t *testing.T) {
	fake := &leaseListAPI{leases: []*Lease{
		testLease("node-1", 10*time.Second),
		testLease("node-2", 5*time.Minute),
	}}
	client := testClient(t, fake.handler())
	renewals, err := ListHeartbeats(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(renewals) != 2 {
		t.Fatalf("got %v", renewals)
	}
	if !renewals["node-1"].Equal(heartbeatNow.Add(-10 * time.Second)) {
		t.Errorf("node-1 renewed at %v", renewals["node-1"])
	}
}

func TestListHeartbeatsSkipsAnUnreadableRenewal(t *testing.T) {
	// A lease whose renewal cannot be parsed carries no liveness
	// claim. It does not appear in the result, and the sweep reads
	// its machine as never heard from.
	broken := testLease("node-2", -1)
	broken.Spec.RenewTime = "not a timestamp"
	fake := &leaseListAPI{leases: []*Lease{
		testLease("node-1", 10*time.Second),
		broken,
	}}
	client := testClient(t, fake.handler())
	renewals, err := ListHeartbeats(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(renewals) != 1 {
		t.Fatalf("only the readable renewal should appear: %v", renewals)
	}
}
