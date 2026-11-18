//! Per-machine usage from the results of the `MetricsSource`'s machine
//! queries.

use std::collections::HashMap;

use crate::prometheus::Sample;

/// Maps the value of each of `labels` in a sample, with any port
/// removed, to the sample's value. The scrape config decides which
/// label holds the `Node` name, so a sample is stored under every one
/// of `labels` that it has, and callers look up only real `Node` names.
/// When several samples give the same name, the largest value is kept.
pub fn by_name(samples: Vec<Sample>, labels: &[String]) -> HashMap<String, f64> {
    let mut by_name = HashMap::new();
    for sample in samples {
        for label in labels {
            let Some(value) = sample.labels.get(label) else {
                continue;
            };
            let name = without_port(value);
            if name.is_empty() {
                continue;
            }
            let entry = by_name.entry(name.to_owned()).or_insert(sample.value);
            *entry = entry.max(sample.value);
        }
    }
    by_name
}

/// `instance` without its port, as in `alpha:10250` to `alpha`. The
/// `instance` label is a scrape address unless the scrape config
/// renames it. An IPv6 address keeps its colons, and a bracketed one
/// keeps its brackets.
fn without_port(instance: &str) -> &str {
    if instance.starts_with('[') {
        return match instance.find(']') {
            Some(end) => &instance[..=end],
            None => instance,
        };
    }
    match instance.split_once(':') {
        Some((host, port)) if !port.contains(':') => host,
        _ => instance,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::usage::fixtures::sample;

    /// The labels that may hold a `Node`'s name, in the shapes that
    /// common scrape configurations produce.
    fn labels() -> Vec<String> {
        ["node", "instance", "hostname"].map(Into::into).to_vec()
    }

    #[rstest]
    #[case::node_label(&[("node", "alpha"), ("instance", "10.0.0.5:10250")])]
    #[case::instance_is_the_node(&[("instance", "alpha")])]
    #[case::instance_with_a_port(&[("instance", "alpha:10250")])]
    #[case::hostname_label(&[("instance", "kubernetes.default.svc:443"), ("hostname", "alpha")])]
    fn finds_the_node_from_whichever_label_names_it(#[case] pairs: &[(&str, &str)]) {
        let found = by_name(vec![sample(pairs, 1.5)], &labels());
        assert_eq!(found.get("alpha"), Some(&1.5));
    }

    #[rstest]
    fn two_series_for_one_node_keep_the_larger() {
        let by_name = by_name(
            vec![
                sample(&[("node", "alpha"), ("instance", "a:10250")], 1.0),
                sample(&[("node", "alpha"), ("instance", "b:10250")], 3.0),
            ],
            &labels(),
        );
        assert_eq!(by_name.get("alpha"), Some(&3.0));
    }

    #[rstest]
    #[case::bare("alpha", "alpha")]
    #[case::port("alpha:10250", "alpha")]
    #[case::ipv4("10.0.0.5:10250", "10.0.0.5")]
    #[case::ipv6_with_port("[fd00::5]:10250", "[fd00::5]")]
    #[case::ipv6_bare("fd00::5", "fd00::5")]
    fn removes_the_port_from_an_instance(#[case] instance: &str, #[case] expected: &str) {
        assert_eq!(without_port(instance), expected);
    }
}
