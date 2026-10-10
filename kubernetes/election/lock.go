package election

import (
	"context"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// renewalClock is the Lease lock with two additions. It records when
// this process last sent a write of the Lease that the API server
// accepted with this process as the holder, for the write guard. The
// time is taken before the request is sent, so it is never later than
// the time a waiting copy first reads the renewal and measures the
// duration from. And it frees the expired Lease of an earlier process
// of this pod (Get).
type renewalClock struct {
	resourcelock.Interface

	mu      sync.Mutex
	renewed time.Time

	// pod is the prefix that every identity of this pod starts with.
	// A pod's name is a DNS subdomain, which has no underscore, so the
	// prefix names this pod and no other.
	pod string
}

func (r *renewalClock) Create(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	sent := time.Now()
	err := r.Interface.Create(ctx, record)
	r.mark(sent, record, err)
	return err
}

func (r *renewalClock) Update(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	sent := time.Now()
	err := r.Interface.Update(ctx, record)
	r.mark(sent, record, err)
	return err
}

// Get reads the Lease, and shows client-go a Lease with no holder when
// an earlier process of this pod held it and its last renewal is one
// Lease duration old.
//
// client-go does not compare renewTime with its own clock, because the
// holder can run on another node with another clock. It measures the
// duration from the time it first read the current record. A process
// that restarts, or that starts after the API server comes back, first
// reads its earlier process's Lease late, and waits a whole duration
// from that read. When the only API server reboots, the leader cannot
// renew, exits, and restarts, and then nothing acts for a whole Lease
// duration after the API server returns. A rolling update in that time
// moves the wait to the new pod, which cannot clear a Lease that names
// another process.
//
// An earlier process of this pod is not a paused leader that can
// resume. The kubelet starts a container's process again only after
// the one before it ended, so that process sends no more writes. It
// wrote renewTime from the clock of this node, so the comparison with
// this process's clock is sound.
//
// A copy in another pod still waits the whole duration, and this
// package accepts that wait. client-go documents it:
// LeaderElectionConfig.LeaseDuration is how long a candidate that does
// not lead waits before it forces the take, measured from the last
// renewal it observed. Another pod cannot tell a holder that ended from
// one that paused. A missing pod does not prove that the holder ended:
// a force delete, or the out-of-service taint on its node, removes the
// pod from the API while its process can still run. The wait happens
// at most once for each restart of the only API server of a fleet.
//
// The Lease that client-go then updates keeps the resourceVersion of
// this read, so the take is a conditional write, and a conflict with
// any other writer ends it.
func (r *renewalClock) Get(ctx context.Context) (*resourcelock.LeaderElectionRecord, []byte, error) {
	record, raw, err := r.Interface.Get(ctx)
	if err == nil && r.abandonedByThisPod(record.HolderIdentity, record.RenewTime.Time,
		record.LeaseDurationSeconds, time.Now()) {
		free := *record
		free.HolderIdentity = ""
		return &free, raw, nil
	}
	return record, raw, err
}

// abandonedByThisPod answers whether holder is an earlier process of
// this pod, whose last renewal at renewed is at least the Lease's
// duration old at now.
func (r *renewalClock) abandonedByThisPod(holder string, renewed time.Time, seconds int, now time.Time) bool {
	if holder == r.Identity() || !strings.HasPrefix(holder, r.pod) {
		return false
	}
	return !now.Before(renewed.Add(time.Duration(seconds) * time.Second))
}

func (r *renewalClock) mark(sent time.Time, record resourcelock.LeaderElectionRecord, err error) {
	if err != nil || record.HolderIdentity != r.Identity() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renewed = sent
}

func (r *renewalClock) lastRenewal() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.renewed
}
