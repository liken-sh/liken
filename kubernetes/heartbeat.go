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
// All objects that liken coordinates through this API use the
// namespace that the OS owns. The command `kubectl get leases -n
// liken-system` shows the fleet's complete liveness state.

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
// machine's own operator renews it. The value is just under the
// ten-second reconcile ticker, so every ticker pass renews the
// lease, and the event-driven passes in between send nothing.
// HeartbeatStaleAfter sets how long a machine may then stay silent
// before the cluster operator marks it Lost. A single missed
// renewal may only mean a busy moment. Several missed renewals mean
// the machine is down.
//
// These numbers come from kube-node-lease. The kubelet renews its
// lease every ten seconds, and the node controller waits forty
// seconds for a silent kubelet before its Node goes NotReady. A dead
// machine stops renewing both leases at the same moment, so matching
// the two thresholds means both systems report the loss together. This way,
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

// Lease is the part of a coordination.k8s.io Lease that the heartbeat
// writes and the cluster operator reads.
type Lease struct {
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
func newLease(name, holder string, duration time.Duration, now time.Time) *Lease {
	l := &Lease{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	l.Metadata.Name = name
	l.Spec.HolderIdentity = holder
	l.Spec.LeaseDurationSeconds = int(duration.Seconds())
	l.Spec.AcquireTime = now.UTC().Format(microTime)
	l.Spec.RenewTime = l.Spec.AcquireTime
	return l
}

// Heartbeat keeps a machine's own lease current. Each machine is the
// only writer of its own lease, the same way a kubelet is the only
// writer of its own node lease, so there is no election here, and every
// failure only means "try again on the next pass."
//
// A Heartbeat holds the lease as this process last wrote it. The
// resourceVersion in that copy is what a renewal needs, so a steady
// renewal is one update and no read. The machine's operator creates one
// Heartbeat for the life of the process, the same way it keeps one
// release fetcher.
type Heartbeat struct {
	name string
	held *Lease
}

// NewHeartbeat returns the heartbeat of the named machine. It holds no
// lease until its first renewal reads one.
func NewHeartbeat(name string) *Heartbeat {
	return &Heartbeat{name: name}
}

// Renew renews the lease once it has aged past HeartbeatRenewAfter,
// and creates it when it does not exist. A pass that finds the held
// copy fresh sends nothing, so the event-driven passes between two
// ticker passes cost no request.
//
// The first renewal reads the lease, because the process has no copy
// yet. After that, the renewal writes from the copy it holds. The
// update carries the copy's resourceVersion, so a lease that something
// else wrote since then makes the API server answer 409 Conflict, and
// a lease that somebody deleted answers 404. Either answer drops the
// copy, and the same call reads the lease again and renews from what
// it read.
func (h *Heartbeat) Renew(c *Client, now time.Time) {
	path := heartbeatDir + "/" + h.name
	if h.held == nil && !h.read(c, now) {
		return
	}
	if renewed, err := time.Parse(microTime, h.held.Spec.RenewTime); err == nil && now.Sub(renewed) < HeartbeatRenewAfter {
		return
	}
	err := h.write(c, path, now)
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		h.held = nil
		if !h.read(c, now) {
			return
		}
		err = h.write(c, path, now)
	}
	if err != nil {
		h.held = nil
		fmt.Printf("renewing the heartbeat lease: %v\n", err)
	}
}

// read reads the lease into the held copy, or creates the lease when
// it does not exist. It answers false when the caller has nothing left
// to do: the read failed, or the create already renewed the lease.
func (h *Heartbeat) read(c *Client, now time.Time) bool {
	l := &Lease{}
	err := c.RequestJSON(http.MethodGet, heartbeatDir+"/"+h.name, nil, l)
	if errors.Is(err, ErrNotFound) {
		// A lease is a struct of strings and ints. Marshaling it cannot fail.
		body, _ := json.Marshal(newLease(h.name, h.name, HeartbeatStaleAfter, now))
		created := &Lease{}
		if err := c.RequestJSON(http.MethodPost, heartbeatDir, body, created); err != nil {
			if !errors.Is(err, ErrConflict) {
				fmt.Printf("creating the heartbeat lease: %v\n", err)
			}
			return false
		}
		h.held = created
		return false
	}
	if err != nil {
		fmt.Printf("reading the heartbeat lease: %v\n", err)
		return false
	}
	h.held = l
	return true
}

// write sends the renewal from the held copy, and keeps the lease the
// API server answers with, which carries the new resourceVersion.
func (h *Heartbeat) write(c *Client, path string, now time.Time) error {
	renewal := *h.held
	renewal.Spec.HolderIdentity = h.name
	renewal.Spec.LeaseDurationSeconds = int(HeartbeatStaleAfter.Seconds())
	renewal.Spec.RenewTime = now.UTC().Format(microTime)
	// A lease is a struct of strings and ints. Marshaling it cannot fail.
	body, _ := json.Marshal(&renewal)
	written := &Lease{}
	if err := c.RequestJSON(http.MethodPut, path, body, written); err != nil {
		return err
	}
	h.held = written
	return nil
}

// ListHeartbeats reads every machine's last renewal for the cluster
// operator's sweep. One cheap list request yields the fleet's
// liveness. Renewals says what the answer holds.
func ListHeartbeats(c *Client) (map[string]time.Time, error) {
	leases, err := List[Lease](c, heartbeatDir)
	if err != nil {
		return nil, err
	}
	return Renewals(leases), nil
}

// Renewals maps each lease's name to the moment of its last renewal.
// A lease that is not some machine's heartbeat, such as the cluster
// operator's own leader election Lease, causes no harm in this map:
// the sweep looks up renewals by machine name and never iterates over
// the map, so a stray key can never be read as a machine. A lease
// whose renewal does not parse carries no liveness claim, and it is
// left out.
func Renewals(leases []Lease) map[string]time.Time {
	renewals := map[string]time.Time{}
	for _, l := range leases {
		if renewed, err := time.Parse(microTime, l.Spec.RenewTime); err == nil {
			renewals[l.Metadata.Name] = renewed
		}
	}
	return renewals
}
