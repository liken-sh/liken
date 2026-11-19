package main

import "testing"

// Only a failed pod with a pod-level reason other than DeadlineExceeded,
// or with the condition DisruptionTarget, was ended by the kubelet. A
// container that exits with a status that is not zero leaves the reason
// empty.
func TestTheKubeletEndedAPod(t *testing.T) {
	disrupted := []podCondition{{Type: "DisruptionTarget", Status: "True"}}
	cases := []struct {
		name   string
		status podStatus
		want   bool
	}{
		{"an eviction", podStatus{Phase: podFailed, Reason: "Evicted", Conditions: disrupted}, true},
		{"a rejection at admission", podStatus{Phase: podFailed, Reason: "OutOfmemory"}, true},
		{"a node shutdown", podStatus{Phase: podFailed, Reason: "Terminated"}, true},
		{"a preemption", podStatus{Phase: podFailed, Conditions: disrupted}, true},
		{"a container that exited with 1", podStatus{Phase: podFailed}, false},
		{"the pod's own activeDeadlineSeconds", podStatus{Phase: podFailed, Reason: "DeadlineExceeded"}, false},
		{"a DisruptionTarget that is False", podStatus{Phase: podFailed, Conditions: []podCondition{{Type: "DisruptionTarget", Status: "False"}}}, false},
		{"a pod that succeeded", podStatus{Phase: podSucceeded, Reason: "Evicted"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (pod{Status: c.status}).endedByKubelet(); got != c.want {
				t.Errorf("endedByKubelet() = %v for the status %+v, want %v", got, c.status, c.want)
			}
		})
	}
}
