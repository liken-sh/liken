//! The line under each workload's name on the namespace page gives how
//! many pods the workload asks for and how it spreads them, or how its
//! last run went.  This file reads the facts, and `sentence.rs` writes
//! the words.

use k8s_openapi::api::batch::v1::CronJob;
use k8s_openapi::api::core::v1::Pod;

use transect_graph::pods::finished;
pub use transect_ground::guards::Spread;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Line {
    /// A Deployment or StatefulSet: the replicas it asks for, and how
    /// its pod template spreads them across machines, if it does.
    Replicas {
        replicas: i32,
        spread: Option<Spread>,
    },
    /// A DaemonSet: how many machines it runs a pod on.
    OnMachines(i32),
    LastRun(LastRun),
    Job(JobState),
    /// Any other kind, such as a custom one: how many pods it runs.
    Pods(usize),
    /// The box that groups small helpers.
    Helpers,
}

/// How a CronJob's last run went.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LastRun {
    Running,
    Succeeded,
    Failed,
    Never,
    Suspended,
}

/// The state of a Job, read from its pods, because transect watches a
/// Job only for its owner references.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum JobState {
    Running,
    Succeeded,
    Failed,
    /// Its pods are gone, so nothing is left to show how it went.
    NoPods,
}

/// How `cron_job`'s last run went.  The controller sets
/// `lastScheduleTime` when it starts a run and `lastSuccessfulTime` when
/// one completes, so with no run active, a last success older than the
/// last schedule means the last run failed.
pub fn last_run(cron_job: &CronJob) -> LastRun {
    if cron_job.spec.suspend == Some(true) {
        return LastRun::Suspended;
    }
    let Some(status) = &cron_job.status else {
        return LastRun::Never;
    };
    if status
        .active
        .as_ref()
        .is_some_and(|active| !active.is_empty())
    {
        return LastRun::Running;
    }
    let Some(scheduled) = &status.last_schedule_time else {
        return LastRun::Never;
    };
    match &status.last_successful_time {
        Some(succeeded) if succeeded.0 >= scheduled.0 => LastRun::Succeeded,
        _ => LastRun::Failed,
    }
}

/// The state of a Job from `pods`: running while any pod runs, then
/// succeeded when any pod succeeded, because a Job that retries keeps
/// the pods that failed before its last good one.
pub fn job_state(pods: &[&Pod]) -> JobState {
    let phase = |pod: &&Pod| pod.status.as_ref().and_then(|s| s.phase.clone());
    if pods.iter().any(|pod| !finished(pod)) {
        JobState::Running
    } else if pods
        .iter()
        .any(|pod| phase(pod).as_deref() == Some("Succeeded"))
    {
        JobState::Succeeded
    } else if pods.is_empty() {
        JobState::NoPods
    } else {
        JobState::Failed
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;
    use serde::de::DeserializeOwned;

    use super::*;

    fn yaml<K: DeserializeOwned>(text: &str) -> K {
        serde_yaml::from_str(text).unwrap()
    }

    #[rstest]
    #[case::never("{}", "{}", LastRun::Never)]
    #[case::suspended("{suspend: true}", "{}", LastRun::Suspended)]
    #[case::running("{}", "{active: [{name: report-1}]}", LastRun::Running)]
    #[case::succeeded(
        "{}",
        "{lastScheduleTime: '2026-01-01T00:00:00Z', lastSuccessfulTime: '2026-01-01T00:01:00Z'}",
        LastRun::Succeeded
    )]
    #[case::failed(
        "{}",
        "{lastScheduleTime: '2026-01-02T00:00:00Z', lastSuccessfulTime: '2026-01-01T00:01:00Z'}",
        LastRun::Failed
    )]
    #[case::never_succeeded("{}", "{lastScheduleTime: '2026-01-02T00:00:00Z'}", LastRun::Failed)]
    fn reads_a_cron_jobs_last_run(#[case] spec: &str, #[case] status: &str, #[case] last: LastRun) {
        let mut cron_job: CronJob =
            yaml(&format!("{{metadata: {{name: report}}, status: {status}}}"));
        cron_job.spec = yaml(spec);
        assert_eq!(last_run(&cron_job), last);
    }

    fn pod(phase: &str) -> Pod {
        yaml(&format!("{{status: {{phase: {phase}}}}}"))
    }

    #[rstest]
    #[case::no_pods(&[], JobState::NoPods)]
    #[case::running(&["Failed", "Running"], JobState::Running)]
    #[case::succeeded_after_a_failure(&["Failed", "Succeeded"], JobState::Succeeded)]
    #[case::failed(&["Failed"], JobState::Failed)]
    fn reads_a_jobs_state_from_its_pods(#[case] phases: &[&str], #[case] state: JobState) {
        let pods: Vec<Pod> = phases.iter().map(|phase| pod(phase)).collect();
        let pods: Vec<&Pod> = pods.iter().collect();
        assert_eq!(job_state(&pods), state);
    }
}
