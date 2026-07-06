package main

// Leader election for the fleet sweep.
//
// Every leader carries the sweep's code, but only one should run it
// at a time. Not for safety — every sweeper computes the same
// verdicts from the same data, and the API server's optimistic
// concurrency (resourceVersion, 409 Conflict) already serializes the
// writes — but for quiet: concurrent sweepers reject each other's
// writes on every change and fill the logs with conflicts that are
// all working as intended and all noise.
//
// Kubernetes has a standard answer, and it's the same one it uses for
// its own control plane: a Lease. kube-controller-manager and
// kube-scheduler run one replica per control-plane node, and all but
// one of them stand idle, watching a small coordination.k8s.io Lease
// object that names the current holder and when it last renewed. A
// contender that holds the lease works and keeps renewing; one that
// doesn't stands by; one that finds the claim gone stale takes it.
// client-go wraps this in its leaderelection package; underneath, it
// is nothing but the reads and conditional writes below — the same
// optimistic concurrency as everything else, pointed at an object
// whose only content is the holder's name and the time of its last
// renewal. The mechanism is the same one the machine heartbeats use:
// a timestamp renewed on a schedule, where a stale value means the
// holder is gone.
//
// Losing the holder costs nothing but latency: its renewals stop,
// the lease ages past its duration, and the next leader's pass takes
// over — the sweep pauses for at most a lease duration, and a
// machine's Lost verdict arrives a minute late. The election
// deliberately runs on the reconcile loop's own cadence rather than
// a dedicated goroutine: the sweep happens at most once per pass, so
// renewing any faster would accomplish nothing.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// The sweep's lease lives beside the operator in liken-system. Leases
// are namespaced (unlike Machines and Clusters) because they're
// working state, not API: the coordination group's whole job is
// letting replicas of a thing coordinate, and replicas live in
// namespaces.
const fleetLeasePath = "/apis/coordination.k8s.io/v1/namespaces/liken-system/leases/liken-fleet-sweep"

// fleetLeaseDuration is how long a holder's claim stands without a
// renewal: three missed renewals, the same threshold the machine
// heartbeats use, and for the same reason.
const fleetLeaseDuration = 90 * time.Second

// microTime is the layout coordination.k8s.io uses for its
// timestamps (metav1.MicroTime): RFC 3339 with microseconds, a finer
// grain than most of the API because leases exist to compare
// closely-spaced instants.
const microTime = "2006-01-02T15:04:05.000000Z07:00"

type leaseObject struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   machine.ObjectMeta `json:"metadata"`
	Spec       struct {
		HolderIdentity       string `json:"holderIdentity,omitempty"`
		LeaseDurationSeconds int    `json:"leaseDurationSeconds,omitempty"`
		AcquireTime          string `json:"acquireTime,omitempty"`
		RenewTime            string `json:"renewTime,omitempty"`
	} `json:"spec"`
}

// A leaseVerdict is what a contender should do about the lease as it
// stands: renew it (this machine holds it), take it (nobody does, or
// the holder went silent), or stand by (someone else holds a live
// claim).
type leaseVerdict string

const (
	leaseRenew   leaseVerdict = "renew"
	leaseTake    leaseVerdict = "take"
	leaseStandby leaseVerdict = "standby"
)

// leaseAction is the election's pure half: the verdict for this
// contender given the lease as it stands.
func leaseAction(l *leaseObject, self string, now time.Time) leaseVerdict {
	if l.Spec.HolderIdentity == self {
		return leaseRenew
	}
	renewed, err := time.Parse(microTime, l.Spec.RenewTime)
	if l.Spec.HolderIdentity == "" || err != nil || now.Sub(renewed) > fleetLeaseDuration {
		return leaseTake
	}
	return leaseStandby
}

// holdFleetLease returns whether this machine holds the sweep lease,
// acquiring or renewing it if it can. Every write is conditional —
// the create fails if someone else created first, the update carries
// the resourceVersion we read — so two leaders grabbing at the same
// moment resolve the way all contended writes do: one wins, the
// other reads the winner's claim next pass and stands by.
func holdFleetLease(c *apiClient, self string, now time.Time) bool {
	lease := &leaseObject{}
	err := c.requestJSON(http.MethodGet, fleetLeasePath, nil, lease)
	if errors.Is(err, errNotFound) {
		return createFleetLease(c, self, now)
	}
	if err != nil {
		fmt.Printf("reading the fleet sweep lease: %v\n", err)
		return false
	}

	action := leaseAction(lease, self, now)
	if action == leaseStandby {
		return false
	}
	lease.Spec.HolderIdentity = self
	lease.Spec.LeaseDurationSeconds = int(fleetLeaseDuration.Seconds())
	lease.Spec.RenewTime = now.UTC().Format(microTime)
	if action == leaseTake {
		lease.Spec.AcquireTime = lease.Spec.RenewTime
	}
	body, err := json.Marshal(lease)
	if err != nil {
		return false
	}
	if err := c.requestJSON(http.MethodPut, fleetLeasePath, body, nil); err != nil {
		if !errors.Is(err, errConflict) {
			fmt.Printf("writing the fleet sweep lease: %v\n", err)
		}
		return false
	}
	if action == leaseTake {
		fmt.Println("holding the fleet sweep lease; this machine watches the fleet now")
	}
	return true
}

func createFleetLease(c *apiClient, self string, now time.Time) bool {
	lease := &leaseObject{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	lease.Metadata.Name = "liken-fleet-sweep"
	lease.Spec.HolderIdentity = self
	lease.Spec.LeaseDurationSeconds = int(fleetLeaseDuration.Seconds())
	lease.Spec.AcquireTime = now.UTC().Format(microTime)
	lease.Spec.RenewTime = lease.Spec.AcquireTime
	body, err := json.Marshal(lease)
	if err != nil {
		return false
	}
	createPath := "/apis/coordination.k8s.io/v1/namespaces/liken-system/leases"
	if err := c.requestJSON(http.MethodPost, createPath, body, nil); err != nil {
		// errConflict here is another leader creating first: they won.
		if !errors.Is(err, errConflict) {
			fmt.Printf("creating the fleet sweep lease: %v\n", err)
		}
		return false
	}
	fmt.Println("holding the fleet sweep lease; this machine watches the fleet now")
	return true
}
