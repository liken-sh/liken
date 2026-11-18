//! The `alerts` section of a `MetricsSource`: the Alertmanager that
//! transect reads, and the conventions that its alerts follow.  The
//! monitoring stack's rules and scrape configuration set the meaning of
//! each severity value, the labels that name an object or a machine,
//! and the prefix that Prometheus adds to a label that collides with a
//! scrape target's own label.  So the cluster declares these
//! conventions in its `MetricsSource`, not in transect's code.

use serde::Deserialize;

use crate::health::Level;
use transect_graph::upstream::{Service, base_url};

/// How transect reads a cluster's alerts.
#[derive(Clone, Debug, Default, PartialEq, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct Source {
    /// The Alertmanager's base URL.  The `--alertmanager` flag takes
    /// precedence over it.
    pub url: Option<String>,
    /// The `Service` in front of the Alertmanager, used when no URL is
    /// given.
    pub service: Option<Service>,
    pub severity: Severity,
    /// The label that names an alert's namespace.
    pub namespace: String,
    /// The labels that name an object, most specific first.
    #[serde(default)]
    pub objects: Vec<ObjectLabel>,
    /// The label that names a pod's container.
    pub container: Option<String>,
    /// The labels whose value is a `Node`'s name.  When an alert has
    /// one of these labels and names no object except a pod, the alert
    /// is about the machine, not about the pod's namespace.
    #[serde(default)]
    pub node_labels: Vec<String>,
    /// The labels whose value is a scrape address.  When no other label
    /// names a machine, a scrape address identifies one if it is a
    /// `Node`'s name, one of its addresses, or a host name whose first
    /// label is the `Node`'s name.
    #[serde(default)]
    pub instance_labels: Vec<String>,
    /// The labels that a scrape target adds to every series.  When the
    /// exported namespace label is present, these labels describe the
    /// exporter, not the object that the alert is about.
    #[serde(default)]
    pub scrape_labels: Vec<String>,
    /// The prefix that Prometheus adds to an object's label when it
    /// collides with a scrape target's label, such as `exported_`.
    pub exported_prefix: Option<String>,
    #[serde(default)]
    pub annotations: Annotations,
}

/// The severity label and what its values mean.  An alert whose value
/// is in no list, or that has no severity label, counts as a warning,
/// because someone wrote a rule for it and it is firing.
#[derive(Clone, Debug, Default, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Severity {
    pub label: String,
    /// The values that color what an alert is about as failing.
    #[serde(default)]
    pub failing: Vec<String>,
    /// The values that make an alert a notice, which "Now" lists and the
    /// page draws gray.
    #[serde(default)]
    pub notice: Vec<String>,
    /// The values of alerts that never need attention, such as a
    /// watchdog alert that proves that the alerting pipeline works.
    #[serde(default)]
    pub hidden: Vec<String>,
}

/// A label whose value names an object of one kind.
#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ObjectLabel {
    pub label: String,
    #[serde(default)]
    pub group: String,
    pub kind: String,
}

/// The annotations that hold an alert's summary and description.
#[derive(Clone, Debug, Default, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Annotations {
    pub summary: Option<String>,
    pub description: Option<String>,
}

impl Source {
    /// Normalizes `url` with `base_url`, or returns why the section is
    /// not valid.
    pub fn validated(mut self) -> Result<Self, String> {
        self.url = self.url.as_deref().map(base_url).transpose()?;
        let empty = |label: &str| label.trim().is_empty();
        if empty(&self.severity.label) {
            return Err("alerts.severity.label is empty".into());
        }
        if empty(&self.namespace) {
            return Err("alerts.namespace is empty".into());
        }
        if let Some(object) = self
            .objects
            .iter()
            .find(|o| empty(&o.label) || empty(&o.kind))
        {
            return Err(format!(
                "alerts.objects has an entry with an empty label or kind, label {:?}",
                object.label
            ));
        }
        let lists = [
            &self.node_labels,
            &self.instance_labels,
            &self.scrape_labels,
        ];
        if lists
            .iter()
            .any(|labels| labels.iter().any(|label| empty(label)))
        {
            return Err("alerts.nodeLabels, alerts.instanceLabels, or alerts.scrapeLabels has an empty label".into());
        }
        Ok(self)
    }
}

impl Severity {
    /// The level that an alert whose severity label is `value` gives to
    /// what it is about, or `None` for an alert that never needs
    /// attention.  Values match without regard to case.
    pub fn level(&self, value: Option<&str>) -> Option<Level> {
        let is = |values: &[String]| {
            value.is_some_and(|value| values.iter().any(|v| v.eq_ignore_ascii_case(value)))
        };
        match () {
            _ if is(&self.hidden) => None,
            _ if is(&self.failing) => Some(Level::Failing),
            _ if is(&self.notice) => Some(Level::Notice),
            _ => Some(Level::Warning),
        }
    }
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// The section that a cluster declares for the conventions of a
    /// common monitoring stack.  The tests parse alerts with it.
    pub fn source() -> Source {
        let source: Source = serde_yaml::from_str(
            "severity: {label: severity, failing: [critical, error], notice: [info], hidden: [none]}
namespace: namespace
objects:
  - {label: pod, kind: Pod}
  - {label: deployment, group: apps, kind: Deployment}
  - {label: statefulset, group: apps, kind: StatefulSet}
  - {label: daemonset, group: apps, kind: DaemonSet}
  - {label: cronjob, group: batch, kind: CronJob}
  - {label: job_name, group: batch, kind: Job}
  - {label: persistentvolumeclaim, kind: PersistentVolumeClaim}
  - {label: service, kind: Service}
container: container
nodeLabels: [node]
instanceLabels: [instance]
scrapeLabels: [pod, container, service]
exportedPrefix: exported_
annotations: {summary: summary, description: description}",
        )
        .unwrap();
        source.validated().unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::failing(Some("critical"), Some(Level::Failing))]
    #[case::failing_by_another_value(Some("error"), Some(Level::Failing))]
    #[case::in_another_case(Some("Critical"), Some(Level::Failing))]
    #[case::a_notice(Some("info"), Some(Level::Notice))]
    #[case::hidden(Some("none"), None)]
    #[case::a_value_in_no_list(Some("page"), Some(Level::Warning))]
    #[case::no_value(None, Some(Level::Warning))]
    fn a_severity_sets_the_level(#[case] value: Option<&str>, #[case] level: Option<Level>) {
        assert_eq!(fixtures::source().severity.level(value), level);
    }

    #[rstest]
    #[case::an_empty_severity_label(
        "severity: {label: ''}\nnamespace: namespace",
        "severity.label"
    )]
    #[case::an_empty_namespace_label("severity: {label: severity}\nnamespace: ' '", "namespace")]
    #[case::an_object_with_no_kind(
        "severity: {label: severity}\nnamespace: namespace\nobjects: [{label: pod, kind: ''}]",
        "empty label or kind"
    )]
    #[case::an_empty_machine_label(
        "severity: {label: severity}\nnamespace: namespace\nnodeLabels: ['']",
        "empty label"
    )]
    #[case::a_url_of_another_scheme(
        "url: ftp://alerts.example.test\nseverity: {label: severity}\nnamespace: namespace",
        "http or https"
    )]
    fn says_why_a_section_does_not_validate(#[case] yaml: &str, #[case] says: &str) {
        let source: Source = serde_yaml::from_str(yaml).unwrap();
        let error = source.validated().unwrap_err();
        assert!(error.contains(says), "{error:?} does not say {says:?}");
    }
}
