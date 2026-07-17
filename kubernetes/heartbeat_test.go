package kubernetes

// The heartbeat protocol from both sides: a machine keeping its own
// lease fresh, and the fleet's observer reading every renewal.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

var heartbeatNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func testLease(name string, renewedAgo time.Duration) *lease {
	l := &lease{}
	l.Metadata.Name = name
	l.Spec.HolderIdentity = name
	if renewedAgo >= 0 {
		l.Spec.RenewTime = heartbeatNow.Add(-renewedAgo).UTC().Format(microTime)
	}
	return l
}

// leaseAPI is a miniature API server holding one Lease: it answers
// GETs with the standing lease (404 when there is none) and
// remembers whatever a create or update writes. fail scripts a
// refusal: any request with that method is answered with the given
// status instead of being served.
type leaseAPI struct {
	lease *lease
	fail  map[string]int
}

func (fake *leaseAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			l := &lease{}
			_ = json.NewDecoder(r.Body).Decode(l)
			fake.lease = l
		}
	})
}

func TestHeartbeatCreatesTheFirstLease(t *testing.T) {
	fake := &leaseAPI{}
	client := testClient(t, fake.handler())
	RenewHeartbeat(client, "node-1", heartbeatNow)
	if fake.lease == nil || fake.lease.Spec.HolderIdentity != "node-1" {
		t.Fatalf("the first pass creates the machine's lease: %+v", fake.lease)
	}
}

func TestHeartbeatRenewsAnAgedLease(t *testing.T) {
	fake := &leaseAPI{lease: testLease("node-1", 30*time.Second)}
	client := testClient(t, fake.handler())
	RenewHeartbeat(client, "node-1", heartbeatNow)
	if fake.lease.Spec.RenewTime != heartbeatNow.UTC().Format(microTime) {
		t.Errorf("an aged lease should renew: %s", fake.lease.Spec.RenewTime)
	}
}

func TestHeartbeatLeavesAFreshLeaseAlone(t *testing.T) {
	// Most reconcile passes are event-driven and land seconds apart;
	// the heartbeat costs them a read, never a write.
	fake := &leaseAPI{lease: testLease("node-1", 5*time.Second)}
	client := testClient(t, fake.handler())
	before := fake.lease.Spec.RenewTime
	RenewHeartbeat(client, "node-1", heartbeatNow)
	if fake.lease.Spec.RenewTime != before {
		t.Errorf("a fresh lease should not be rewritten: %s", fake.lease.Spec.RenewTime)
	}
}

// The heartbeat's failure handling is "report it and wait for the
// next pass": each machine is its lease's only writer, so nothing is
// lost by trying again in a few seconds. These three tests refuse
// each of the protocol's requests in turn and expect the standing
// lease to come through untouched.

func TestHeartbeatSurvivesARefusedRead(t *testing.T) {
	fake := &leaseAPI{fail: map[string]int{http.MethodGet: http.StatusInternalServerError}}
	client := testClient(t, fake.handler())
	RenewHeartbeat(client, "node-1", heartbeatNow)
	if fake.lease != nil {
		t.Errorf("an unreadable lease must not be rewritten: %+v", fake.lease)
	}
}

func TestHeartbeatSurvivesARefusedCreate(t *testing.T) {
	fake := &leaseAPI{fail: map[string]int{http.MethodPost: http.StatusInternalServerError}}
	client := testClient(t, fake.handler())
	RenewHeartbeat(client, "node-1", heartbeatNow)
	if fake.lease != nil {
		t.Errorf("a refused create leaves no lease behind: %+v", fake.lease)
	}
}

func TestHeartbeatSurvivesARefusedRenewal(t *testing.T) {
	fake := &leaseAPI{
		lease: testLease("node-1", 30*time.Second),
		fail:  map[string]int{http.MethodPut: http.StatusInternalServerError},
	}
	client := testClient(t, fake.handler())
	before := fake.lease.Spec.RenewTime
	RenewHeartbeat(client, "node-1", heartbeatNow)
	if fake.lease.Spec.RenewTime != before {
		t.Errorf("a refused renewal changes nothing: %s", fake.lease.Spec.RenewTime)
	}
}

// leaseListAPI answers a list request with a fixed set of leases.
type leaseListAPI struct {
	leases []*lease
}

func (fake *leaseListAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var list struct {
			Items []*lease `json:"items"`
		}
		list.Items = fake.leases
		_ = json.NewEncoder(w).Encode(&list)
	})
}

func TestListHeartbeatsReadsRenewals(t *testing.T) {
	fake := &leaseListAPI{leases: []*lease{
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
	// A lease whose renewal can't be parsed carries no liveness
	// claim; it simply doesn't appear, and the sweep reads its
	// machine as never heard from.
	broken := testLease("node-2", -1)
	broken.Spec.RenewTime = "not a timestamp"
	fake := &leaseListAPI{leases: []*lease{
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
