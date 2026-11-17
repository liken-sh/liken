//! What each machine uses right now, from the results of the
//! `MetricsSource`'s machine queries.  Which label of a series holds
//! the `Node`'s name depends on the scrape configuration, so the source
//! lists the labels to try.

use std::collections::HashMap;

use crate::prometheus::Sample;

/// Stores each sample under every name that its `labels` give, because
/// which label holds the `Node`'s name depends on the scrape config.
/// No caller looks up a name that matches no `Node`, so the extra keys
/// cost only a few bytes.  A label's value may be a scrape address, and
/// the function removes its port.
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

/// `alpha:10250` as `alpha`.  An `instance` is a scrape address when the
/// scrape config does not rename it.  An IPv6 address in brackets keeps
/// its colons.
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
    fn instance_without_its_port(#[case] instance: &str, #[case] expected: &str) {
        assert_eq!(without_port(instance), expected);
    }
}
