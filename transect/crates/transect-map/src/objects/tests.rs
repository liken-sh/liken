use rstest::rstest;

use super::*;
use transect_resources::knowledge::fixtures::Nowhere;

pub fn resource(group: &str, kind: &str, plural: &str) -> ApiResource {
    ApiResource {
        group: group.into(),
        version: "v1".into(),
        api_version: format!("{group}/v1"),
        kind: kind.into(),
        plural: plural.into(),
    }
}

/// An object of `kind` in `namespace`, with `conditions` as its
/// `status.conditions`.
pub fn custom(kind: &str, namespace: &str, name: &str, conditions: &str) -> Arc<Custom> {
    let text = format!(
        "apiVersion: example.io/v1\nkind: {kind}\nmetadata: {{namespace: {namespace}, name: {name}}}\nstatus: {{conditions: {conditions}}}"
    );
    Arc::new(serde_yaml::from_str(&text).unwrap())
}

const READY: &str = "[{type: Ready, status: 'True'}]";
const FAILING: &str = "[{type: Ready, status: 'False'}]";

fn cluster() -> Vec<(ApiResource, Vec<Arc<Custom>>)> {
    vec![
        (
            resource("example.io", "Widget", "widgets"),
            vec![
                custom("Widget", "shop", "b-widget", FAILING),
                custom("Widget", "shop", "a-widget", READY),
                custom("Widget", "blog", "c-widget", READY),
            ],
        ),
        (
            resource("example.io", "Gadget", "gadgets"),
            vec![custom("Gadget", "shop", "runs-pods", "[]")],
        ),
        (
            resource("example.io", "Sprocket", "sprockets"),
            vec![custom("Sprocket", "shop", "plain", "[]")],
        ),
    ]
}

/// A group as its label, its list's URL, and each name with its level.
type Read = (String, String, Vec<(String, Option<Level>)>);

fn texts(groups: &[Group]) -> Vec<Read> {
    groups
        .iter()
        .map(|group| {
            let items = group.items.iter();
            let items = items.map(|item| (item.name.clone(), item.level)).collect();
            (group.label.clone(), group.href.clone(), items)
        })
        .collect()
}

#[rstest]
fn groups_a_namespaces_custom_resources_by_kind() {
    let custom = cluster();
    let kinds = Kinds::default();
    let none = |_: &Object| None;
    let sources = Sources {
        custom: &custom,
        kinds: &kinds,
        ends: &Nowhere,
        href: &none,
    };
    let runs = Object::new("example.io", "Gadget", Some("shop"), "runs-pods");
    let groups = sources.groups("shop", &BTreeSet::from([&runs]));
    assert_eq!(
        texts(&groups),
        vec![
            (
                "Sprockets".into(),
                "/namespaces/shop/sprockets.example.io".into(),
                vec![("plain".into(), None)]
            ),
            (
                "Widgets".into(),
                "/namespaces/shop/widgets.example.io".into(),
                vec![
                    ("a-widget".into(), Some(Level::Healthy)),
                    ("b-widget".into(), Some(Level::Failing)),
                ]
            ),
        ]
    );
}

#[rstest]
fn shows_the_first_names_of_a_long_group() {
    let objects = (0..15)
        .map(|n| custom("Widget", "shop", &format!("w{n:02}"), READY))
        .collect();
    let custom = vec![(resource("example.io", "Widget", "widgets"), objects)];
    let kinds = Kinds::default();
    let none = |_: &Object| None;
    let sources = Sources {
        custom: &custom,
        kinds: &kinds,
        ends: &Nowhere,
        href: &none,
    };
    let groups = sources.groups("shop", &BTreeSet::new());
    assert_eq!(
        (groups[0].items.len(), groups[0].more_text()),
        (MOST, Some("+3 more".into()))
    );
}

#[rstest]
#[case::one_of_a_label(&[("a.io", "Cluster", "clusters")], &["Clusters"])]
#[case::two_of_a_label(&[("a.io", "Cluster", "clusters"), ("b.io", "Cluster", "clusters")], &["Clusters (a.io)", "Clusters (b.io)"])]
fn labels_each_kind(#[case] kinds: &[(&str, &str, &str)], #[case] expected: &[&str]) {
    let resources: Vec<ApiResource> = kinds
        .iter()
        .map(|(group, kind, plural)| resource(group, kind, plural))
        .collect();
    let found = labels(&resources);
    let mut found: Vec<&str> = found.values().map(String::as_str).collect();
    found.sort();
    assert_eq!(found, expected);
}
