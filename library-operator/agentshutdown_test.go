package main

import "testing"

// Every pod that runs a catalog or progress agent gives the agent more
// time to exit than the agent itself waits for its tasks on SIGTERM, so
// the kubelet does not kill an agent that is still inside that wait.
func TestEachAgentPodOutwaitsTheAgentsOwnExit(t *testing.T) {
	cleanup := buildCleanupJob(studioMovies(), testScannerImage, testCorrosionImage).Spec.Template
	cases := []struct {
		name string
		pod  *Pod
	}{
		{name: "the Library's walk Job", pod: testScanPod(studioMovies())},
		{name: "the cleanup Job", pod: &Pod{Spec: cleanup.Spec}},
		{name: "the catalog pod", pod: testCatalogPod(housekeepingCatalog(), 0)},
		{name: "the progress pod", pod: testProgressPod(housekeepingCatalog(), 0)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			grace := one.pod.Spec.TerminationGracePeriodSeconds
			if grace == nil {
				t.Fatal("terminationGracePeriodSeconds is unset")
			}
			if *grace <= agentExitWait {
				t.Errorf("terminationGracePeriodSeconds = %d, want more than the agent's own %d s wait",
					*grace, agentExitWait)
			}
		})
	}
}
