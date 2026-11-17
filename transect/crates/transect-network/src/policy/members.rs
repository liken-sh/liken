//! The pods that policy is evaluated over, grouped into members.  A
//! member is the pods of one top owner that have the same labels.
//!
//! Every selector in a policy reads labels and namespaces, so pods of
//! one workload with the same labels match the same rules, and the
//! evaluation runs once for each member instead of once for each pod.
//! A selector reads only the label keys it names, so before the pods
//! are grouped, each pod's labels are reduced to the keys that some
//! policy's pod selector names.  Without that step, the label that a
//! `StatefulSet` or a `Job` sets on each pod with the pod's own name
//! would make each pod a member, and the evaluation, which compares
//! members in pairs, would grow with pairs of pods.  Only an `ipBlock`
//! reads a pod's own address, so a member keeps every address its pods
//! have, and a block matches the member when it contains any of them.
//!
//! A workload with no running pod, such as a `CronJob` between runs or
//! a `Deployment` scaled to zero, is a member by its pod template's
//! labels, with no address.  Its pods will match the same selectors
//! when they run, and a backup job that calls a database is a caller
//! that a card should list between its runs too.

use std::collections::{BTreeMap, BTreeSet};
use std::net::IpAddr;

use k8s_openapi::api::core::v1::{Pod, PodSpec};
use k8s_openapi::api::networking::v1::{NetworkPolicy, NetworkPolicyPeer};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::LabelSelector;

use super::ports::{DEFAULT_PROTOCOL, NamedPort};
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;
use transect_graph::pods::finished;

#[derive(Clone, Debug, PartialEq)]
pub struct Member {
    /// The top owner of its pods, the workload a card names.
    pub owner: Object,
    /// The namespace its pods run in, which a cluster-scoped owner does
    /// not name.
    pub namespace: String,
    pub labels: BTreeMap<String, String>,
    pub addresses: Vec<IpAddr>,
    /// The ports its containers name, which a rule's named port
    /// resolves to when a connection reaches it.
    pub named: Vec<NamedPort>,
    /// Whether its pods are on their machine's network, where no
    /// `NetworkPolicy` selects them.
    pub host_network: bool,
}

/// A workload's pod templates, which make it a member when it has no
/// running pod.
pub struct Idle<'a> {
    pub owner: &'a Object,
    pub labels: Vec<&'a BTreeMap<String, String>>,
    pub specs: Vec<&'a PodSpec>,
}

/// The label keys any pod selector of `policies` names, in the pods a
/// policy applies to or in its peers.
pub fn read_keys<'a>(policies: &[&'a NetworkPolicy]) -> BTreeSet<&'a str> {
    let mut keys = BTreeSet::new();
    for spec in policies.iter().filter_map(|policy| policy.spec.as_ref()) {
        let ingress = spec.ingress.iter().flatten();
        let from = ingress.flat_map(|rule| rule.from.iter().flatten());
        let egress = spec.egress.iter().flatten();
        let to = egress.flat_map(|rule| rule.to.iter().flatten());
        let peers = from
            .chain(to)
            .filter_map(|peer: &NetworkPolicyPeer| peer.pod_selector.as_ref());
        for selector in spec.pod_selector.iter().chain(peers) {
            keys.extend(named_keys(selector));
        }
    }
    keys
}

fn named_keys(selector: &LabelSelector) -> impl Iterator<Item = &str> {
    let labels = selector.match_labels.iter().flatten().map(|(key, _)| key);
    let expressions = selector.match_expressions.iter().flatten();
    labels
        .chain(expressions.map(|expression| &expression.key))
        .map(String::as_str)
}

/// `labels` reduced to the keys in `keys`.
fn read<'a>(
    labels: impl IntoIterator<Item = (&'a String, &'a String)>,
    keys: &BTreeSet<&str>,
) -> BTreeMap<String, String> {
    labels
        .into_iter()
        .filter(|(key, _)| keys.contains(key.as_str()))
        .map(|(key, value)| (key.clone(), value.clone()))
        .collect()
}

/// The members of `pods`, then of each of `idle` that has none, with
/// only the labels in `keys`.  A pod that has finished, such as a
/// `Job`'s, has no address that a connection could reach, so the
/// function omits it.
pub fn members<'a>(
    pods: impl IntoIterator<Item = &'a Pod>,
    idle: impl IntoIterator<Item = Idle<'a>>,
    keys: &BTreeSet<&str>,
    ownership: &Ownership,
) -> Vec<Member> {
    let mut found: BTreeMap<(Object, BTreeMap<String, String>), Member> = BTreeMap::new();
    for pod in pods {
        if finished(pod) {
            continue;
        }
        let (Some(namespace), Some(name)) = (&pod.metadata.namespace, &pod.metadata.name) else {
            continue;
        };
        let owner = ownership.top_owner(&Object::new("", "Pod", Some(namespace), name));
        let labels = read(pod.metadata.labels.iter().flatten(), keys);
        let member = found
            .entry((owner.clone(), labels.clone()))
            .or_insert_with(|| Member {
                owner,
                namespace: namespace.clone(),
                labels,
                addresses: Vec::new(),
                named: pod.spec.iter().flat_map(named_ports).collect(),
                host_network: false,
            });
        member.host_network |= pod.spec.as_ref().is_some_and(on_machine_network);
        member.addresses.extend(addresses(pod));
    }
    let running: BTreeSet<Object> = found.keys().map(|(owner, _)| owner.clone()).collect();
    for idle in idle
        .into_iter()
        .filter(|idle| !running.contains(idle.owner))
    {
        let Some(namespace) = &idle.owner.namespace else {
            continue;
        };
        let host_network = idle.specs.iter().any(|spec| on_machine_network(spec));
        let named: Vec<NamedPort> = idle
            .specs
            .iter()
            .flat_map(|spec| named_ports(spec))
            .collect();
        // A template with no labels, as a `CronJob`'s often has, still
        // makes pods that an empty pod selector matches.
        let none = BTreeMap::new();
        let unlabelled = (idle.labels.is_empty() && !idle.specs.is_empty()).then_some(&none);
        for labels in idle.labels.into_iter().chain(unlabelled) {
            let labels = read(labels, keys);
            found
                .entry((idle.owner.clone(), labels.clone()))
                .or_insert_with(|| Member {
                    owner: idle.owner.clone(),
                    namespace: namespace.clone(),
                    labels: labels.clone(),
                    addresses: Vec::new(),
                    named: named.clone(),
                    host_network,
                });
        }
    }
    found.into_values().collect()
}

fn on_machine_network(spec: &PodSpec) -> bool {
    spec.host_network == Some(true)
}

fn addresses(pod: &Pod) -> impl Iterator<Item = IpAddr> + '_ {
    let status = pod.status.iter();
    let listed = status.flat_map(|status| status.pod_ips.iter().flatten());
    listed.filter_map(|address| address.ip.parse().ok())
}

/// The named ports of every container.  The init containers are
/// included, because a sidecar among them serves while the pod runs.
fn named_ports(spec: &PodSpec) -> Vec<NamedPort> {
    let containers = spec.containers.iter();
    containers
        .chain(spec.init_containers.iter().flatten())
        .flat_map(|container| container.ports.iter().flatten())
        .filter_map(|port| {
            Some(NamedPort {
                name: port.name.clone()?,
                protocol: port.protocol.clone().unwrap_or(DEFAULT_PROTOCOL.into()),
                number: u16::try_from(port.container_port).ok()?,
            })
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
    use rstest::rstest;

    use super::*;
    use transect_cluster::ownership::Entry;

    /// A running pod of the `StatefulSet` `db`, with the label that names
    /// the pod itself.
    fn replica(name: &str) -> Pod {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: {name}, labels: {{app: db, statefulset.kubernetes.io/pod-name: {name}}},
  ownerReferences: [{{apiVersion: apps/v1, kind: StatefulSet, name: db, uid: db, controller: true}}]}}
status: {{phase: Running}}"
        ))
        .unwrap()
    }

    fn policy(selector: &str) -> NetworkPolicy {
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: shop, name: p}}\nspec: {{podSelector: {selector}}}"
        ))
        .unwrap()
    }

    #[rstest]
    #[case::by_app("{matchLabels: {app: db}}", 1)]
    #[case::by_pod_name("{matchLabels: {statefulset.kubernetes.io/pod-name: db-0}}", 2)]
    #[case::by_an_expression(
        "{matchExpressions: [{key: statefulset.kubernetes.io/pod-name, operator: Exists}]}",
        2
    )]
    fn folds_pods_that_differ_only_in_labels_no_policy_reads(
        #[case] selector: &str,
        #[case] count: usize,
    ) {
        let pods = [replica("db-0"), replica("db-1")];
        let set: ObjectMeta = serde_yaml::from_str("{namespace: shop, name: db, uid: db}").unwrap();
        let entries = pods.iter().map(|pod| Entry {
            group: "",
            kind: "Pod",
            meta: &pod.metadata,
        });
        let set = Entry {
            group: "apps",
            kind: "StatefulSet",
            meta: &set,
        };
        let ownership = Ownership::build(entries.chain([set]));
        let policy = policy(selector);
        let keys = read_keys(&[&policy]);
        assert_eq!(members(&pods, [], &keys, &ownership).len(), count);
    }
}
