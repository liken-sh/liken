package kubernetes

// The machine heartbeat protocol: how a machine proves it is alive,
// and how the fleet's observer reads that proof.
//
// A machine's status is only as current as the machine that wrote
// it. A dead machine can't report that it's dead; its last written
// status sits in the API reading Ready forever, which is worse than
// no status at all. Kubernetes has this exact problem with kubelets
// and solves it with heartbeats: the kubelet renews a lease every
// few seconds, and the node controller turns a silent lease into a
// NotReady Node. liken's machines get the same treatment: each
// machine's operator renews a coordination.k8s.io Lease named for
// its machine, and the cluster operator lists those leases to judge
// the fleet's liveness.
//
// The mechanism is kube-node-lease's, adopted for the same reasons:
// a heartbeat must renew on a schedule forever, so it should be the
// cheapest write the API server offers, and a Lease is a few dozen
// bytes with no watchers. A timestamp inside Machine status would
// instead rewrite the whole object (hardware inventory, boot record,
// conditions) and wake every watcher on every renewal of every
// machine. Kubernetes moved the kubelet's heartbeats out of Node
// status and into kube-node-lease to escape exactly that; liken
// heartbeats through a lease from the start. The leases live in
// liken-system rather than a copy of kube-node-lease's dedicated
// namespace so that everything liken coordinates through sits in
// the one namespace the OS owns: `kubectl get leases -n
// liken-system` is the fleet's whole liveness surface.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/liken-sh/liken/api"
)

const heartbeatDir = "/apis/coordination.k8s.io/v1/namespaces/liken-system/leases"

// HeartbeatRenewAfter is how old the heartbeat must be before the
// machine's own operator renews it: just under the ten-second
// reconcile ticker, so every ticker pass renews but the event-driven
// passes in between get by on a read. HeartbeatStaleAfter is how
// long a machine may then go silent before the cluster operator
// declares it Lost. A single missed renewal may just mean a busy
// moment; several missed renewals mean the machine is down.
//
// The numbers are kube-node-lease's: the kubelet renews its lease
// every ten seconds, and the node controller gives a silent kubelet
// forty before its Node goes NotReady. A dead machine silences both
// leases at the same moment, so matching the thresholds means both
// verdicts land together, and `kubectl get nodes` never spends a
// minute contradicting `kubectl get machines` about a machine that
// just died.
const (
	HeartbeatRenewAfter = 8 * time.Second
	HeartbeatStaleAfter = 40 * time.Second
)

// microTime is the layout coordination.k8s.io uses for its
// timestamps (metav1.MicroTime): RFC 3339 with microseconds, a finer
// grain than most of the API because leases exist to compare
// closely-spaced instants.
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

// newLease is a fresh claim: held by holder as of now.
func newLease(name, holder string, duration time.Duration, now time.Time) *lease {
	l := &lease{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	l.Metadata.Name = name
	l.Spec.HolderIdentity = holder
	l.Spec.LeaseDurationSeconds = int(duration.Seconds())
	l.Spec.AcquireTime = now.UTC().Format(microTime)
	l.Spec.RenewTime = l.Spec.AcquireTime
	return l
}

// RenewHeartbeat keeps a machine's own lease fresh: create it if it
// doesn't exist, renew it once it has aged past HeartbeatRenewAfter,
// and leave it alone otherwise, so most passes cost one read and no
// write. There is no election here: each machine is its lease's only
// writer, the way a kubelet is the only writer of its node lease.
// Every failure mode is therefore just "try again next pass."
func RenewHeartbeat(c *Client, name string, now time.Time) {
	path := heartbeatDir + "/" + name
	l := &lease{}
	err := c.RequestJSON(http.MethodGet, path, nil, l)
	if errors.Is(err, ErrNotFound) {
		// A lease is a struct of strings and ints; marshaling cannot fail.
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
	// A lease is a struct of strings and ints; marshaling cannot fail.
	body, _ := json.Marshal(l)
	if err := c.RequestJSON(http.MethodPut, path, body, nil); err != nil {
		fmt.Printf("renewing the heartbeat lease: %v\n", err)
	}
}

// ListHeartbeats reads every machine's last renewal for the cluster
// operator's sweep: one cheap list yields the fleet's liveness,
// mapping each machine's name to the moment of its last renewal. A
// lease that isn't some machine's heartbeat is harmless in the map:
// the sweep looks renewals up by machine name and never iterates
// them, so a stray key can never read as a machine.
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
