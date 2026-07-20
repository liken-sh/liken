package kubernetes

// This file implements the machine heartbeat protocol: how a machine
// proves it is alive, and how the fleet's observer reads that proof.
//
// A machine's status is only as current as the last update from that
// machine. A dead machine cannot report that it is dead. Its last
// written status stays in the API showing Ready forever, which is
// worse than showing no status. Kubernetes has this same problem
// with kubelets, and solves it with heartbeats. The kubelet renews a
// lease every few seconds, and the node controller turns a silent
// lease into a NotReady Node. liken's machines get the same
// treatment. Each machine's operator renews a coordination.k8s.io
// Lease named for its machine. The cluster operator lists those
// leases to judge the fleet's liveness.
//
// This mechanism comes from kube-node-lease, and liken adopts it for
// the same reasons. A heartbeat must renew on a schedule forever, so
// it should be the cheapest write the API server offers. A Lease is
// a few dozen bytes with no watchers. A timestamp inside Machine
// status would instead rewrite the whole object (hardware inventory,
// boot record, conditions), and would wake every watcher on every
// renewal of every machine. Kubernetes moved the kubelet's
// heartbeats out of Node status and into kube-node-lease to avoid
// that cost. liken has used a lease for its heartbeats from the
// start, for the same reason. The leases live in the liken-system
// namespace, not in a copy of kube-node-lease's dedicated namespace.
// This means everything liken coordinates through sits in the one
// namespace that the OS owns: the command `kubectl get leases -n
// liken-system` shows the fleet's entire liveness picture.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/liken-sh/liken/api"
)

const heartbeatDir = "/apis/coordination.k8s.io/v1/namespaces/liken-system/leases"

// HeartbeatRenewAfter sets how old the heartbeat must be before the
// machine's own operator renews it. The value sits just under the
// ten-second reconcile ticker, so every ticker pass renews the
// lease, and the event-driven passes in between only need to read
// it. HeartbeatStaleAfter sets how long a machine may then stay
// silent before the cluster operator marks it Lost. A single missed
// renewal may only mean a busy moment. Several missed renewals mean
// the machine is down.
//
// These numbers come from kube-node-lease. The kubelet renews its
// lease every ten seconds, and the node controller waits forty
// seconds for a silent kubelet before its Node goes NotReady. A dead
// machine stops renewing both leases at the same moment, so matching
// the two thresholds means both verdicts land together. This way,
// `kubectl get nodes` never disagrees with `kubectl get machines` for
// a minute about a machine that just died.
const (
	HeartbeatRenewAfter = 8 * time.Second
	HeartbeatStaleAfter = 40 * time.Second
)

// microTime is the time layout that coordination.k8s.io uses for its
// timestamps (metav1.MicroTime): RFC 3339 with microseconds. This is
// a finer grain than most of the API uses, because leases exist to
// compare instants that are close together in time.
const microTime = "2006-01-02T15:04:05.000000Z07:00"

type lease struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   api.ObjectMeta `json:"metadata"`
	Spec       struct {
		HolderIdentity       string `json:"holderIdentity,omitempty"`
		LeaseDurationSeconds int    `json:"leaseDurationSeconds,omitempty"`
		AcquireTime          string `json:"acquireTime,omitempty"`
		RenewTime            string `json:"renewTime,omitempty"`
	} `json:"spec"`
}

// newLease creates a new claim, held by holder as of the given time.
func newLease(name, holder string, duration time.Duration, now time.Time) *lease {
	l := &lease{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	l.Metadata.Name = name
	l.Spec.HolderIdentity = holder
	l.Spec.LeaseDurationSeconds = int(duration.Seconds())
	l.Spec.AcquireTime = now.UTC().Format(microTime)
	l.Spec.RenewTime = l.Spec.AcquireTime
	return l
}

// RenewHeartbeat keeps a machine's own lease current. It creates the
// lease if the lease does not exist. It renews the lease once the
// lease has aged past HeartbeatRenewAfter. Otherwise, it leaves the
// lease alone, so most passes cost one read and no write. There is no
// election here. Each machine is the only writer of its own lease,
// the same way a kubelet is the only writer of its own node lease.
// Because of this, every failure mode only means "try again on the
// next pass."
func RenewHeartbeat(c *Client, name string, now time.Time) {
	path := heartbeatDir + "/" + name
	l := &lease{}
	err := c.RequestJSON(http.MethodGet, path, nil, l)
	if errors.Is(err, ErrNotFound) {
		// A lease is a struct of strings and ints. Marshaling it cannot fail.
		body, _ := json.Marshal(newLease(name, name, HeartbeatStaleAfter, now))
		if err := c.RequestJSON(http.MethodPost, heartbeatDir, body, nil); err != nil && !errors.Is(err, ErrConflict) {
			fmt.Printf("creating the heartbeat lease: %v\n", err)
		}
		return
	}
	if err != nil {
		fmt.Printf("reading the heartbeat lease: %v\n", err)
		return
	}
	if renewed, err := time.Parse(microTime, l.Spec.RenewTime); err == nil && now.Sub(renewed) < HeartbeatRenewAfter {
		return
	}
	l.Spec.HolderIdentity = name
	l.Spec.LeaseDurationSeconds = int(HeartbeatStaleAfter.Seconds())
	l.Spec.RenewTime = now.UTC().Format(microTime)
	// A lease is a struct of strings and ints. Marshaling it cannot fail.
	body, _ := json.Marshal(l)
	if err := c.RequestJSON(http.MethodPut, path, body, nil); err != nil {
		fmt.Printf("renewing the heartbeat lease: %v\n", err)
	}
}

// ListHeartbeats reads every machine's last renewal for the cluster
// operator's sweep. One cheap list request yields the fleet's
// liveness, mapped from each machine's name to the moment of its
// last renewal. A lease that is not some machine's heartbeat causes
// no harm in this map: the sweep looks up renewals by machine name
// and never iterates over the map, so a stray key can never be read
// as a machine.
func ListHeartbeats(c *Client) (map[string]time.Time, error) {
	leases, err := List[lease](c, heartbeatDir)
	if err != nil {
		return nil, err
	}
	renewals := map[string]time.Time{}
	for _, l := range leases {
		if renewed, err := time.Parse(microTime, l.Spec.RenewTime); err == nil {
			renewals[l.Metadata.Name] = renewed
		}
	}
	return renewals, nil
}
