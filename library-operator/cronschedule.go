package main

// The operator starts a Library's full walk itself when the walk is due, so
// it reads spec.scan.schedule with the parser the Kubernetes CronJob
// controller uses, robfig/cron's standard parser. That keeps every schedule a
// person wrote for a CronJob valid, the descriptors such as @hourly and a
// CRON_TZ= or TZ= prefix included. A schedule with no prefix is in UTC.

import (
	"time"

	"github.com/robfig/cron/v3"
)

// parseScanSchedule reads one spec.scan.schedule. The error is the parser's
// own text, which the Ready condition carries word for word.
func parseScanSchedule(expression string) (cron.Schedule, error) {
	return cron.ParseStandard(expression)
}

// nextWalk is the first time in the schedule after the given time, in UTC.
// robfig's Next returns the zero time for a schedule that finds no time
// within five years, such as "0 0 30 2 *", and the caller reads the zero
// time as never.
func nextWalk(schedule cron.Schedule, after time.Time) time.Time {
	return schedule.Next(after.UTC())
}
