package main

// The report of an OS pod that stays terminating.
//
// The steward evicts a stale OS pod and then waits: the DaemonSet
// creates the new pod only after the kubelet has stopped the old one.
// The steward does not evict a terminating pod again (steward.go), so
// a pod that the kubelet never finishes stopping would leave nothing
// in the log. That pod keeps its machine on the old template, and a
// person has to look at the kubelet on that machine. So the steward
// reports such a pod, once, when it is past its deletion deadline.
//
// The API server sets deletionTimestamp to the moment of the deletion
// plus the pod's grace period, so a pod that is still in the listing
// after that time has run past its grace period.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/liken/kubernetes"
)

// podSteward is what the steward remembers from one sweep to the
// next: the UIDs of the overdue pods it has reported. One sweep reads
// every OS DaemonSet's listing, and settle keeps only the UIDs that
// sweep found overdue, so a pod that leaves the listing leaves the
// memory with it.
type podSteward struct {
	reported map[string]bool
	held     map[string]bool
}

// overdue returns the pods of one listing that are terminating past
// their deletion deadline and that no earlier sweep reported. A pod
// whose deadline does not parse is left out, because there is no
// deadline to judge it by.
func (s *podSteward) overdue(pods []kubernetes.Pod, now time.Time) []kubernetes.Pod {
	if s.held == nil {
		s.held = map[string]bool{}
	}
	var report []kubernetes.Pod
	for _, p := range pods {
		if !p.Terminating() {
			continue
		}
		deadline, err := time.Parse(time.RFC3339, p.Metadata.DeletionTimestamp)
		if err != nil || !now.After(deadline) {
			continue
		}
		s.held[p.Metadata.UID] = true
		if !s.reported[p.Metadata.UID] {
			report = append(report, p)
		}
	}
	return report
}

// settle ends one sweep: the overdue pods it found are the ones the
// next sweep does not report again. A sweep that could not read every
// listing is not complete, and it keeps the earlier UIDs too: the pods
// of the listing it missed are still overdue, and a UID it dropped
// would be reported again.
func (s *podSteward) settle(complete bool) {
	if !complete {
		for uid := range s.reported {
			if s.held == nil {
				s.held = map[string]bool{}
			}
			s.held[uid] = true
		}
	}
	s.reported, s.held = s.held, nil
}

// reportOverdue prints one line for each pod that overdue returns.
func (s *podSteward) reportOverdue(pods []kubernetes.Pod, now time.Time) {
	for _, p := range s.overdue(pods, now) {
		fmt.Printf("pod %s on %s is still terminating past its deletion deadline %s; "+
			"the DaemonSet recreates it only after the kubelet on %s stops it\n",
			p.Metadata.Name, p.Spec.NodeName, p.Metadata.DeletionTimestamp, p.Spec.NodeName)
	}
}
