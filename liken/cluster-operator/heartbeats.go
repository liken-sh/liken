package main

// This file records when this program saw each machine's heartbeat
// change, so a sweep judges the heartbeat's age on one clock.
//
// A machine's operator writes the renewTime of its heartbeat Lease from
// the machine's own clock. A sweep that subtracted that time from this
// program's clock would add the difference between the two clocks to
// every heartbeat's age. A machine whose clock runs 45 seconds behind
// would read Lost while it renews every 8 seconds, and a machine whose
// clock runs ahead would read Lost late after it stopped. Clocks
// differ most at boot, before the first time synchronization, and on a
// machine with a wrong hardware clock and no time server.
//
// So this program reads renewTime only to learn that it changed. The
// Leases' watch calls the handler below for each write, and the handler
// records the moment on this program's clock. The node lifecycle
// controller in Kubernetes judges a kubelet's lease the same way
// (pkg/controller/nodelifecycle/node_lifecycle_controller.go).
//
// The record is a cache of what the watch delivered, so a program that
// starts records each Lease as seen at that moment. This program opens
// its watches only after it takes the lead, and exits when it loses
// the lead, so a new leader is always a new process with an empty
// record. A machine that is already gone then reads Lost
// HeartbeatStaleAfter after that, and until then it reads its last
// written status, which is usually Ready. So the record also keeps the
// time of the first sweep's read of the Leases, and the rollout grants
// no reboot turn until the record is HeartbeatStaleAfter older than
// that read (decideRollout). Without the hold, a new leader that
// started a minute after the old leader's machine lost power could
// count that dead machine as up and grant a second leader its turn.

import (
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// heartbeatSightings maps each Lease's name to the last renewTime this
// program saw, and when it first saw it. The watch's handler writes it
// from the informer's goroutine and a sweep reads it from the loop's
// goroutine, so a lock guards it. Its zero value is an empty record.
type heartbeatSightings struct {
	mu   sync.Mutex
	seen map[string]sighting

	// began is when the first sweep read the Leases, on this program's
	// clock. That read holds every Lease, so each Lease that existed
	// then has a sighting no later than began, and each Lease that
	// stayed unchanged since reads stale after began plus
	// HeartbeatStaleAfter. A Lease that appears later was written by a
	// machine that was up to write it.
	began time.Time
}

type sighting struct {
	// renewed is the Lease's renewTime, from the machine's clock. It is
	// compared only for equality.
	renewed time.Time
	// at is when this program first saw that renewTime, from its own
	// clock.
	at time.Time
}

// observe records that the Lease held renewed at now, and answers when
// this program first saw that renewTime. A renewTime that differs from
// the recorded one, earlier or later, is a renewal: a machine's clock
// can step back.
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

func (s *heartbeatSightings) forget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, name)
}

// heard observes each renewal of one read of the Leases, and maps each
// Lease's name to when this program saw its renewTime change. A sweep
// reads the Leases from the watch's copy or from the API server, and
// both reads pass through here. The handler has already recorded each
// change the watch delivered, at the moment it arrived, so this records
// only a change that a direct read finds first.
func (s *heartbeatSightings) heard(renewals map[string]time.Time, now time.Time) map[string]time.Time {
	s.mu.Lock()
	if s.began.IsZero() {
		s.began = now
	}
	s.mu.Unlock()
	heard := make(map[string]time.Time, len(renewals))
	for name, renewed := range renewals {
		heard[name] = s.observe(name, renewed, now)
	}
	return heard
}

// since answers when the first sweep read the Leases, or the zero time
// before any sweep has read them.
func (s *heartbeatSightings) since() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.began
}

// handler records each Lease the watch delivers. A Lease whose
// renewTime does not parse carries no liveness claim
// (kubernetes.Renewals), so it records nothing. A deleted Lease
// leaves the record, so a Lease created again later counts as seen
// when it arrives.
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
			// A tombstone can hold no copy of the Lease, but its key
			// always names it.
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
