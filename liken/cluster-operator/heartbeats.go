package main

// A heartbeat's age is measured on this program's clock, not on the
// machine's clock.
//
// A machine's operator writes the renewTime of its heartbeat Lease from
// the machine's clock. If a pass subtracted renewTime from this
// program's clock, the difference between the clocks would become part
// of every age. A machine whose clock is 45 seconds behind would read
// Lost while it renews every 8 seconds, and a machine whose clock is
// ahead would read Lost late after it stopped. Clocks differ most at
// boot, before time synchronization, and on a machine with a wrong
// hardware clock and no time server. So this program uses renewTime
// only to detect a change, and records when it saw each change on its
// own clock. The node lifecycle controller in Kubernetes treats a
// kubelet's Lease the same way
// (pkg/controller/nodelifecycle/node_lifecycle_controller.go).
//
// The record is in memory. This program opens its watches only after it
// wins the election and exits when it loses it, so a new leader starts
// with an empty record and counts each Lease as changed when it first
// reads it. A machine that went down before then keeps the phase in its
// status until HeartbeatStaleAfter has passed. So decideRollout grants
// no reboot turn until HeartbeatStaleAfter has passed since the first
// read of the Leases. Without that wait, a new leader could count a
// machine that lost power a minute earlier as up, and grant a second
// machine a reboot turn.

import (
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// heartbeatSightings records, for each Lease, the last renewTime this
// program saw and when it first saw it. The watch handler and the pass
// run on different goroutines, so a mutex guards it.
type heartbeatSightings struct {
	mu   sync.Mutex
	seen map[string]sighting

	// firstRead is when the first pass read the Leases. Every Lease
	// that existed then has a sighting no later than firstRead.
	firstRead time.Time
}

type sighting struct {
	// renewed is the Lease's renewTime, on the machine's clock. It is
	// compared only for equality.
	renewed time.Time
	// at is when this program first saw that renewTime.
	at time.Time
}

// observe records that the named Lease had renewTime renewed at now,
// and returns when this program first saw that renewTime. Any renewTime
// that differs from the recorded one counts as a renewal, including an
// earlier one, because a machine's clock can step back.
func (s *heartbeatSightings) observe(name string, renewed, now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.seen[name]; ok && last.renewed.Equal(renewed) {
		return last.at
	}
	if s.seen == nil {
		s.seen = map[string]sighting{}
	}
	s.seen[name] = sighting{renewed: renewed, at: now}
	return now
}

// forget removes a Lease's record, so a Lease created again with the
// same name and renewTime counts as changed when it arrives.
func (s *heartbeatSightings) forget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, name)
}

// changeTimes records the renewTimes of one read of the Leases, from the watch
// cache or from the API server, and returns when this program saw each
// one change. The watch handler has already recorded each change that
// the watch received, so this records only a change that the read finds
// first. The first call sets firstRead.
func (s *heartbeatSightings) changeTimes(renewals map[string]time.Time, now time.Time) map[string]time.Time {
	s.mu.Lock()
	if s.firstRead.IsZero() {
		s.firstRead = now
	}
	s.mu.Unlock()
	changedAt := make(map[string]time.Time, len(renewals))
	for name, renewed := range renewals {
		changedAt[name] = s.observe(name, renewed, now)
	}
	return changedAt
}

// firstReadTime returns when the first pass read the Leases, or the zero time
// before any pass has read them.
func (s *heartbeatSightings) firstReadTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstRead
}

// handler returns the watch handler that records each Lease the watch
// receives. kubernetes.Renewals leaves out a Lease whose renewTime does
// not parse, so that Lease is not recorded.
func (s *heartbeatSightings) handler(source informer.Source) cache.ResourceEventHandler {
	seen := func(object any) {
		lease, err := informer.Convert[kubernetes.Lease](object)
		if err != nil {
			informer.Report(source.String(), err)
			return
		}
		now := time.Now()
		for name, renewed := range kubernetes.Renewals([]kubernetes.Lease{lease}) {
			s.observe(name, renewed, now)
		}
	}
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(object any) { seen(object) },
		UpdateFunc: func(_, object any) { seen(object) },
		DeleteFunc: func(object any) {
			// A deletion tombstone may not contain the Lease, but its
			// key names it.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(object)
			if err != nil {
				informer.Report(source.String(), err)
				return
			}
			if _, name, err := cache.SplitMetaNamespaceKey(key); err == nil {
				s.forget(name)
			}
		},
	}
}
