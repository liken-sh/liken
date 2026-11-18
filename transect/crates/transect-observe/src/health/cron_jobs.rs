//! The `CronJob` rule: report a `CronJob` whose last run did not
//! succeed.
//!
//! transect does not watch `Job`s, so the rule reads the `CronJob`'s
//! status. The controller sets `lastScheduleTime` when it starts a run
//! and `lastSuccessfulTime` when a run succeeds. When no run is active,
//! a `lastSuccessfulTime` that is missing or older than
//! `lastScheduleTime` means the last run failed.

use k8s_openapi::api::batch::v1::CronJob;
use kube::ResourceExt;

use super::{Kind, Reason, Workload};

/// Whether the `CronJob`'s last run succeeded and no run is active.
pub fn settled(cron_job: &CronJob) -> bool {
    let Some(status) = cron_job.status.as_ref() else {
        return false;
    };
    let active = status
        .active
        .as_ref()
        .is_some_and(|active| !active.is_empty());
    let scheduled = status.last_schedule_time.as_ref().map(|time| time.0);
    let succeeded = status.last_successful_time.as_ref().map(|time| time.0);
    !active && succeeded.is_some() && succeeded >= scheduled
}

pub fn last_run(cron_job: &CronJob) -> Option<Reason> {
    let status = cron_job.status.as_ref()?;
    if status
        .active
        .as_ref()
        .is_some_and(|active| !active.is_empty())
    {
        return None;
    }
    let scheduled = status.last_schedule_time.as_ref()?.0;
    let succeeded = status.last_successful_time.as_ref().map(|time| time.0);
    if succeeded.is_some_and(|succeeded| succeeded >= scheduled) {
        return None;
    }
    Some(Reason::LastRunFailed {
        workload: Workload {
            kind: Kind::CronJob,
            name: cron_job.name_any(),
        },
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::*;
    use super::*;

    #[rstest]
    #[case::never_ran(None, None, 0, false)]
    #[case::last_run_succeeded(Some(60), Some(58), 0, true)]
    #[case::last_run_failed(Some(60), Some(1500), 0, false)]
    #[case::running_now(Some(1), Some(58), 1, false)]
    fn is_settled_only_when_the_last_run_succeeded_and_none_is_active(
        #[case] scheduled_minutes_ago: Option<i64>,
        #[case] succeeded_minutes_ago: Option<i64>,
        #[case] active: usize,
        #[case] expected: bool,
    ) {
        let report = cron_job(
            scheduled_minutes_ago.map(minutes_before),
            succeeded_minutes_ago.map(minutes_before),
            active,
        );
        assert_eq!(settled(&report), expected);
    }

    #[rstest]
    #[case::never_ran(None, None, 0, false)]
    #[case::last_run_succeeded(Some(60), Some(58), 0, false)]
    #[case::last_run_failed(Some(60), Some(1500), 0, true)]
    #[case::never_succeeded(Some(60), None, 0, true)]
    #[case::running_now(Some(1), Some(1500), 1, false)]
    fn last_run_failed(
        #[case] scheduled_minutes_ago: Option<i64>,
        #[case] succeeded_minutes_ago: Option<i64>,
        #[case] active: usize,
        #[case] failed: bool,
    ) {
        let report = cron_job(
            scheduled_minutes_ago.map(minutes_before),
            succeeded_minutes_ago.map(minutes_before),
            active,
        );
        assert_eq!(last_run(&report).is_some(), failed);
    }
}
