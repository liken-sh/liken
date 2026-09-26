package main

// These tests read the lines a Catalog's Jellyfin backfill leaves: the Job
// the pass creates, and its end, once each.

import (
	"testing"
)

func TestTheBackfillLeavesOneLinePerStep(t *testing.T) {
	cases := []struct {
		name   string
		job    *JobStatus
		status *CatalogJellyfinStatus
		want   string
	}{
		{
			name: "the Job the pass creates",
			want: "catalog house/house: created the job house-jellyfin-backfill " +
				"to copy every person's progress from the jellyfin server spec.jellyfin names",
		},
		{
			name:   "the Job that succeeded",
			job:    &JobStatus{Succeeded: 1},
			status: &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillRunning},
			want:   "catalog house/house: the job house-jellyfin-backfill finished the jellyfin backfill",
		},
		{
			name:   "the Job that gave up",
			job:    &JobStatus{Failed: scanBackoffLimit + 1},
			status: &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillRunning},
			want: "catalog house/house: the job house-jellyfin-backfill failed the jellyfin backfill " +
				"after 3 attempts, and a later pass creates it again",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			catalog := jellyfinCatalog()
			catalog.Status.Jellyfin = c.status
			var jobs []Job
			if c.job != nil {
				jobs = []Job{*backfillJobWith(catalog, *c.job)}
			}
			operator, logged := loggingOperator(t, newFakeCluster())

			operator.standJellyfinBackfill(t.Context(), catalog, jobs, []*Pod{readyProgressPod(catalog)}, testNow)

			wantOneLine(t, logged, c.want)
		})
	}
}

// A backfill the status already calls finished leaves no second line on the
// passes before the TTL takes its Job.
func TestAFinishedBackfillAddsNoLine(t *testing.T) {
	catalog := jellyfinCatalog()
	catalog.Status.Jellyfin = &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFinished,
		Backfilled: testBackfilled}
	jobs := []Job{*backfillJobWith(catalog, JobStatus{Succeeded: 1})}
	operator, logged := loggingOperator(t, newFakeCluster())

	operator.standJellyfinBackfill(t.Context(), catalog, jobs, []*Pod{readyProgressPod(catalog)}, testNow)

	if logged.Len() != 0 {
		t.Errorf("logged %q, want nothing", logged)
	}
}
