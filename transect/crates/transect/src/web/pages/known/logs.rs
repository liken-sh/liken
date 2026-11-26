//! The page of a `Job`'s log lines, for a cell of an elevation of rows
//! that names the `Job`. The logs lens of a pod shows its lines from the
//! `LogSource`, so the cell links to the lens of the `Job`'s pod while a
//! `LogSource` is declared and the cluster has the pod. A `Job` that its
//! time to live deleted has no pod for the lens to name, and its name is
//! shown with no link.

use super::super::super::snapshot::Snapshot;
use transect_graph::edges::Object;

/// The URL of the logs lens of the newest pod of the `Job`
/// `namespace/job`, when the lens can show its lines.  A `Job` that
/// retries makes a pod for each try, and the newest has the lines of
/// the try that counted.
pub fn of_job(snapshot: &Snapshot, namespace: &str, job: &str) -> Option<String> {
    snapshot.resources.log_source.as_ref()?;
    let job = Object::new("batch", "Job", Some(namespace), job);
    let owned = snapshot.ownership.owned(&job, "", "Pod");
    let pods = snapshot.pods.iter().filter(|pod| {
        let meta = &pod.metadata;
        meta.namespace.as_deref() == Some(namespace)
            && owned
                .iter()
                .any(|o| Some(o.name.as_str()) == meta.name.as_deref())
    });
    let newest = pods.max_by_key(|pod| pod.metadata.creation_timestamp.as_ref().map(|t| t.0))?;
    let name = newest.metadata.name.as_deref()?;
    Some(format!("/namespaces/{namespace}/pods/{name}/logs"))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::super::fixture;
    use super::*;

    #[rstest]
    #[case::a_job_with_a_pod(
        "garden-tend-1",
        Some("/namespaces/acme-garden/pods/garden-tend-1-pod/logs")
    )]
    #[case::a_job_with_no_pod("garden-tend-1-before", None)]
    #[case::a_job_that_is_gone("garden-tend-0", None)]
    fn links_a_job_to_the_lines_of_its_pod(#[case] job: &str, #[case] expected: Option<&str>) {
        let found = of_job(&fixture::snapshot(), "acme-garden", job);
        assert_eq!(found.as_deref(), expected);
    }

    #[rstest]
    fn links_no_job_without_a_log_source() {
        let mut snapshot = fixture::snapshot();
        snapshot.resources = std::sync::Arc::default();
        assert_eq!(of_job(&snapshot, "acme-garden", "garden-tend-1"), None);
    }
}
