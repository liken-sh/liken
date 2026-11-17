//! The tags the cluster map puts on parcels when a namespace is
//! selected, from a cluster built by hand.

use std::sync::Arc;

use k8s_openapi::api::core::v1::{Namespace, Pod};
use k8s_openapi::api::networking::v1::NetworkPolicy;
use rstest::rstest;

use super::{Focus, cluster_page};
use crate::web::model::Model;
use crate::web::snapshot::Snapshot;
use transect_cluster::ownership::{Entry, Ownership};
use transect_graph::edges::Shown;
use transect_map::map::relation::Relation;
use transect_observe::usage;
use transect_resources::layers::Layers;

fn parse<T: serde::de::DeserializeOwned>(yaml: &str) -> Arc<T> {
    Arc::new(serde_yaml::from_str(yaml).unwrap())
}

/// One pod named `app` in `namespace`, at `ip`.
fn pod(namespace: &str, app: &str, ip: &str) -> Arc<Pod> {
    parse(&format!(
        "metadata: {{namespace: {namespace}, name: {app}, uid: {app}, labels: {{app: {app}}}}}
spec: {{containers: [{{name: main, image: example.test/{app}}}]}}
status: {{phase: Running, podIPs: [{{ip: '{ip}'}}]}}"
    ))
}

/// The tags on each parcel with `shop` selected, when `ingress` is the
/// one rule of a policy that isolates every pod in `shop`.
fn tags(ingress: &str) -> Vec<(String, Vec<Relation>)> {
    let mut snapshot = Snapshot::empty();
    snapshot.namespaces = vec![
        parse::<Namespace>("metadata: {name: shop}"),
        parse("metadata: {name: blog, labels: {team: ops}}"),
        parse("metadata: {name: watch}"),
    ];
    snapshot.pods = vec![
        pod("shop", "db", "10.0.0.1"),
        pod("blog", "web", "10.0.0.2"),
        pod("watch", "probe", "10.0.0.3"),
    ];
    snapshot.network_policies = vec![parse::<NetworkPolicy>(&format!(
        "metadata: {{namespace: shop, name: isolate}}
spec: {{podSelector: {{}}, policyTypes: [Ingress], ingress: [{ingress}]}}"
    ))];
    let entries = snapshot.pods.iter().map(|pod| Entry {
        group: "",
        kind: "Pod",
        meta: &pod.metadata,
    });
    snapshot.ownership = Ownership::build(entries);
    let model = Model::build(snapshot);
    let page = cluster_page(
        &model,
        &Layers::default(),
        &usage::Reading::Off,
        Focus::Select(Some("shop")),
        &Shown::default(),
        k8s_openapi::jiff::Timestamp::now(),
    );
    let parcels = page.map.bands.iter().flat_map(|band| &band.parcels);
    let tagged = parcels.filter(|parcel| !parcel.relations.is_empty());
    tagged
        .map(|parcel| (parcel.namespace.clone(), parcel.relations.clone()))
        .collect()
}

#[rstest]
#[case::a_namespace_a_policy_names(
    "{from: [{namespaceSelector: {matchLabels: {team: ops}}}]}",
    vec![("blog".to_string(), vec![Relation::may_call()])]
)]
#[case::every_namespace("{from: [{namespaceSelector: {}}]}", vec![])]
fn tags_only_the_callers_a_policy_names(
    #[case] ingress: &str,
    #[case] expected: Vec<(String, Vec<Relation>)>,
) {
    assert_eq!(tags(ingress), expected);
}
