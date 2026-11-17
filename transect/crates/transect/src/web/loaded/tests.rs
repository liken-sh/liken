//! Each way a document of `transect`'s resources can go wrong, and the
//! condition it gets.

use rstest::rstest;

use super::*;
use transect_resources::resources::{Read, sort};

fn one(text: &str) -> Loaded {
    sort(vec![Read {
        path: "a.yaml".into(),
        text: Ok(text.into()),
    }])
}

/// Each document's kind, name, and the reasons of its conditions.
fn read(loaded: &Loaded) -> Vec<(String, String, Vec<&'static str>)> {
    let documents = loaded
        .directory
        .files
        .iter()
        .flat_map(|file| &file.documents);
    documents
        .map(|document| {
            let reasons = document.conditions.iter().map(|c| c.reason).collect();
            (document.kind.clone(), document.name.clone(), reasons)
        })
        .collect()
}

const LAYERS: &str = "
apiVersion: transect.liken.sh/v1alpha1
kind: Layers
metadata: {name: layers}
spec: {layers: [{name: apps}]}
";

#[rstest]
#[case::layers(LAYERS, "Layers", "layers", "Loaded")]
#[case::an_unknown_kind(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: Layer\nmetadata: {name: typo}\n",
    "Layer",
    "typo",
    "UnknownKind"
)]
#[case::a_log_pattern(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: LogPattern\nmetadata: {name: refused}\nspec: {match: refused, severity: error, meaning: It cannot connect.}\n",
    "LogPattern",
    "refused",
    "Loaded"
)]
#[case::a_log_source_with_no_spec(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: LogSource\nmetadata: {name: logs}\n",
    "LogSource",
    "logs",
    "Invalid"
)]
#[case::another_version(
    "apiVersion: transect.liken.sh/v2\nkind: Layers\nmetadata: {name: layers}\n",
    "Layers",
    "layers",
    "UnknownVersion"
)]
#[case::layers_that_do_not_validate(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: Layers\nmetadata: {name: layers}\nspec: {layers: [{name: apps}, {name: apps}]}\n",
    "Layers",
    "layers",
    "Invalid"
)]
#[case::layers_with_no_spec(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: Layers\nmetadata: {name: layers}\n",
    "Layers",
    "layers",
    "Invalid"
)]
#[case::an_intent_with_no_selects(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: Intent\nmetadata: {namespace: shop, name: db}\nspec: {why: Keeps orders.}\n",
    "Intent",
    "shop/db",
    "Invalid"
)]
#[case::kind_knowledge(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: KindKnowledge\nmetadata: {name: widgets.widgets.example.com}\nspec: {healthy: {condition: Ready}}\n",
    "KindKnowledge",
    "widgets.widgets.example.com",
    "Loaded"
)]
#[case::kind_knowledge_with_an_unknown_edge(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: KindKnowledge\nmetadata: {name: widgets.widgets.example.com}\nspec: {references: [{path: spec.a, edge: calls}]}\n",
    "KindKnowledge",
    "widgets.widgets.example.com",
    "Invalid"
)]
#[case::device_knowledge(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: DeviceKnowledge\nmetadata: {name: radio.example.com}\nspec: {title: product}\n",
    "DeviceKnowledge",
    "radio.example.com",
    "Loaded"
)]
#[case::an_outside(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: Outside\nmetadata: {name: a-nas}\nspec: {title: The NAS, addresses: [nas.example.internal]}\n",
    "Outside",
    "a-nas",
    "Loaded"
)]
#[case::an_outside_that_nothing_reaches(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: Outside\nmetadata: {name: a-nas}\nspec: {title: The NAS}\n",
    "Outside",
    "a-nas",
    "Invalid"
)]
#[case::a_registry_mirror(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: RegistryMirror\nmetadata: {name: images}\nspec: {host: images.example, mirror: 'https://mirror.example'}\n",
    "RegistryMirror",
    "images",
    "Loaded"
)]
#[case::a_registry_mirror_that_is_not_a_url(
    "apiVersion: transect.liken.sh/v1alpha1\nkind: RegistryMirror\nmetadata: {name: images}\nspec: {host: images.example, mirror: mirror.example}\n",
    "RegistryMirror",
    "images",
    "Invalid"
)]
#[case::an_overlay(
    "apiVersion: apps/v1\nkind: Deployment\nmetadata: {namespace: shop, name: web, annotations: {transect.liken.sh/why: Sells things.}}\n",
    "Deployment",
    "shop/web",
    "Loaded"
)]
#[case::an_overlay_with_a_spec(
    "apiVersion: apps/v1\nkind: Deployment\nmetadata: {namespace: shop, name: web}\nspec: {replicas: 2}\n",
    "Deployment",
    "shop/web",
    "Invalid"
)]
#[case::not_a_resource("replicas: 2\n", "", "", "Invalid")]
fn gives_each_document_a_condition(
    #[case] text: &str,
    #[case] kind: &str,
    #[case] name: &str,
    #[case] reason: &'static str,
) {
    assert_eq!(
        read(&one(text)),
        vec![(kind.into(), name.into(), vec![reason])]
    );
}

#[rstest]
fn keeps_the_first_of_two_layers() {
    let second = LAYERS.replace("apps", "platform");
    let loaded: Loaded = sort(vec![
        Read {
            path: "a.yaml".into(),
            text: Ok(LAYERS.into()),
        },
        Read {
            path: "b.yaml".into(),
            text: Ok(second),
        },
    ]);
    let reasons: Vec<_> = read(&loaded).into_iter().map(|(.., r)| r).collect();
    assert_eq!(reasons, vec![vec!["Loaded"], vec!["Duplicate"]]);
    assert_eq!(loaded.layers.names().collect::<Vec<_>>(), vec!["apps"]);
}

#[rstest]
fn keeps_the_first_of_two_log_sources() {
    let source = serde_yaml::to_string(&transect_observe::logs::source::fixtures::document(
        transect_observe::logs::source::fixtures::SPEC,
    ))
    .unwrap();
    let loaded = one(&format!(
        "{source}---\n{}",
        source.replace("name: logs", "name: other")
    ));
    let reasons: Vec<_> = read(&loaded).into_iter().map(|(.., r)| r).collect();
    assert_eq!(reasons, vec![vec!["Loaded"], vec!["Duplicate"]]);
    assert_eq!(
        loaded.log_source.map(|source| source.name).as_deref(),
        Some("logs")
    );
}

#[rstest]
fn keeps_the_first_of_two_metrics_sources() {
    let source = serde_yaml::to_string(&transect_observe::metrics::source::fixtures::document(
        transect_observe::metrics::source::fixtures::VOLUME_USED,
    ))
    .unwrap();
    let loaded = one(&format!(
        "{source}---\n{}",
        source.replace("name: metrics", "name: other")
    ));
    let reasons: Vec<_> = read(&loaded).into_iter().map(|(.., r)| r).collect();
    assert_eq!(reasons, vec![vec!["Loaded"], vec!["Duplicate"]]);
    assert_eq!(
        loaded.metrics_source.map(|source| source.name).as_deref(),
        Some("metrics")
    );
}

#[rstest]
fn keeps_the_first_of_two_descriptions_of_one_kind() {
    let widgets = "apiVersion: transect.liken.sh/v1alpha1\nkind: KindKnowledge\nmetadata: {name: widgets.widgets.example.com}\nspec: {}\n";
    let loaded = one(&format!("{widgets}---\n{widgets}"));
    let reasons: Vec<_> = read(&loaded).into_iter().map(|(.., r)| r).collect();
    assert_eq!(reasons, vec![vec!["Loaded"], vec!["Duplicate"]]);
    assert_eq!(loaded.kinds.len(), 1);
}

#[rstest]
#[case::not_yaml(Ok("kind: [unclosed".into()))]
#[case::unreadable(Err("permission denied".into()))]
fn a_file_that_is_not_yaml_has_a_problem_and_no_documents(#[case] text: Result<String, String>) {
    let loaded: Loaded = sort(vec![Read {
        path: "a.yaml".into(),
        text,
    }]);
    assert!(loaded.directory.files[0].problem.is_some());
    assert!(loaded.directory.files[0].documents.is_empty());
}

#[rstest]
fn reads_every_document_of_a_file_and_skips_empty_ones() {
    let text = format!(
        "---\n{LAYERS}---\n# only a comment\n---\napiVersion: v1\nkind: Namespace\nmetadata: {{name: shop}}\n"
    );
    let kinds: Vec<_> = read(&one(&text)).into_iter().map(|(k, ..)| k).collect();
    assert_eq!(kinds, vec!["Layers", "Namespace"]);
}
