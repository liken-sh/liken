package main

// what these tests read: the enricher Job mounts the cache of an imdb
// provider in the nfo container alone, and only when the provider holds a
// claim.

import (
	"slices"
	"testing"
)

// an imdb provider as a pass would have checked it, with its Cached verdict.
func readyIMDbProvider(cached ConditionStatus) *MetadataProvider {
	provider := seedIMDbProvider(newFakeCluster(), "imdb")
	provider.Status.Conditions = []Condition{
		{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable},
		{Type: conditionCached, Status: cached},
	}
	return provider
}

func TestTheNFOContainerAloneMountsTheCache(t *testing.T) {
	cases := []struct {
		name    string
		cached  ConditionStatus
		holders []string
		volumes []string
	}{
		{name: "the provider holds a claim", cached: ConditionTrue,
			holders: []string{nfoContainerName}, volumes: []string{"imdb-datasets"}},
		{name: "the cluster has no per-node class", cached: ConditionFalse},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			job := testEnrichJob(studioMovies(), "", readyIMDbProvider(one.cached))

			spec := job.Spec.Template.Spec
			var holders, volumes []string
			for _, container := range append(spec.InitContainers, spec.Containers...) {
				for _, mount := range container.VolumeMounts {
					if mount.Name == datasetsVolumeName {
						holders = append(holders, container.Name)
					}
				}
			}
			for _, held := range spec.Volumes {
				if held.Name == datasetsVolumeName {
					volumes = append(volumes, held.PersistentVolumeClaim.ClaimName)
				}
			}
			if !slices.Equal(holders, one.holders) || !slices.Equal(volumes, one.volumes) {
				t.Errorf("mounts = %v on %v, want %v on %v", holders, volumes, one.holders, one.volumes)
			}
		})
	}
}
