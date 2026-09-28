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
	NewHeartbeat("node-1").Renew(client, testMachineOwner, heartbeatNow)
	if fake.lease == nil || fake.lease.Spec.HolderIdentity != "node-1" {
		t.Fatalf("the first pass creates the machine's lease: %+v", fake.lease)
	}
}

func TestHeartbeatRenewsAnAgedLease(t *testing.T) {
	fake := &leaseAPI{}
	fake.store(testLease("node-1", 30*time.Second))
	client := testClient(t, fake.handler())
	NewHeartbeat("node-1").Renew(client, testMachineOwner, heartbeatNow)
	if fake.lease.Spec.RenewTime != heartbeatNow.UTC().Format(microTime) {
		t.Errorf("an aged lease should renew: %s", fake.lease.Spec.RenewTime)
	}
}

func TestHeartbeatLeavesAFreshLeaseAlone(t *testing.T) {
	fake := &leaseAPI{}
	fake.store(testLease("node-1", 5*time.Second))
	client := testClient(t, fake.handler())
	before := fake.lease.Spec.RenewTime
	NewHeartbeat("node-1").Renew(client, testMachineOwner, heartbeatNow)
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
			h.Renew(client, testMachineOwner, heartbeatNow)
			fake.requests = nil

			h.Renew(client, testMachineOwner, heartbeatNow.Add(c.after))

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
			h.Renew(client, testMachineOwner, heartbeatNow)
			c.change(fake)
			fake.requests = nil
			later := heartbeatNow.Add(10 * time.Second)

			h.Renew(client, testMachineOwner, later)

			if !slices.Equal(fake.requests, c.want) || fake.lease.Spec.RenewTime != later.UTC().Format(microTime) {
				t.Errorf("requests = %q, renewed at %s; want %q and a renewal at %s",
					fake.requests, fake.lease.Spec.RenewTime, c.want, later.UTC().Format(microTime))
			}
		})
	}
}

// The lease names its Machine as its owner, so the garbage collector
// deletes the lease when the Machine is deleted. A lease from before
// the owner existed takes it on its next renewal. A lease whose owner
// is a Machine of the same name that was deleted and created again
// takes the new Machine's UID.
func TestTheLeaseIsOwnedByItsMachine(t *testing.T) {
	cases := []struct {
		name  string
		lease *Lease
	}{
		{"a new lease", nil},
		{"a lease with no owner", testLease("node-1", 30*time.Second)},
		{"a lease owned by an earlier Machine", ownedLease(testLease("node-1", 30*time.Second), "uid-earlier")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &leaseAPI{}
			if c.lease != nil {
				fake.store(c.lease)
			}
			client := testClient(t, fake.handler())

			NewHeartbeat("node-1").Renew(client, testMachineOwner, heartbeatNow)

			if fake.lease == nil || !slices.Equal(fake.lease.Metadata.OwnerReferences, []OwnerReference{testMachineOwner}) {
				t.Errorf("the lease's owners are %+v, want %+v", fake.lease, testMachineOwner)
			}
		})
	}
}

// An owner with no UID is one the API server refuses, and a refused
// renewal is a missed heartbeat. The lease renews with no owner.
func TestAnOwnerWithNoUIDIsLeftOut(t *testing.T) {
	fake := &leaseAPI{}
	fake.store(testLease("node-1", 30*time.Second))
	client := testClient(t, fake.handler())
	owner := testMachineOwner
	owner.UID = ""

	NewHeartbeat("node-1").Renew(client, owner, heartbeatNow)

	if fake.lease.Spec.RenewTime != heartbeatNow.UTC().Format(microTime) || fake.lease.Metadata.OwnerReferences != nil {
		t.Errorf("the lease renewed at %s with owners %+v; want a renewal at %s with none",
			fake.lease.Spec.RenewTime, fake.lease.Metadata.OwnerReferences, heartbeatNow.UTC().Format(microTime))
	}
}

// A pass whose Machine is gone renews with no heartbeat, and sends
// nothing.
func TestANilHeartbeatSendsNothing(t *testing.T) {
	fake := &leaseAPI{}
	client := testClient(t, fake.handler())

	var h *Heartbeat
	h.Renew(client, testMachineOwner, heartbeatNow)

	if len(fake.requests) != 0 {
		t.Errorf("requests = %q, want none", fake.requests)
	}
}

// A renewal replaces the whole lease, so it keeps the labels and
// annotations that another client set.
func TestARenewalKeepsLabelsAndAnnotations(t *testing.T) {
	fake := &leaseAPI{}
	l := testLease("node-1", 30*time.Second)
	l.Metadata.Labels = map[string]string{"team": "lab"}
	l.Metadata.Annotations = map[string]string{"note": "kept"}
	fake.store(l)
	client := testClient(t, fake.handler())

	NewHeartbeat("node-1").Renew(client, testMachineOwner, heartbeatNow)

	if fake.lease.Metadata.Labels["team"] != "lab" || fake.lease.Metadata.Annotations["note"] != "kept" {
		t.Errorf("the renewal left labels %v and annotations %v", fake.lease.Metadata.Labels, fake.lease.Metadata.Annotations)
	}
}

var testMachineOwner = OwnerReference{
	APIVersion: "liken.sh/v1alpha1", Kind: "Machine", Name: "node-1", UID: "uid-node-1",
}

func ownedLease(l *Lease, uid string) *Lease {
	owner := testMachineOwner
	owner.UID = uid
	l.Metadata.OwnerReferences = []OwnerReference{owner}
	return l
}

// The heartbeat's failure handling follows one rule: report the
// failure and wait for the next pass. Each machine is the only
// writer of its own lease, so trying again in a few seconds loses
// nothing. These three tests refuse each of the protocol's requests
// in turn, and expect the current lease to remain untouched.

func TestHeartbeatSurvivesARefusedRead(t *testing.T) {
	fake := &leaseAPI{fail: map[string]int{http.MethodGet: http.StatusInternalServerError}}
	client := testClient(t, fake.handler())
	NewHeartbeat("node-1").Renew(client, testMachineOwner, heartbeatNow)
	if fake.lease != nil {
		t.Errorf("an unreadable lease must not be rewritten: %+v", fake.lease)
	}
}

func TestHeartbeatSurvivesARefusedCreate(t *testing.T) {
	fake := &leaseAPI{fail: map[string]int{http.MethodPost: http.StatusInternalServerError}}
	client := testClient(t, fake.handler())
	NewHeartbeat("node-1").Renew(client, testMachineOwner, heartbeatNow)
	if fake.lease != nil {
		t.Errorf("a refused create leaves no lease behind: %+v", fake.lease)
	}
}

func TestHeartbeatSurvivesARefusedRenewal(t *testing.T) {
	fake := &leaseAPI{fail: map[string]int{http.MethodPut: http.StatusInternalServerError}}
	fake.store(testLease("node-1", 30*time.Second))
	client := testClient(t, fake.handler())
	before := fake.lease.Spec.RenewTime
	NewHeartbeat("node-1").Renew(client, testMachineOwner, heartbeatNow)
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
