//! Finds what an alert is about from its labels, with the conventions
//! that the `alerts` section of the `MetricsSource` declares.  An alert
//! is about an object in a namespace, a namespace, a machine, or the
//! whole cluster.
//!
//! An alert's labels come from two places: the subject of its rule's
//! expression, and the scrape target that its series came from.  A
//! series from an exporter that reports on other objects has the
//! exporter's own namespace and pod labels.  Prometheus renames the
//! object's labels that collide with those by adding a prefix, such as
//! `exported_`.  So when an alert has an exported namespace label, the
//! plain labels that a scrape target adds belong to the exporter, and
//! this module reads only the exported labels and the labels that no
//! scrape target adds.

use std::collections::{BTreeMap, HashSet};

use super::source::Source;
use transect_graph::edges::Object;

/// What an alert is about.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Target {
    /// A namespace, and the object in it that the labels name, if any.
    /// The object is the subject of the alert's reason in health.
    Namespace {
        namespace: String,
        object: Option<Object>,
        container: Option<String>,
    },
    /// A machine, by the name of its `Node`.
    Machine(String),
    /// The labels name nothing in the cluster, or name nothing at all.
    Cluster,
}

/// The things in the cluster that an alert can attach to, and the
/// conventions that the labels of its alerts follow.
#[derive(Default)]
pub struct Known {
    pub namespaces: HashSet<String>,
    /// Each `Node`'s name, with its addresses.  An instance label may
    /// name one of the addresses in place of the name.
    pub nodes: Vec<(String, Vec<String>)>,
    pub source: Source,
}

/// What the alert with `labels` is about, among the things that `known`
/// lists.
///
/// A named object, such as a `Deployment` or a claim, takes precedence
/// over a machine, and a machine takes precedence over a namespace and
/// pod with no other object.  The reason is that a series that names a
/// machine and only a pod most often comes from an agent or an exporter
/// that reports about that machine, such as an exporter of the `Node`'s
/// own conditions.  An instance label names a machine only when no
/// other label does.
pub fn target(labels: &BTreeMap<String, String>, known: &Known) -> Target {
    let source = &known.source;
    let read = Labels::of(labels, source);
    let namespace = read.namespace().filter(|ns| known.namespaces.contains(*ns));
    let machines = source
        .node_labels
        .iter()
        .filter_map(|label| labels.get(label));
    let node = machines.into_iter().find(|node| known.has_node(node));
    match (namespace, node) {
        (Some(namespace), _) if read.names_beyond_a_pod() => read.in_namespace(namespace),
        (_, Some(node)) => Target::Machine(node.clone()),
        (Some(namespace), None) => read.in_namespace(namespace),
        (None, None) => source
            .instance_labels
            .iter()
            .filter_map(|label| labels.get(label))
            .find_map(|instance| known.machine_at(instance))
            .map_or(Target::Cluster, Target::Machine),
    }
}

/// An alert's labels.  When they include the exported namespace label,
/// `Labels` reads them as the labels of an exporter's series.
struct Labels<'a> {
    labels: &'a BTreeMap<String, String>,
    source: &'a Source,
    exported: bool,
}

impl<'a> Labels<'a> {
    fn of(labels: &'a BTreeMap<String, String>, source: &'a Source) -> Self {
        let exported =
            exported(&source.namespace, source).is_some_and(|key| labels.contains_key(&key));
        Self {
            labels,
            source,
            exported,
        }
    }

    fn scrape_label(&self, key: &str) -> bool {
        self.source.scrape_labels.iter().any(|label| label == key)
    }

    fn get(&self, key: &str) -> Option<&'a str> {
        let exported = || self.labels.get(&exported(key, self.source)?);
        let plain = || match self.exported && self.scrape_label(key) {
            true => None,
            false => self.labels.get(key),
        };
        let value = if self.exported { exported() } else { None };
        value.or_else(plain).map(String::as_str)
    }

    fn namespace(&self) -> Option<&'a str> {
        let label = &self.source.namespace;
        match self.exported {
            true => self.get(label),
            false => self.labels.get(label).map(String::as_str),
        }
    }

    /// Whether the labels name an object other than a pod.  The labels
    /// of a node agent's own series never do.
    fn names_beyond_a_pod(&self) -> bool {
        let objects = self.source.objects.iter();
        self.exported
            || objects.into_iter().any(|object| {
                !self.scrape_label(&object.label) && self.get(&object.label).is_some()
            })
    }

    fn in_namespace(&self, namespace: &str) -> Target {
        let object = self.source.objects.iter().find_map(|object| {
            let name = self.get(&object.label)?;
            Some(Object::new(
                &object.group,
                &object.kind,
                Some(namespace),
                name,
            ))
        });
        let container = self.source.container.as_deref();
        let container = object
            .as_ref()
            .filter(|object| object.group.is_empty() && object.kind == "Pod")
            .and_then(|_| self.get(container?))
            .map(Into::into);
        Target::Namespace {
            namespace: namespace.into(),
            object,
            container,
        }
    }
}

/// The exported form of the label `key`, when the source names a
/// prefix.
fn exported(key: &str, source: &Source) -> Option<String> {
    let prefix = source.exported_prefix.as_deref()?;
    Some(format!("{prefix}{key}"))
}

impl Known {
    fn has_node(&self, name: &str) -> bool {
        self.nodes.iter().any(|(node, _)| node == name)
    }

    /// The machine an `instance` label names: a Node's name or one of
    /// its addresses, with or without a port, or a host name whose first
    /// label is a Node's name.
    fn machine_at(&self, instance: &str) -> Option<String> {
        let host = host_of(instance);
        let first = host.split('.').next().unwrap_or(host);
        let is_ip = host.parse::<std::net::IpAddr>().is_ok();
        self.nodes
            .iter()
            .find(|(name, addresses)| {
                name == host
                    || (!is_ip && name == first)
                    || addresses.iter().any(|address| address == host)
            })
            .map(|(name, _)| name.clone())
    }
}

/// `instance` without its port: `host:port`, `[v6]:port`, or a bare
/// host or address.
fn host_of(instance: &str) -> &str {
    if let Some(rest) = instance.strip_prefix('[') {
        return rest.split(']').next().unwrap_or(rest);
    }
    match instance.rsplit_once(':') {
        Some((host, port)) if !host.contains(':') && port.parse::<u16>().is_ok() => host,
        _ => instance,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn known() -> Known {
        Known {
            namespaces: ["shop", "monitoring", "agents"].map(Into::into).into(),
            nodes: vec![
                ("alpha".into(), vec!["10.0.0.1".into(), "alpha".into()]),
                ("beta".into(), vec!["10.0.0.2".into()]),
            ],
            source: crate::alerts::source::fixtures::source(),
        }
    }

    fn labels(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect()
    }

    fn in_shop(object: Option<(&str, &str, &str)>, container: Option<&str>) -> Target {
        Target::Namespace {
            namespace: "shop".into(),
            object: object.map(|(group, kind, name)| Object::new(group, kind, Some("shop"), name)),
            container: container.map(Into::into),
        }
    }

    fn machine(name: &str) -> Target {
        Target::Machine(name.into())
    }

    /// Label sets in the shapes that common rules produce: a pod's own
    /// exporter, an exporter of other objects' state with and without
    /// `honor_labels`, a node exporter scraped by address or by host
    /// name, and a rule whose sum removes every label.
    #[rstest]
    #[case::a_namespace(&[("namespace", "shop")], in_shop(None, None))]
    #[case::a_pods_own_exporter(
        &[("namespace", "shop"), ("pod", "db-0"), ("container", "metrics"), ("instance", "10.42.0.9:9121"), ("job", "shop/db")],
        in_shop(Some(("", "Pod", "db-0")), Some("metrics"))
    )]
    #[case::a_service(&[("namespace", "shop"), ("service", "web")], in_shop(Some(("", "Service", "web")), None))]
    #[case::exported_by_a_state_exporter(
        &[("namespace", "monitoring"), ("pod", "state-exporter-7d9"), ("container", "state-exporter"),
          ("exported_namespace", "shop"), ("exported_pod", "web-0"), ("exported_container", "app")],
        in_shop(Some(("", "Pod", "web-0")), Some("app"))
    )]
    #[case::exported_without_a_pod(
        &[("namespace", "monitoring"), ("pod", "state-exporter-7d9"), ("exported_namespace", "shop"), ("daemonset", "agent")],
        in_shop(Some(("apps", "DaemonSet", "agent")), None)
    )]
    #[case::a_job_by_job_name(
        &[("exported_namespace", "shop"), ("job_name", "report-29"), ("job", "state-exporter")],
        in_shop(Some(("batch", "Job", "report-29")), None)
    )]
    #[case::the_job_label_is_the_scrape_job(&[("namespace", "shop"), ("job", "web")], in_shop(None, None))]
    #[case::honoring_labels(
        &[("namespace", "shop"), ("deployment", "web"), ("job", "state-exporter"), ("instance", "10.42.0.7:8080")],
        in_shop(Some(("apps", "Deployment", "web")), None)
    )]
    #[case::a_claim_on_a_machine(
        &[("namespace", "shop"), ("persistentvolumeclaim", "data-db-0"), ("node", "alpha")],
        in_shop(Some(("", "PersistentVolumeClaim", "data-db-0")), None)
    )]
    #[case::an_exported_volume_on_a_machine(
        &[("namespace", "agents"), ("pod", "csi-node-x"), ("node", "alpha"), ("exported_namespace", "shop")],
        in_shop(None, None)
    )]
    #[case::a_nodes_condition(
        &[("namespace", "monitoring"), ("pod", "state-exporter-7d9"), ("node", "beta"), ("condition", "Ready")],
        machine("beta")
    )]
    #[case::a_node_agent(&[("namespace", "agents"), ("pod", "radio-agent-x"), ("node", "alpha")], machine("alpha"))]
    #[case::a_node(&[("node", "alpha")], machine("alpha"))]
    #[case::an_instance_by_host_name(&[("instance", "alpha.home.example:9100")], machine("alpha"))]
    #[case::an_instance_by_name(&[("instance", "beta")], machine("beta"))]
    #[case::an_instance_by_address(&[("instance", "10.0.0.2:6443")], machine("beta"))]
    #[case::an_instance_by_v6_address(&[("instance", "[fd00::5]:9100")], Target::Cluster)]
    #[case::a_pod_address(&[("instance", "10.42.0.9:9121")], Target::Cluster)]
    #[case::an_unknown_namespace(&[("namespace", "gone"), ("pod", "web-0")], Target::Cluster)]
    #[case::an_unknown_node(&[("node", "gamma")], Target::Cluster)]
    #[case::nothing(&[("alertname", "QueueBackingUp")], Target::Cluster)]
    fn finds_what_an_alert_is_about(#[case] pairs: &[(&str, &str)], #[case] expected: Target) {
        assert_eq!(target(&labels(pairs), &known()), expected);
    }

    #[rstest]
    #[case::host_and_port("alpha:9100", "alpha")]
    #[case::bare("alpha", "alpha")]
    #[case::v4("10.0.0.1:80", "10.0.0.1")]
    #[case::v6_with_port("[fd00::1]:80", "fd00::1")]
    #[case::v6_bare("fd00::1", "fd00::1")]
    fn reads_the_host_of_an_instance(#[case] instance: &str, #[case] expected: &str) {
        assert_eq!(host_of(instance), expected);
    }
}
