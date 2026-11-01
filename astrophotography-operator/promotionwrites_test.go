package main

// The promotion loop's creates of a Job while the watch of the Jobs
// runs behind the API server. A watch delivers each create some time
// after the API server answers it, and every pass that runs in that
// time reads a store that does not hold the Job yet.

import (
	"slices"
	"testing"
	"testing/synctest"
)

// A pass that runs before the watch delivers the operator's own create
// of a promotion Job lists that Job from the memo of the operator's
// writes, so it creates the Job once.
func TestAPassDoesNotCreateAPromotionJobTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		startOperator(t, api)
		settle()
		api.lag(jobsPath)
		putTarget(t, api, withPending(m42(), 1, 0))
		settle()
		// A change elsewhere wakes one more pass while the watch lags.
		other := m42()
		other.Metadata.Name, other.Metadata.UID = "m43", "uid-m43"
		putTarget(t, api, other)
		settle()
		api.catchUp(jobsPath)
		settle()
		want := []string{"POST /apis/batch/v1/namespaces/observatory/jobs"}
		if got := api.writes(jobsPath); !slices.Equal(got, want) {
			t.Errorf("Job writes %q, want %q", got, want)
		}
	})
}
