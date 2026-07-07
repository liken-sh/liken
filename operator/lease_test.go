package main

// The election's pure half: what a contender does given the lease as
// it stands.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func lease(holder string, renewedAgo time.Duration) *leaseObject {
	l := &leaseObject{}
	l.Spec.HolderIdentity = holder
	if renewedAgo >= 0 {
		l.Spec.RenewTime = sweepNow.Add(-renewedAgo).UTC().Format(microTime)
	}
	return l
}

func TestLeaseHolderRenews(t *testing.T) {
	if got := leaseAction(lease("node-1", 10*time.Second), "node-1", sweepNow); got != leaseRenew {
		t.Errorf("got %s", got)
	}
}

func TestLeaseWithALiveHolderMeansStandBy(t *testing.T) {
	if got := leaseAction(lease("node-2", 10*time.Second), "node-1", sweepNow); got != leaseStandby {
		t.Errorf("got %s", got)
	}
}

func TestLeaseWithASilentHolderIsTaken(t *testing.T) {
	if got := leaseAction(lease("node-2", 5*time.Minute), "node-1", sweepNow); got != leaseTake {
		t.Errorf("a holder past the lease duration has lost it, got %s", got)
	}
}

func TestLeaseWithNoHolderIsTaken(t *testing.T) {
	if got := leaseAction(lease("", -1), "node-1", sweepNow); got != leaseTake {
		t.Errorf("got %s", got)
	}
}

func TestLeaseWithAnUnreadableRenewTimeIsTaken(t *testing.T) {
	l := lease("node-2", -1)
	l.Spec.RenewTime = "not a timestamp"
	if got := leaseAction(l, "node-1", sweepNow); got != leaseTake {
		t.Errorf("a claim whose renewal can't be read is no claim, got %s", got)
	}
}

// leaseAPI is a miniature API server holding one Lease, used to test
// the acting half against the pure decisions above: it answers GETs
// with the standing lease (404 when there is none) and remembers
// whatever a create or update writes.
type leaseAPI struct {
	lease *leaseObject
}

func (api *leaseAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if api.lease == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(api.lease)
		case http.MethodPost, http.MethodPut:
			l := &leaseObject{}
			_ = json.NewDecoder(r.Body).Decode(l)
			api.lease = l
		}
	})
}

func TestHoldFleetLeaseCreatesTheFirstClaim(t *testing.T) {
	api := &leaseAPI{}
	client := testClient(t, api.handler())
	if !holdFleetLease(client, "node-1", sweepNow) {
		t.Fatal("with no lease standing, the first contender holds it")
	}
	if api.lease == nil || api.lease.Spec.HolderIdentity != "node-1" {
		t.Errorf("the created lease should name its holder: %+v", api.lease)
	}
}

func TestHoldFleetLeaseRenewsItsOwnClaim(t *testing.T) {
	api := &leaseAPI{lease: lease("node-1", 30*time.Second)}
	client := testClient(t, api.handler())
	if !holdFleetLease(client, "node-1", sweepNow) {
		t.Fatal("the holder keeps holding")
	}
	if api.lease.Spec.RenewTime != sweepNow.UTC().Format(microTime) {
		t.Errorf("holding is renewing: %s", api.lease.Spec.RenewTime)
	}
}

func TestHoldFleetLeaseStandsByForALiveHolder(t *testing.T) {
	api := &leaseAPI{lease: lease("node-2", 30*time.Second)}
	client := testClient(t, api.handler())
	if holdFleetLease(client, "node-1", sweepNow) {
		t.Fatal("a live claim by someone else means stand by")
	}
	if api.lease.Spec.HolderIdentity != "node-2" {
		t.Errorf("standing by writes nothing: %+v", api.lease)
	}
}

func TestHoldFleetLeaseTakesOverFromASilentHolder(t *testing.T) {
	api := &leaseAPI{lease: lease("node-2", 5*time.Minute)}
	client := testClient(t, api.handler())
	if !holdFleetLease(client, "node-1", sweepNow) {
		t.Fatal("a stale claim is there for the taking")
	}
	if api.lease.Spec.HolderIdentity != "node-1" {
		t.Errorf("the takeover should rename the holder: %+v", api.lease)
	}
	if api.lease.Spec.AcquireTime != api.lease.Spec.RenewTime {
		t.Errorf("a takeover is a fresh acquisition: %+v", api.lease.Spec)
	}
}

func TestHeartbeatCreatesTheFirstLease(t *testing.T) {
	api := &leaseAPI{}
	client := testClient(t, api.handler())
	renewMachineHeartbeat(client, "node-1", sweepNow)
	if api.lease == nil || api.lease.Spec.HolderIdentity != "node-1" {
		t.Fatalf("the first pass creates the machine's lease: %+v", api.lease)
	}
}

func TestHeartbeatRenewsAnAgedLease(t *testing.T) {
	api := &leaseAPI{lease: lease("node-1", 30*time.Second)}
	client := testClient(t, api.handler())
	renewMachineHeartbeat(client, "node-1", sweepNow)
	if api.lease.Spec.RenewTime != sweepNow.UTC().Format(microTime) {
		t.Errorf("an aged lease should renew: %s", api.lease.Spec.RenewTime)
	}
}

func TestHeartbeatLeavesAFreshLeaseAlone(t *testing.T) {
	// Most reconcile passes are event-driven and land seconds apart;
	// the heartbeat costs them a read, never a write.
	api := &leaseAPI{lease: lease("node-1", 5*time.Second)}
	client := testClient(t, api.handler())
	before := api.lease.Spec.RenewTime
	renewMachineHeartbeat(client, "node-1", sweepNow)
	if api.lease.Spec.RenewTime != before {
		t.Errorf("a fresh lease should not be rewritten: %s", api.lease.Spec.RenewTime)
	}
}

func TestListMachineHeartbeatsReadsRenewals(t *testing.T) {
	api := &leaseListAPI{leases: []*leaseObject{
		lease("node-1", 10*time.Second),
		lease("node-2", 5*time.Minute),
	}}
	api.leases[0].Metadata.Name = "node-1"
	api.leases[1].Metadata.Name = "node-2"
	client := testClient(t, api.handler())
	renewals, err := listMachineHeartbeats(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(renewals) != 2 {
		t.Fatalf("got %v", renewals)
	}
	if !renewals["node-1"].Equal(sweepNow.Add(-10 * time.Second)) {
		t.Errorf("node-1 renewed at %v", renewals["node-1"])
	}
}

// leaseListAPI answers a list request with a fixed set of leases.
type leaseListAPI struct {
	leases []*leaseObject
}

func (api *leaseListAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var list struct {
			Items []*leaseObject `json:"items"`
		}
		list.Items = api.leases
		_ = json.NewEncoder(w).Encode(&list)
	})
}

func TestTakingTheLeaseCountsTheTransition(t *testing.T) {
	api := &leaseAPI{lease: lease("node-2", 5*time.Minute)}
	client := testClient(t, api.handler())
	if !holdFleetLease(client, "node-1", sweepNow) {
		t.Fatal("a stale claim is there for the taking")
	}
	if api.lease.Spec.LeaseTransitions != 1 {
		t.Errorf("a takeover is a transition: %d", api.lease.Spec.LeaseTransitions)
	}
}
