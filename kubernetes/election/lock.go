package election

import (
	"context"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// renewalRecordingLock wraps the Lease lock. It records the time of
// this process's last accepted renewal for Lead.MayWrite, and Get
// treats the expired Lease of an earlier process of this pod as free.
//
// The time is taken before the request is sent, so it is never later
// than the time from which another candidate measures the Lease
// duration: the moment that candidate first reads the renewal.
type renewalRecordingLock struct {
	resourcelock.Interface

	mu      sync.Mutex
	renewed time.Time

	// pod is the pod name and an underscore, the prefix of every
	// identity of this pod. A pod name cannot contain an
	// underscore, so no other pod's identity has this prefix.
	pod string
}

func (r *renewalRecordingLock) Create(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	sent := time.Now()
	err := r.Interface.Create(ctx, record)
	r.recordRenewal(sent, record, err)
	return err
}

func (r *renewalRecordingLock) Update(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	sent := time.Now()
	err := r.Interface.Update(ctx, record)
	r.recordRenewal(sent, record, err)
	return err
}

// Get reads the Lease. When an earlier process of this pod holds it and
// its last renewal is at least one Lease duration old, Get returns the
// record with no holder, so client-go takes the Lease at once.
//
// Without this, a restarted process waits a whole Lease duration for
// its own expired Lease. client-go does not compare renewTime with the
// local clock, because the holder can run on another node. It starts to
// count the duration when it first reads the record. A restarted process
// reads the record for the first time when it starts, so it waits the
// full duration from its start, even when the Lease expired long
// before. When a fleet's only API server reboots, the leader cannot
// renew and exits, and nothing acts until that wait ends.
//
// This is safe only for this pod. The kubelet starts a container's new
// process after the old one has exited, so the old holder sends no more
// writes, and it wrote renewTime from this node's clock. A candidate in
// another pod still waits the full duration, as client-go documents for
// LeaderElectionConfig.LeaseDuration. It cannot tell an exited holder
// from a paused one, and a deleted pod does not prove its process
// stopped: a force delete or the out-of-service taint removes the pod
// from the API while the process can still run.
//
// client-go updates the Lease with the resourceVersion of this read.
// When another writer changes the Lease after this read, the update that
// takes the Lease fails with a conflict.
func (r *renewalRecordingLock) Get(ctx context.Context) (*resourcelock.LeaderElectionRecord, []byte, error) {
	record, raw, err := r.Interface.Get(ctx)
	if err == nil && r.abandonedByThisPod(record.HolderIdentity, record.RenewTime.Time,
		record.LeaseDurationSeconds, time.Now()) {
		free := *record
		free.HolderIdentity = ""
		return &free, raw, nil
	}
	return record, raw, err
}

// abandonedByThisPod reports whether holder is an earlier process of
// this pod and its last renewal, at renewed, is at least the Lease
// duration old at now.
func (r *renewalRecordingLock) abandonedByThisPod(holder string, renewed time.Time, seconds int, now time.Time) bool {
	if holder == r.Identity() || !strings.HasPrefix(holder, r.pod) {
		return false
	}
	return !now.Before(renewed.Add(time.Duration(seconds) * time.Second))
}

func (r *renewalRecordingLock) recordRenewal(sent time.Time, record resourcelock.LeaderElectionRecord, err error) {
	if err != nil || record.HolderIdentity != r.Identity() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renewed = sent
}

func (r *renewalRecordingLock) lastRenewal() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.renewed
}
