package main

// Leader election for the fleet sweep.
//
// Every leader carries the sweep's code, but only one should run it
// at a time. The reason is not safety: every sweeper computes the
// same verdicts from the same data, and the API server's optimistic
// concurrency (resourceVersion, 409 Conflict) already serializes the
// writes. The reason is noise: concurrent sweepers reject each
// other's writes on every change and fill the logs with conflicts
// that are working as intended but tell nobody anything.
//
// Kubernetes has a standard answer, and it's the same one it uses for
// its own control plane: a Lease. kube-controller-manager and
// kube-scheduler run one replica per control-plane node, and all but
// one of them stand idle, watching a small coordination.k8s.io Lease
// object that names the current holder and when it last renewed. A
// contender that holds the lease works and keeps renewing; one that
// doesn't stands by; one that finds the claim gone stale takes it.
// client-go wraps this in its leaderelection package; underneath, it
// is nothing but the reads and conditional writes below: the same
// optimistic concurrency as everything else, pointed at an object
// whose only content is the holder's name and the time of its last
// renewal. The mechanism is the same one the machine heartbeats use:
// a timestamp renewed on a schedule, where a stale value means the
// holder is gone.
//
// Losing the holder costs nothing but latency: its renewals stop,
// the lease ages past its duration, and the next leader's pass takes
// over. The sweep pauses for at most one lease duration, and a
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

// The machines' heartbeat leases live in a namespace of their own,
// one Lease per machine, named for it. This is the arrangement of
// kube-node-lease, adopted for the same reasons. A heartbeat must
// renew on a schedule forever, so it should be the cheapest write
// the API server offers, and a Lease is a few dozen bytes with no
// watchers. A timestamp inside Machine status would instead rewrite
// the whole object (hardware inventory, boot record, conditions) and
// wake every watcher on every renewal of every machine. Kubernetes
// moved the kubelet's heartbeats out of Node status and into
// kube-node-lease to escape exactly that; liken heartbeats through a
// lease from the start.
const machineLeaseDir = "/apis/coordination.k8s.io/v1/namespaces/liken-machine-lease/leases"

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

		// LeaseTransitions counts changes of holder, incremented on
		// every takeover. A high count on a long-lived lease means
		// the holders keep dying.
		LeaseTransitions int `json:"leaseTransitions,omitempty"`
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
// acquiring or renewing it if it can. Every write is conditional:
// the create fails if someone else created first, and the update
// carries the resourceVersion we read. Two leaders grabbing at the
// same moment therefore resolve the way all contended writes do: one
// wins, and the other reads the winner's claim next pass and stands
// by.
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
		lease.Spec.LeaseTransitions++
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
	body, err := json.Marshal(newLease("liken-fleet-sweep", self, fleetLeaseDuration, now))
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

// newLease is a fresh claim: held by holder as of now.
func newLease(name, holder string, duration time.Duration, now time.Time) *leaseObject {
	lease := &leaseObject{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	lease.Metadata.Name = name
	lease.Spec.HolderIdentity = holder
	lease.Spec.LeaseDurationSeconds = int(duration.Seconds())
	lease.Spec.AcquireTime = now.UTC().Format(microTime)
	lease.Spec.RenewTime = lease.Spec.AcquireTime
	return lease
}

// renewMachineHeartbeat keeps this machine's own lease fresh: create
// it if it doesn't exist, renew it once it has aged past
// heartbeatRenewAfter, and leave it alone otherwise, so most passes
// cost one read and no write. Unlike the sweep's lease there is no
// election here: this machine is its lease's only writer, the way a
// kubelet is the only writer of its node lease. Every failure mode
// is therefore just "try again next pass."
func renewMachineHeartbeat(c *apiClient, name string, now time.Time) {
	path := machineLeaseDir + "/" + name
	lease := &leaseObject{}
	err := c.requestJSON(http.MethodGet, path, nil, lease)
	if errors.Is(err, errNotFound) {
		body, err := json.Marshal(newLease(name, name, heartbeatStaleAfter, now))
		if err != nil {
			return
		}
		if err := c.requestJSON(http.MethodPost, machineLeaseDir, body, nil); err != nil && !errors.Is(err, errConflict) {
			fmt.Printf("creating the heartbeat lease: %v\n", err)
		}
		return
	}
	if err != nil {
		fmt.Printf("reading the heartbeat lease: %v\n", err)
		return
	}
	if renewed, err := time.Parse(microTime, lease.Spec.RenewTime); err == nil && now.Sub(renewed) < heartbeatRenewAfter {
		return
	}
	lease.Spec.HolderIdentity = name
	lease.Spec.LeaseDurationSeconds = int(heartbeatStaleAfter.Seconds())
	lease.Spec.RenewTime = now.UTC().Format(microTime)
	body, err := json.Marshal(lease)
	if err != nil {
		return
	}
	if err := c.requestJSON(http.MethodPut, path, body, nil); err != nil {
		fmt.Printf("renewing the heartbeat lease: %v\n", err)
	}
}

// listMachineHeartbeats reads every machine's last renewal for the
// sweep: one cheap list yields the fleet's liveness, mapping each
// machine's name to the moment of its last renewal.
func listMachineHeartbeats(c *apiClient) (map[string]time.Time, error) {
	var list struct {
		Items []leaseObject `json:"items"`
	}
	if err := c.requestJSON(http.MethodGet, machineLeaseDir, nil, &list); err != nil {
		return nil, err
	}
	renewals := map[string]time.Time{}
	for _, l := range list.Items {
		if renewed, err := time.Parse(microTime, l.Spec.RenewTime); err == nil {
			renewals[l.Metadata.Name] = renewed
		}
	}
	return renewals, nil
}
