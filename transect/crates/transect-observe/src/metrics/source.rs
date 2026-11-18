//! The `MetricsSource` resource: the Prometheus that transect queries
//! for the figures that Kubernetes does not report, the queries it
//! sends, and the Alertmanager whose alerts it reads.  The cluster
//! declares every query and label, because the exporters, the label
//! that holds a `Node`'s name, and the labels on alerts differ from one
//! cluster to the next.
//!
//! `--prometheus` takes precedence over the source's `url`, and `url`
//! over its `service`.  `--alertmanager` takes precedence over the
//! address in `alerts`.  Without a source, transect sends no queries,
//! and each page says which declaration would show the figures.

use serde::Deserialize;

use crate::alerts;
use crate::usage::{self, ContainerQuery, MachineQuery};
use transect_graph::upstream::{Service, base_url};
use transect_resources::resources::DocId;

/// One `MetricsSource` resource, validated.
#[derive(Clone, Debug, PartialEq)]
pub struct MetricsSource {
    pub doc: DocId,
    pub name: String,
    /// Prometheus's base URL, with no trailing slash.
    pub url: Option<String>,
    pub service: Option<Service>,
    /// The queries for what each machine and container uses.
    pub usage: usage::Queries,
    /// The query for the bytes used on each claim's volume, when the
    /// source declares one.
    pub volume_used: Option<ClaimQuery>,
    /// The Alertmanager and how its alerts are labelled, when the
    /// source declares them.
    pub alerts: Option<alerts::Source>,
}

/// A query that returns one series per claim, and the labels in each
/// series that hold the claim's namespace and name.
#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClaimQuery {
    pub query: String,
    pub labels: ClaimLabels,
}

#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClaimLabels {
    pub namespace: String,
    pub claim: String,
}

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Spec {
    url: Option<String>,
    service: Option<Service>,
    #[serde(default)]
    node_labels: Vec<String>,
    #[serde(default)]
    queries: Queries,
    alerts: Option<alerts::Source>,
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Queries {
    machine_cpu: Option<MachineQuery>,
    machine_memory: Option<MachineQuery>,
    container_cpu: Option<ContainerQuery>,
    container_memory: Option<ContainerQuery>,
    volume_used: Option<ClaimQuery>,
}

/// The `MetricsSource` that `value` declares, or why it does not validate.
pub fn parse(doc: DocId, value: serde_yaml::Value) -> Result<MetricsSource, String> {
    let document: Document = serde_yaml::from_value(value).map_err(|error| error.to_string())?;
    let spec = document.spec;
    let queries = spec.queries;
    let volume_used = queries.volume_used;
    if let Some(used) = &volume_used {
        if used.query.trim().is_empty() {
            return Err("queries.volumeUsed.query is empty".into());
        }
        let labels = [&used.labels.namespace, &used.labels.claim];
        if labels.iter().any(|label| label.trim().is_empty()) {
            return Err("queries.volumeUsed.labels has an empty label".into());
        }
    }
    let usage = usage::Queries::read(
        spec.node_labels,
        queries.machine_cpu,
        queries.machine_memory,
        queries.container_cpu,
        queries.container_memory,
    )?;
    Ok(MetricsSource {
        doc,
        name: document.metadata.name,
        url: spec.url.as_deref().map(base_url).transpose()?,
        service: spec.service,
        usage,
        volume_used,
        alerts: spec.alerts.map(alerts::Source::validated).transpose()?,
    })
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// A `MetricsSource` that declares only `alerts`.
    pub fn with_alerts(alerts: alerts::Source) -> MetricsSource {
        MetricsSource {
            doc: DocId::default(),
            name: "metrics".into(),
            url: None,
            service: None,
            usage: usage::Queries::default(),
            volume_used: None,
            alerts: Some(alerts),
        }
    }

    /// A `MetricsSource` document with `spec` as its spec.
    pub fn document(spec: &str) -> serde_yaml::Value {
        let text = format!(
            "apiVersion: transect.liken.sh/v1alpha1\nkind: MetricsSource\nmetadata: {{name: metrics}}\nspec: {spec}\n"
        );
        serde_yaml::from_str(&text).unwrap()
    }

    pub const VOLUME_USED: &str = "{queries: {volumeUsed: {query: used_bytes, labels: {namespace: namespace, claim: persistentvolumeclaim}}}}";
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::{VOLUME_USED, document};
    use super::*;

    #[rstest]
    fn reads_the_volume_used_query_and_its_labels() {
        let source = parse(DocId::default(), document(VOLUME_USED)).unwrap();
        let used = source.volume_used.unwrap();
        assert_eq!(
            (
                used.query.as_str(),
                used.labels.namespace.as_str(),
                used.labels.claim.as_str()
            ),
            ("used_bytes", "namespace", "persistentvolumeclaim")
        );
    }

    #[rstest]
    fn reads_the_usage_queries_and_their_labels() {
        let spec = "{nodeLabels: [node, instance], queries: {
            machineCpu: {query: machine_cores},
            machineMemory: {query: machine_bytes},
            containerCpu: {query: container_cores, labels: {namespace: ns, pod: p, container: c}},
            containerMemory: {query: container_bytes, labels: {namespace: ns, pod: p, container: c}}}}";
        let usage = parse(DocId::default(), document(spec)).unwrap().usage;
        let container = |query: &Option<ContainerQuery>| {
            let query = query.as_ref().unwrap();
            (query.query.clone(), query.labels.pod.clone())
        };
        assert_eq!(
            (
                usage.node_labels.clone(),
                usage.machine_cpu.clone(),
                usage.machine_memory.clone(),
                container(&usage.container_cpu),
                container(&usage.container_memory),
            ),
            (
                vec!["node".to_string(), "instance".to_string()],
                Some("machine_cores".to_string()),
                Some("machine_bytes".to_string()),
                ("container_cores".to_string(), "p".to_string()),
                ("container_bytes".to_string(), "p".to_string()),
            )
        );
    }

    #[rstest]
    #[case::a_url(
        "{url: 'http://localhost:9090/'}",
        Some("http://localhost:9090"),
        false
    )]
    #[case::a_service(
        "{service: {namespace: monitoring, name: prometheus, port: 9090}}",
        None,
        true
    )]
    fn reads_where_prometheus_is(
        #[case] spec: &str,
        #[case] url: Option<&str>,
        #[case] service: bool,
    ) {
        let source = parse(DocId::default(), document(spec)).unwrap();
        assert_eq!(
            (source.url.as_deref(), source.service.is_some()),
            (url, service)
        );
    }

    #[rstest]
    fn reads_the_alerts_section() {
        let spec = "{alerts: {url: 'http://localhost:9093', severity: {label: severity, failing: [critical]}, namespace: namespace}}";
        let alerts = parse(DocId::default(), document(spec))
            .unwrap()
            .alerts
            .unwrap();
        assert_eq!(
            (alerts.url.as_deref(), alerts.severity.failing),
            (Some("http://localhost:9093"), vec!["critical".to_string()])
        );
    }

    #[rstest]
    fn may_declare_nothing() {
        let source = parse(DocId::default(), document("{}")).unwrap();
        assert_eq!(
            (
                source.volume_used,
                source.usage.is_empty(),
                source.alerts,
                source.url
            ),
            (None, true, None, None)
        );
    }

    #[rstest]
    #[case::an_empty_query(
        "{queries: {volumeUsed: {query: ' ', labels: {namespace: n, claim: c}}}}",
        "is empty"
    )]
    #[case::an_empty_label(
        "{queries: {volumeUsed: {query: q, labels: {namespace: '', claim: c}}}}",
        "empty label"
    )]
    #[case::a_missing_label("{queries: {volumeUsed: {query: q, labels: {namespace: n}}}}", "claim")]
    #[case::an_unknown_query("{queries: {diskUsed: {query: q}}}", "unknown field")]
    #[case::machine_queries_with_no_node_label("{queries: {machineCpu: {query: q}}}", "nodeLabels")]
    #[case::an_empty_machine_query(
        "{nodeLabels: [node], queries: {machineMemory: {query: ''}}}",
        "machineMemory.query is empty"
    )]
    #[case::an_empty_container_label(
        "{queries: {containerCpu: {query: q, labels: {namespace: n, pod: '', container: c}}}}",
        "containerCpu.labels"
    )]
    #[case::a_url_of_another_scheme("{url: 'ftp://metrics.example.test'}", "http or https")]
    #[case::alerts_with_no_severity_label("{alerts: {namespace: namespace}}", "severity")]
    fn says_why_a_source_does_not_validate(#[case] spec: &str, #[case] says: &str) {
        let error = parse(DocId::default(), document(spec)).unwrap_err();
        assert!(error.contains(says), "{error:?}, want {says:?}");
    }
}
