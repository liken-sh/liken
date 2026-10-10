package main

// A provider's Secret can go away between two check calls, which are an
// hour apart while the provider is Reachable. A Job that names the
// Secret in a secretKeyRef then never starts, and it holds every other
// Job of its Library until its deadline. So the pass that creates a Job
// reads each Secret the Job would name first.

import (
	"net/http"
	"testing"
)

// secretsNamed answers the Secret of each secretKeyRef in a Job's pod.
func secretsNamed(job Job) []string {
	var names []string
	spec := job.Spec.Template.Spec
	for _, container := range append(spec.InitContainers, spec.Containers...) {
		for _, env := range container.Env {
			if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
				names = append(names, env.ValueFrom.SecretKeyRef.Name)
			}
		}
	}
	return names
}

func TestAJobNamesNoSecretThatIsGone(t *testing.T) {
	cases := []struct {
		name   string
		secret *Secret
		broken bool
		named  bool
		reason string
	}{
		{name: "the Secret is there", secret: tmdbSecret("token", "the-key"), named: true, reason: reasonReachable},
		{name: "the Secret is gone", reason: reasonNoSecret},
		{name: "the Secret lost its key", secret: tmdbSecret("other", "the-key"), reason: reasonNoSecret},
		// A read that fails says nothing about the Secret, so the Job
		// still names it.
		{name: "the read fails", broken: true, named: true, reason: reasonReachable},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			library.Spec.Sources = []string{"tmdb"}
			provider := seedProvider(cluster, "tmdb", "house", factIdentity)
			provider.Status.Conditions = []Condition{
				{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable},
			}
			if one.secret != nil {
				cluster.secrets["tmdb-key"] = one.secret
			}
			if one.broken {
				cluster.broken[secretPath("house", "tmdb-key")] = http.StatusInternalServerError
			}
			operator := testOperator(t, cluster)
			providers := providerSet{libraryKey("house", "tmdb"): provider}

			if err := operator.runLibrary(t.Context(), library, nil, nil, providers, testNow); err != nil {
				t.Fatal(err)
			}

			created := cluster.heldJobs()
			if len(created) != 1 {
				t.Fatalf("jobs = %d, want one walk", len(created))
			}
			if named := len(secretsNamed(created[0])) > 0; named != one.named {
				t.Errorf("the Job names %v, want a Secret named: %v", secretsNamed(created[0]), one.named)
			}
			ready := conditionNamed(cluster.heldProvider("tmdb").Status.Conditions, conditionReady)
			if ready.Reason != one.reason {
				t.Errorf("Ready = %s %s, want %s", ready.Status, ready.Reason, one.reason)
			}
		})
	}
}
