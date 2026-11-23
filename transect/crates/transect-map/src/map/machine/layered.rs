//! The pods on one machine, laid out like the cluster map: a band per
//! layer in the map's order, a box per namespace inside each band, and
//! a row per pod. A DaemonSet runs a pod on every machine it covers, so
//! the row of a DaemonSet's pod names the DaemonSet and links to the
//! namespace page, which shows the DaemonSet's pods on every machine
//! side by side.

use std::collections::BTreeMap;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;

use super::super::workload::tiles::{PodTile, State, tile};
use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Object, Plurals, kind_and_name};
use transect_graph::links::Reference;
use transect_graph::pods::finished;
use transect_observe::usage::Usage;

/// One layer's namespaces on the machine.
#[derive(Debug, PartialEq)]
pub struct Band {
    pub name: String,
    pub namespaces: Vec<Namespace>,
}

/// One namespace's pods on the machine.
#[derive(Debug, PartialEq)]
pub struct Namespace {
    pub name: String,
    pub rows: Vec<PodRow>,
}

/// The most rows that a namespace's box shows in one column. A box with
/// more rows spans the width of its band and lays its rows out in
/// several columns, so a long namespace does not leave the band's other
/// columns empty.
const ONE_COLUMN: usize = 6;

impl Namespace {
    pub fn wide(&self) -> bool {
        self.rows.len() > ONE_COLUMN
    }
}

/// One pod on the machine, or the DaemonSet whose pod it is.
#[derive(Debug, PartialEq)]
pub struct PodRow {
    /// The pod's short name, or the DaemonSet's kind and name for a
    /// DaemonSet's pod, with a link to its page.
    pub name: Reference,
    /// The pod's top owner, if it has one. For a DaemonSet's pod, it is
    /// the text "one per machine" with a link to the namespace page.
    pub workload: Option<Reference>,
    /// The word between the name and the workload. A DaemonSet's row has
    /// none, and the page draws "one per machine" as a tag after the
    /// name.
    pub joiner: Option<&'static str>,
    pub tile: PodTile,
}

impl PodRow {
    /// A pod that is not ready gets the same `--warm` outline as a pod
    /// tile that is not ready.
    pub fn class(&self) -> &'static str {
        match self.tile.state {
            State::NotReady => "machine-pod machine-pod-not-ready",
            State::Ready | State::Finished => "machine-pod",
        }
    }
}

/// What `runs_here` reads besides the pods.
pub struct Reads<'a> {
    pub ownership: &'a Ownership,
    pub plurals: &'a Plurals,
    pub usage: Option<&'a Usage>,
    pub now: Timestamp,
    /// The band that contains each namespace on the map.
    pub layer_of: &'a dyn Fn(&str) -> String,
    /// The bands in the map's order, top to bottom.
    pub order: &'a [String],
}

/// The pods in `pods` that run on `machine` and have not finished,
/// grouped by band and then by namespace.
pub fn runs_here<'a>(
    pods: impl IntoIterator<Item = &'a Pod>,
    machine: &str,
    reads: &Reads,
) -> Vec<Band> {
    let mut found: BTreeMap<String, Vec<PodRow>> = BTreeMap::new();
    for pod in pods {
        let on = pod.spec.as_ref().and_then(|spec| spec.node_name.as_deref());
        if on != Some(machine) || finished(pod) {
            continue;
        }
        let (Some(namespace), Some(name)) = (&pod.metadata.namespace, &pod.metadata.name) else {
            continue;
        };
        let me = Object::new("", "Pod", Some(namespace), name);
        let owner = reads.ownership.top_owner(&me);
        let row = row(pod, &me, &owner, reads);
        found.entry(namespace.clone()).or_default().push(row);
    }
    let mut bands: Vec<Band> = Vec::new();
    for (namespace, mut rows) in found {
        rows.sort_by(|a, b| sort_key(a).cmp(&sort_key(b)));
        let layer = (reads.layer_of)(&namespace);
        let namespace = Namespace {
            name: namespace,
            rows,
        };
        match bands.iter_mut().find(|band| band.name == layer) {
            Some(band) => band.namespaces.push(namespace),
            None => bands.push(Band {
                name: layer,
                namespaces: vec![namespace],
            }),
        }
    }
    let place = |band: &Band| reads.order.iter().position(|name| *name == band.name);
    bands.sort_by_key(|band| place(band).unwrap_or(usize::MAX));
    bands
}

fn row(pod: &Pod, me: &Object, owner: &Object, reads: &Reads) -> PodRow {
    let tile = tile(pod, reads.usage, reads.now);
    let namespace = me.namespace.as_deref().unwrap_or_default();
    if owner.kind == "DaemonSet" && owner.group == "apps" {
        return PodRow {
            name: Reference {
                text: kind_and_name(&owner.kind, &owner.name),
                href: transect_graph::links::link(owner, reads.plurals),
            },
            workload: Some(Reference {
                text: "one per machine".into(),
                href: Some(transect_graph::links::namespace(namespace)),
            }),
            joiner: None,
            tile,
        };
    }
    let workload = (owner != me).then(|| Reference {
        text: kind_and_name(&owner.kind, &owner.name),
        href: transect_graph::links::link(owner, reads.plurals),
    });
    PodRow {
        name: Reference {
            text: tile.short_name.clone(),
            href: transect_graph::links::link(me, reads.plurals),
        },
        workload,
        joiner: Some("of"),
        tile,
    }
}

/// Rows sort by workload, so a workload's pods are listed together, and
/// then by name. A pod with no owner sorts by its own name.
fn sort_key(row: &PodRow) -> (&str, &str) {
    let workload = row.workload.as_ref().map(|w| w.text.as_str());
    (workload.unwrap_or(&row.name.text), &row.name.text)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_cluster::ownership::Entry;
    use transect_cluster::ownership::fixtures::meta;

    /// A pod of `owner`, by its kind and name, in `namespace`, on
    /// `machine`, running unless `phase` is one that finishes a pod.
    fn pod(
        namespace: &str,
        name: &str,
        owner: Option<(&str, &str)>,
        machine: &str,
        phase: &str,
    ) -> Pod {
        let mut pod: Pod = serde_yaml_ng::from_str(&format!(
            "spec: {{nodeName: {machine}, containers: []}}\nstatus: {{phase: {phase}}}"
        ))
        .unwrap();
        let owners: Vec<(&str, &str, &str)> = owner
            .map(|(kind, owner)| ("apps/v1", kind, owner))
            .into_iter()
            .collect();
        pod.metadata = meta(Some(namespace), name, &owners);
        pod
    }

    /// Each band's name, and each of its namespaces' names and rows:
    /// the row's text and link, and its workload's text and link.
    type Read = (String, Vec<(String, Vec<Row>)>);
    type Row = (String, Option<String>, Option<(String, Option<String>)>);

    fn read(pods: &[Pod]) -> Vec<Read> {
        let entries = pods.iter().map(|pod| Entry {
            group: "",
            kind: "Pod",
            meta: &pod.metadata,
        });
        let ownership = Ownership::build(entries);
        let layer_of = |namespace: &str| match namespace {
            "shop" => "apps".to_string(),
            "agents" => "platform".to_string(),
            _ => crate::map::UNSORTED.to_string(),
        };
        let order = ["apps".to_string(), "platform".to_string()];
        let reads = Reads {
            ownership: &ownership,
            plurals: &Plurals::default(),
            usage: None,
            now: "2026-01-01T12:00:00Z".parse().unwrap(),
            layer_of: &layer_of,
            order: &order,
        };
        let row = |row: PodRow| {
            let workload = row.workload.map(|w| (w.text, w.href));
            (row.name.text, row.name.href, workload)
        };
        let namespace = |n: Namespace| (n.name, n.rows.into_iter().map(row).collect());
        let band = |b: Band| (b.name, b.namespaces.into_iter().map(namespace).collect());
        runs_here(pods, "alpha", &reads)
            .into_iter()
            .map(band)
            .collect()
    }

    fn text(text: &str, href: &str) -> Option<(String, Option<String>)> {
        Some((text.into(), Some(href.into())))
    }

    #[rstest]
    #[case::in_the_maps_order(
        vec![
            pod("legacy", "old-1", None, "alpha", "Running"),
            pod("agents", "agent-x1", Some(("DaemonSet", "agent")), "alpha", "Running"),
            pod("shop", "db-0", Some(("StatefulSet", "db")), "alpha", "Running"),
        ],
        vec![
            ("apps".into(), vec![("shop".into(), vec![(
                "db-0".into(),
                Some("/namespaces/shop/pods/db-0".into()),
                text("StatefulSet db", "/namespaces/shop/statefulsets/db"),
            )])]),
            ("platform".into(), vec![("agents".into(), vec![(
                "DaemonSet agent".into(),
                Some("/namespaces/agents/daemonsets/agent".into()),
                text("one per machine", "/namespaces/agents"),
            )])]),
            (crate::map::UNSORTED.into(), vec![("legacy".into(), vec![(
                "old-1".into(),
                Some("/namespaces/legacy/pods/old-1".into()),
                None,
            )])]),
        ]
    )]
    #[case::not_another_machines_or_a_finished_pod(
        vec![
            pod("shop", "web-1", None, "beta", "Running"),
            pod("shop", "job-1", None, "alpha", "Succeeded"),
        ],
        vec![]
    )]
    fn lays_out_its_running_pods_by_band(#[case] pods: Vec<Pod>, #[case] expected: Vec<Read>) {
        assert_eq!(read(&pods), expected);
    }
}
