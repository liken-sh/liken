//! The finding for containers that last restarted between
//! `RECENT_MINUTES` and `EARLIER_MINUTES` ago.  A restart that old no
//! longer affects a namespace's health, so the sidebar's "Now" list does
//! not show it, and this finding lists it with one line for each
//! workload and container.

use super::Finding;
use transect_observe::health::{EARLIER_MINUTES, Earlier, RECENT_MINUTES, times};
use transect_resources::knowledge::age;

/// One container's earlier restarts, with the link to its workload's
/// page.
#[derive(Clone, Debug, PartialEq)]
pub struct Restarted {
    pub earlier: Earlier,
    pub href: Option<String>,
}

/// The finding for `restarted`, when any container restarted earlier.
pub fn finding(restarted: Vec<Restarted>) -> Option<Finding> {
    (!restarted.is_empty()).then_some(Finding::RestartedEarlier(restarted))
}

/// "3 containers restarted between 1h and 24h ago".
pub fn title(restarted: &[Restarted]) -> String {
    format!(
        "{} restarted between {} and {} ago",
        times(restarted.len() as i64, "container"),
        age(RECENT_MINUTES),
        age(EARLIER_MINUTES)
    )
}

/// The text after the workload's name on its line, as "app restarted in
/// 2 pods, the latest 3h ago".
pub fn line(earlier: &Earlier) -> String {
    let restarted = if earlier.clean {
        "exited cleanly and restarted"
    } else {
        "restarted"
    };
    let ago = age(earlier.minutes_ago);
    match earlier.pods {
        1 => format!("{} {restarted} {ago} ago", earlier.container),
        pods => format!(
            "{} {restarted} in {pods} pods, the latest {ago} ago",
            earlier.container
        ),
    }
}

/// The explanation shown with the finding.
pub fn body() -> String {
    format!(
        "A restart {} old or older no longer colors its namespace. A container that restarts every few hours shows only here.",
        age(RECENT_MINUTES)
    )
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::Object;

    fn restarted(container: &str, pods: usize, minutes_ago: i64, clean: bool) -> Restarted {
        let workload = Object::new("apps", "StatefulSet", Some("shop"), "web");
        let href = Some("/namespaces/shop/statefulsets/web".into());
        let earlier = Earlier {
            workload,
            container: container.into(),
            pods,
            minutes_ago,
            clean,
        };
        Restarted { earlier, href }
    }

    #[rstest]
    fn no_earlier_restarts_make_no_finding() {
        assert_eq!(finding(Vec::new()), None);
    }

    #[rstest]
    #[case::one_pod(restarted("app", 1, 180, false), "app restarted 3h ago")]
    #[case::one_pod_cleanly(
        restarted("app", 1, 60, true),
        "app exited cleanly and restarted 1h ago"
    )]
    #[case::two_pods(
        restarted("app", 2, 120, false),
        "app restarted in 2 pods, the latest 2h ago"
    )]
    fn lists_each_workloads_container_with_its_restarts(
        #[case] restarted: Restarted,
        #[case] expected: &str,
    ) {
        let found = finding(vec![restarted]).unwrap();
        let lines: Vec<(String, String)> = found
            .lines()
            .into_iter()
            .map(|line| (line.label, line.text))
            .collect();
        assert_eq!(
            (found.title(), lines),
            (
                "1 container restarted between 1h and 24h ago".into(),
                vec![("shop/web".into(), expected.into())]
            )
        );
    }
}
