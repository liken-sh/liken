//! Finds how a workload last ran, for a custom kind with no controller
//! under it, such as one that runs its work as `Job`s. When none of its
//! pods is running, there are no ready pods to count, so its card shows
//! how its newest pod ended.

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;

/// When the newest finished pod stopped, and whether it failed.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct LastRun {
    pub failed: bool,
    pub at: Timestamp,
}

/// Returns when the newest finished pod of `pods` stopped, and whether
/// it failed. A pod stopped when its last container stopped, or, with
/// no stopped container, when it was created.
pub fn last_run(pods: &[&Pod]) -> Option<LastRun> {
    pods.iter()
        .filter_map(|pod| {
            let status = pod.status.as_ref()?;
            let failed = match status.phase.as_deref() {
                Some("Succeeded") => false,
                Some("Failed") => true,
                _ => return None,
            };
            let stopped = status.container_statuses.iter().flatten().filter_map(|c| {
                let terminated = c.state.as_ref()?.terminated.as_ref()?;
                Some(terminated.finished_at.as_ref()?.0)
            });
            let created = pod.metadata.creation_timestamp.as_ref().map(|t| t.0);
            let at = stopped.max().or(created)?;
            Some(LastRun { failed, at })
        })
        .max_by_key(|run| run.at)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn pod(phase: &str, finished: &str) -> Pod {
        serde_yaml::from_str(&format!(
            "metadata: {{creationTimestamp: '2026-01-01T00:00:00Z'}}\n\
             status: {{phase: {phase}, containerStatuses: [{{name: job, image: x, imageID: x, ready: false, restartCount: 0, state: {{terminated: {{exitCode: 0, finishedAt: '{finished}'}}}}}}]}}"
        ))
        .unwrap()
    }

    fn run(failed: bool, at: &str) -> Option<LastRun> {
        Some(LastRun {
            failed,
            at: at.parse().unwrap(),
        })
    }

    #[rstest]
    #[case::the_newest_succeeded(
        vec![pod("Failed", "2026-01-01T01:00:00Z"), pod("Succeeded", "2026-01-01T02:00:00Z")],
        run(false, "2026-01-01T02:00:00Z")
    )]
    #[case::the_newest_failed(
        vec![pod("Succeeded", "2026-01-01T01:00:00Z"), pod("Failed", "2026-01-01T03:00:00Z")],
        run(true, "2026-01-01T03:00:00Z")
    )]
    #[case::still_running(vec![pod("Running", "2026-01-01T01:00:00Z")], None)]
    #[case::no_pods(vec![], None)]
    fn reads_how_the_newest_pod_ended(#[case] pods: Vec<Pod>, #[case] expected: Option<LastRun>) {
        let pods: Vec<&Pod> = pods.iter().collect();
        assert_eq!(last_run(&pods), expected);
    }
}
