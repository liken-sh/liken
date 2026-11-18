//! The evidence that a log finding shows besides its count: the pods,
//! containers, and machines that the matches came from, and a few of
//! the matching lines. With only a count, a reader must open every pod
//! to tell whether the lines are one routine message or something new.

use super::sample::Sample;
use super::search::Count;
use crate::health::times;

/// The most places that a finding lists, ordered by matches.
const PARTS: usize = 6;

/// The most sample lines that a finding shows.
const SAMPLES: usize = 5;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Evidence {
    pub parts: Vec<Part>,
    /// The number of places with matches that are not in `parts`.
    pub more: usize,
    pub samples: Vec<Sample>,
}

/// The matches from one pod, container, or machine.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Part {
    pub pod: Option<String>,
    pub container: Option<String>,
    pub node: Option<String>,
    pub matches: usize,
    /// A link to the pod's page, or to the pattern's matching lines on
    /// its logs page. `None` when the pod no longer exists.
    pub href: Option<String>,
}

impl Evidence {
    /// The evidence for the matches in `counts`, with the `samples` from
    /// the same pods, or from the same machines for counts that have no
    /// pod. A part links to its pod only when `exists` returns true for
    /// the pod's namespace and name, because a deleted pod has no page.
    pub fn of(
        counts: &[Count],
        samples: &[Sample],
        exists: impl Fn(&str, &str) -> bool,
        anchor: Option<&str>,
    ) -> Self {
        let mut sorted: Vec<&Count> = counts.iter().collect();
        sorted.sort_by(|a, b| b.matches.cmp(&a.matches).then(a.pod.cmp(&b.pod)));
        let parts = sorted.iter().take(PARTS).map(|count| Part {
            pod: count.pod.clone(),
            container: count.container.clone(),
            node: count.node.clone(),
            matches: count.matches,
            href: count
                .pod
                .as_deref()
                .filter(|pod| exists(&count.namespace, pod))
                .map(|pod| {
                    // The pod's logs page marks a pattern's matching
                    // lines with the anchor.
                    match anchor {
                        Some(anchor) => {
                            format!("/namespaces/{}/pods/{pod}/logs#{anchor}", count.namespace)
                        }
                        None => format!("/namespaces/{}/pods/{pod}", count.namespace),
                    }
                }),
        });
        let from = |sample: &&Sample| {
            counts.iter().any(|count| match &count.pod {
                Some(pod) => {
                    sample.pod.as_ref() == Some(pod)
                        && sample.namespace.as_deref() == Some(count.namespace.as_str())
                }
                None => sample.pod.is_none() && sample.node == count.node,
            })
        };
        Self {
            parts: parts.collect(),
            more: sorted.len().saturating_sub(PARTS),
            samples: samples.iter().filter(from).take(SAMPLES).cloned().collect(),
        }
    }

    pub fn is_empty(&self) -> bool {
        self.parts.is_empty() && self.samples.is_empty()
    }

    /// Whether the page lists the places. It does when there is more
    /// than one place, or when the one place has a container or a
    /// machine, which the finding's own line does not show.
    pub fn lists_parts(&self) -> bool {
        self.parts.len() > 1
            || self
                .parts
                .iter()
                .any(|part| part.container.is_some() || part.node.is_some())
    }

    /// "and 3 more", shown under a list that was cut at `PARTS`.
    pub fn more_text(&self) -> Option<String> {
        (self.more > 0).then(|| format!("and {} more", self.more))
    }
}

impl Part {
    /// The pod name, or "lines with no pod" when the matches have no
    /// pod.
    pub fn label(&self) -> &str {
        self.pod.as_deref().unwrap_or("lines with no pod")
    }

    /// "in web", shown after the pod name when the count has a
    /// container.
    pub fn container_text(&self) -> Option<String> {
        self.container
            .as_ref()
            .map(|container| format!("in {container}"))
    }

    /// "12 lines".
    pub fn count_text(&self) -> String {
        times(self.matches as i64, "line")
    }
}

impl Sample {
    /// The time the line was logged, in UTC, as "12:00:04".
    pub fn at(&self) -> Option<String> {
        self.time.map(|time| time.strftime("%H:%M:%S").to_string())
    }

    /// Where the line was logged, as "web-0/web". A line with no pod
    /// shows its machine, and its unit when it has one.
    pub fn from(&self) -> Option<String> {
        match (&self.pod, &self.container, &self.node) {
            (Some(pod), Some(container), _) => Some(format!("{pod}/{container}")),
            (Some(pod), None, _) => Some(pod.clone()),
            (None, Some(unit), Some(node)) => Some(format!("{node}/{unit}")),
            (None, _, node) => node.clone(),
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn count(pod: Option<&str>, container: Option<&str>, node: Option<&str>, n: usize) -> Count {
        Count {
            namespace: "shop".into(),
            pod: pod.map(Into::into),
            container: container.map(Into::into),
            node: node.map(Into::into),
            matches: n,
            last: None,
        }
    }

    fn sample(namespace: &str, pod: Option<&str>, message: &str) -> Sample {
        Sample {
            namespace: Some(namespace.into()),
            pod: pod.map(Into::into),
            message: message.into(),
            ..Sample::default()
        }
    }

    fn exists(_: &str, pod: &str) -> bool {
        pod != "web-9"
    }

    fn parts(evidence: &Evidence) -> Vec<(&str, Option<&str>, usize)> {
        let parts = evidence.parts.iter();
        parts
            .map(|part| (part.label(), part.href.as_deref(), part.matches))
            .collect()
    }

    #[rstest]
    #[case::the_most_first(
        vec![count(Some("web-0"), Some("web"), None, 2), count(Some("web-1"), Some("web"), None, 9)],
        vec![
            ("web-1", Some("/namespaces/shop/pods/web-1/logs#matching-refused"), 9),
            ("web-0", Some("/namespaces/shop/pods/web-0/logs#matching-refused"), 2),
        ]
    )]
    #[case::a_gone_pod_has_no_link(
        vec![count(Some("web-9"), None, None, 4)],
        vec![("web-9", None, 4)]
    )]
    #[case::no_pod(vec![count(None, None, None, 4)], vec![("lines with no pod", None, 4)])]
    fn lists_where_the_matches_came_from(
        #[case] counts: Vec<Count>,
        #[case] expected: Vec<(&str, Option<&str>, usize)>,
    ) {
        let evidence = Evidence::of(&counts, &[], exists, Some("matching-refused"));
        assert_eq!(parts(&evidence), expected);
    }

    #[rstest]
    fn cuts_a_long_list_and_counts_the_rest() {
        let counts: Vec<Count> = (0..8)
            .map(|n| count(Some(&format!("web-{n}")), None, None, n + 1))
            .collect();
        let evidence = Evidence::of(&counts, &[], exists, None);
        assert_eq!(
            (evidence.parts.len(), evidence.more_text()),
            (6, Some("and 2 more".into()))
        );
    }

    #[rstest]
    fn keeps_the_lines_from_its_own_pods() {
        let counts = vec![count(Some("web-0"), None, None, 3)];
        let samples = vec![
            sample("shop", Some("db-0"), "not ours"),
            sample("shop", Some("web-0"), "ours"),
            sample("blog", Some("web-0"), "another namespace's"),
        ];
        let evidence = Evidence::of(&counts, &samples, exists, None);
        let messages: Vec<&str> = evidence
            .samples
            .iter()
            .map(|s| s.message.as_str())
            .collect();
        assert_eq!(messages, vec!["ours"]);
    }

    #[rstest]
    #[case::one_pod_alone(vec![count(Some("web-0"), None, None, 3)], false)]
    #[case::one_pod_on_a_machine(vec![count(Some("web-0"), None, Some("machine-a"), 3)], true)]
    #[case::two_pods(vec![count(Some("web-0"), None, None, 3), count(Some("web-1"), None, None, 1)], true)]
    fn lists_places_only_when_the_finding_line_does_not_show_them(
        #[case] counts: Vec<Count>,
        #[case] lists: bool,
    ) {
        assert_eq!(
            Evidence::of(&counts, &[], exists, None).lists_parts(),
            lists
        );
    }

    #[rstest]
    #[case::a_pods_container(Some("web-0"), Some("web"), Some("machine-a"), Some("web-0/web"))]
    #[case::a_machines_unit(None, Some("k3s"), Some("machine-a"), Some("machine-a/k3s"))]
    #[case::a_machine(None, None, Some("machine-a"), Some("machine-a"))]
    #[case::nowhere(None, None, None, None)]
    fn a_sample_shows_where_it_was_logged(
        #[case] pod: Option<&str>,
        #[case] container: Option<&str>,
        #[case] node: Option<&str>,
        #[case] expected: Option<&str>,
    ) {
        let sample = Sample {
            pod: pod.map(Into::into),
            container: container.map(Into::into),
            node: node.map(Into::into),
            ..Sample::default()
        };
        assert_eq!(sample.from().as_deref(), expected);
    }
}
