package main

// The watch delivers a created Job some time after the API server
// accepts it. These tests run a pass in between, against a store that
// does not have the Job yet.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// A pass before the watch delivers the Job finds it in p.jobs, the memo
// of the Jobs that the loop created, so the Job is created once.
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
		putTarget(t, api, m43())
		settle()
		api.catchUp(jobsPath)
		settle()
		want := []string{"POST /apis/batch/v1/namespaces/observatory/jobs"}
		if got := api.writes(jobsPath); !slices.Equal(got, want) {
			t.Errorf("Job writes %q, want %q", got, want)
		}
	})
}

// When the store does not have the Job yet, a pass reads it from the
// API server. If the API server refuses that read, the pass fails and
// p.jobs notes the Job as unknown, so the next pass reads it again and
// the loop still creates the Job once.
func TestARefusedReadOfAPromotionJobDoesNotCreateItAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		startOperator(t, api)
		settle()
		api.lag(jobsPath)
		putTarget(t, api, withPending(m42(), 1, 0))
		settle()
		api.refuseReads(true)
		putTarget(t, api, m43())
		settle()
		api.refuseReads(false)
		time.Sleep(lastRetry)
		settle()
		api.catchUp(jobsPath)
		settle()
		want := []string{"POST /apis/batch/v1/namespaces/observatory/jobs"}
		if got := api.writes(jobsPath); !slices.Equal(got, want) {
			t.Errorf("Job writes %q, want %q", got, want)
		}
	})
}
