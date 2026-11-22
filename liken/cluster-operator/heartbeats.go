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
// with an empty record. The first renewTime it reads for a Lease shows
// only that the machine renewed at some time in the past. A machine
// that went down a minute earlier still has a Lease and a status that
// reads Ready.
//
// So a machine counts as heard only after this program sees its
// renewTime change from a value it read before: the machine wrote the
// Lease after this program started reading. Until HeartbeatStaleAfter
// has passed since the first read of the Leases, decideRollout counts
// each machine that it has not heard as out of service, grants it no
// turn, and neither confirms nor reclaims its turn. After that time, a
// machine that has not renewed reads Lost, and the record needs no
// such rule. A live machine renews every few seconds, so a new process
// hears every live machine long before HeartbeatStaleAfter, and a
// process that restarts more often than that still finishes turns and
// grants new ones. A machine that is down is never heard, so it never
// counts as up, however often this program restarts.

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
	// renewal is true when this program saw renewTime change from an
	// earlier value that it had read, so the machine wrote the Lease
	// while this program was reading it.
	renewal bool
}

// observe records that the named Lease had renewTime renewed at now,
// and returns when this program first saw that renewTime. Any renewTime
// that differs from the recorded one counts as a renewal, including an
// earlier one, because a machine's clock can step back. heard is true
// when this call is the first renewal that this program saw for the
// Lease.
func (s *heartbeatSightings) observe(name string, renewed, now time.Time) (at time.Time, heard bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.seen[name]
	if ok && last.renewed.Equal(renewed) {
		return last.at, false
	}
	if s.seen == nil {
		s.seen = map[string]sighting{}
	}
	s.seen[name] = sighting{renewed: renewed, at: now, renewal: ok}
	return now, ok && !last.renewal
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
		changedAt[name], _ = s.observe(name, renewed, now)
	}
	return changedAt
}

// trust returns what a pass may believe about the heartbeats: when the
// first pass read the Leases, and which Leases this program has seen
// renewed.
func (s *heartbeatSightings) trust() heartbeatTrust {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := heartbeatTrust{firstRead: s.firstRead, heard: map[string]bool{}}
	for name, seen := range s.seen {
		if seen.renewal {
			t.heard[name] = true
		}
	}
	return t
}

// heartbeatTrust is the part of the heartbeat record that decides which
// machines' statuses a pass may believe. The zero value believes every
// machine, as a process that has read the Leases for longer than
// HeartbeatStaleAfter does.
type heartbeatTrust struct {
	// firstRead is when the first pass read the Leases, or the zero
	// time.
	firstRead time.Time
	// heard names each Lease that this program has seen renewed.
	heard map[string]bool
}

// trustedAt is when every machine's heartbeat becomes trustworthy: a
// machine that has not renewed by then reads Lost.
func (t heartbeatTrust) trustedAt() time.Time {
	return t.firstRead.Add(kubernetes.HeartbeatStaleAfter)
}

// young reports whether the record is younger than HeartbeatStaleAfter,
// so a machine that is down may still read its last status.
func (t heartbeatTrust) young(now time.Time) bool {
	return !t.firstRead.IsZero() && !now.After(t.trustedAt())
}

// unheard reports whether a pass at now must treat the named machine as
// possibly down whatever its status says: the record is young and this
// program has not seen the machine renew its Lease.
func (t heartbeatTrust) unheard(name string, now time.Time) bool {
	return t.young(now) && !t.heard[name]
}

// handler returns the watch handler that records each Lease the watch
// receives. kubernetes.Renewals leaves out a Lease whose renewTime does
// not parse, so that Lease is not recorded. The first renewal of each
// Lease calls wake, because it can let a young record grant a turn or
// end one. Later renewals start nothing, because the ticker measures
// heartbeat ages.
func (s *heartbeatSightings) handler(source informer.Source, wake func()) cache.ResourceEventHandler {
	seen := func(object any) {
		lease, err := informer.Convert[kubernetes.Lease](object)
		if err != nil {
			informer.Report(source.String(), err)
			return
		}
		now := time.Now()
		for name, renewed := range kubernetes.Renewals([]kubernetes.Lease{lease}) {
			if _, heard := s.observe(name, renewed, now); heard {
				wake()
			}
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
