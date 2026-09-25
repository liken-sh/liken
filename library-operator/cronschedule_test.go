package main

// The schedule rules this repository adds on top of the parser: when a walk
// is due, and that a schedule that never fires is never due.

import (
	"testing"
	"time"
)

// A walk is due once a time in the schedule has passed since the last walk
// started, and never for a schedule that finds no time or does not parse.
func TestAWalkIsDueOnItsSchedule(t *testing.T) {
	last := time.Date(2026, 9, 25, 10, 5, 0, 0, time.UTC)
	cases := []struct {
		name     string
		schedule string
		now      time.Time
		want     bool
	}{
		{name: "inside the hour", schedule: "0 * * * *", now: last.Add(50 * time.Minute)},
		{name: "past the hour", schedule: "0 * * * *", now: last.Add(55 * time.Minute), want: true},
		{name: "a descriptor", schedule: "@daily", now: last.Add(14 * time.Hour), want: true},
		{name: "a zone prefix", schedule: "CRON_TZ=America/New_York 0 7 * * *", now: last.Add(time.Hour), want: true},
		{name: "a day that never comes", schedule: "0 0 30 2 *", now: last.Add(24 * 365 * time.Hour)},
		{name: "a schedule that does not parse", schedule: "every hour", now: last.Add(24 * time.Hour)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Scan.Schedule = one.schedule

			if got := walkDue(library, last, one.now); got != one.want {
				t.Errorf("walkDue = %v, want %v", got, one.want)
			}
		})
	}
}
