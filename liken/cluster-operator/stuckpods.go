package main

// This program logs an OS pod that is still Terminating after its
// deletion deadline. After this program evicts an out-of-date OS pod,
// the DaemonSet creates the replacement only when the kubelet has
// stopped the old pod, and decideRefresh does not evict a Terminating
// pod again. So when a kubelet never stops a pod, its machine stays on
// the old template, and without this line nothing in the log shows it.
//
// The API server sets deletionTimestamp to the time of the deletion
// plus the pod's grace period, so a pod still listed after that time
// has outlived its grace period.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/liken/kubernetes"
)

// overduePodReports records the UIDs of the overdue pods that a pass
// has logged, so each pod is logged once. A pass reads the pods of each OS
// DaemonSet, and settle then keeps only the UIDs that the pass found
// overdue, so a pod that is gone is forgotten.
type overduePodReports struct {
	// reported holds the UIDs the previous pass found overdue.
	reported map[string]bool
	// found collects the UIDs that the current pass finds overdue.
	found map[string]bool
}

// overdue returns the pods of one listing that are terminating past
// their deletion deadline and that an earlier pass did not report. It
// skips a pod whose deletionTimestamp does not parse, because it has no
// deadline to compare.
func (s *overduePodReports) overdue(pods []kubernetes.Pod, now time.Time) []kubernetes.Pod {
	if s.found == nil {
		s.found = map[string]bool{}
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
		s.found[p.Metadata.UID] = true
		if !s.reported[p.Metadata.UID] {
			report = append(report, p)
		}
	}
	return report
}

// settle ends a pass, so the next pass does not report the pods this
// one found overdue. When complete is false, the pass could not list
// the pods of some DaemonSet, so settle also keeps the earlier UIDs.
// Without them, the next pass would report those pods again.
func (s *overduePodReports) settle(complete bool) {
	if !complete {
		for uid := range s.reported {
			if s.found == nil {
				s.found = map[string]bool{}
			}
			s.found[uid] = true
		}
	}
	s.reported, s.found = s.found, nil
}

func (s *overduePodReports) reportOverdue(pods []kubernetes.Pod, now time.Time) {
	for _, p := range s.overdue(pods, now) {
		fmt.Printf("pod %s on %s is still terminating past its deletion deadline %s, "+
			"and the DaemonSet creates its replacement only after the kubelet on %s stops it\n",
			p.Metadata.Name, p.Spec.NodeName, p.Metadata.DeletionTimestamp, p.Spec.NodeName)
	}
}
