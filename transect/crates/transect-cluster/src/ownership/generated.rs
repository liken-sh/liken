//! Finds the workload of a deleted pod from the pod's name, so that the
//! log lines of the pod are counted with its workload. A pod's owner
//! references are deleted with the pod, but a `Job` names its pods
//! `<job>-<suffix>`, with a random suffix of five lowercase letters and
//! digits, and a `CronJob` names its `Job`s `<cronjob>-<minutes>`, where
//! the number is the scheduled time in minutes since the Unix epoch. So
//! the name still identifies the `CronJob` after its `Job` is deleted
//! too.
//!
//! Any name can match this pattern, so a match counts only when the
//! `Job` or `CronJob` exists in the pod's namespace.

use super::Ownership;
use transect_graph::edges::Object;

/// The length of the random suffix that Kubernetes adds to a generated
/// name.
const SUFFIX: usize = 5;

impl Ownership {
    /// The top owner of the deleted pod `namespace/pod`, when its name
    /// has the form of a pod of a `Job` or `CronJob` in the index.
    pub fn owner_by_name(&self, namespace: &str, pod: &str) -> Option<Object> {
        let (job, suffix) = pod.rsplit_once('-')?;
        let generated = suffix.len() == SUFFIX
            && suffix
                .chars()
                .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit());
        if !generated {
            return None;
        }
        let job_object = Object::new("batch", "Job", Some(namespace), job);
        if self.index.parents.contains_key(&job_object) {
            return Some(self.top_owner(&job_object));
        }
        let (cron_job, minutes) = job.rsplit_once('-')?;
        if minutes.is_empty() || !minutes.chars().all(|c| c.is_ascii_digit()) {
            return None;
        }
        let cron_job = Object::new("batch", "CronJob", Some(namespace), cron_job);
        self.index
            .parents
            .contains_key(&cron_job)
            .then_some(cron_job)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::build;
    use super::*;

    /// The `CronJob` `report` with its `Job` `report-29000010`, the
    /// `Job` `backfill` with no owner, and the `Deployment` `web`.
    fn ownership() -> Ownership {
        build(&[
            ("batch", "CronJob", Some("shop"), "report", vec![]),
            (
                "batch",
                "Job",
                Some("shop"),
                "report-29000010",
                vec![("batch/v1", "CronJob", "report")],
            ),
            ("batch", "Job", Some("shop"), "backfill", vec![]),
            ("apps", "Deployment", Some("shop"), "web", vec![]),
        ])
    }

    fn object(kind: &str, name: &str) -> Option<Object> {
        Some(Object::new("batch", kind, Some("shop"), name))
    }

    #[rstest]
    #[case::a_job_still_present("report-29000010-x7k2q", object("CronJob", "report"))]
    #[case::a_cron_jobs_job_gone("report-29000020-b4n8z", object("CronJob", "report"))]
    #[case::a_job_alone("backfill-q9z2m", object("Job", "backfill"))]
    #[case::a_job_the_cluster_lacks("migrate-q9z2m", None)]
    #[case::a_cron_job_the_cluster_lacks("nightly-29000020-b4n8z", None)]
    #[case::not_a_schedules_minutes("report-latest-b4n8z", None)]
    #[case::not_a_generated_suffix("report-29000020-0", None)]
    #[case::an_upper_case_suffix("report-29000020-B4N8Z", None)]
    #[case::a_deployments_pod("web-5d8f9c7b6-x7k2q", None)]
    #[case::no_dash("report", None)]
    fn reads_a_gone_pods_job_or_cron_job_from_its_name(
        #[case] pod: &str,
        #[case] expected: Option<Object>,
    ) {
        assert_eq!(ownership().owner_by_name("shop", pod), expected);
    }

    #[rstest]
    fn reads_only_the_pods_own_namespace() {
        assert_eq!(
            ownership().owner_by_name("other", "report-29000020-b4n8z"),
            None
        );
    }
}
