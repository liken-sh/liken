//! Tests of the cluster page with alerts firing, rendered to HTML from
//! an alert reading built in the test: the tile that an alert colors,
//! the machine that it marks, and the lines of "Now".

use std::sync::Arc;

use askama::Template;
use k8s_openapi::api::core::v1::{Namespace, Node};
use rstest::rstest;

use super::{Focus, cluster_page};
use crate::web::model::Model;
use crate::web::snapshot::Snapshot;
use transect_observe::alerts::fixtures::alert;
use transect_observe::alerts::{self, Alert};
use transect_observe::usage;
use transect_resources::layers::Layers;

/// The cluster page's HTML for a cluster with the namespace `shop` and
/// the machine `alpha`, where `alerts` is the reading of Alertmanager.
fn page(alerts: alerts::Reading) -> String {
    let mut snapshot = Snapshot::empty();
    snapshot.namespaces = vec![Arc::new(
        serde_yaml_ng::from_str::<Namespace>("metadata: {name: shop}").unwrap(),
    )];
    snapshot.nodes = vec![Arc::new(
        serde_yaml_ng::from_str::<Node>(
            "metadata: {name: alpha}
status: {conditions: [{type: Ready, status: 'True'}]}",
        )
        .unwrap(),
    )];
    snapshot.alerts = alerts;
    let source = transect_observe::metrics::source::fixtures::with_alerts(
        alerts::source::fixtures::source(),
    );
    snapshot.resources = Arc::new(crate::web::loaded::Loaded {
        metrics_source: Some(source),
        ..Default::default()
    });
    let model = Model::build(snapshot);
    let page = cluster_page(
        &model,
        &Layers::default(),
        &usage::Reading::Off,
        Focus::Select(None),
        k8s_openapi::jiff::Timestamp::now(),
    );
    page.render().unwrap()
}

fn read(alerts: Vec<Alert>) -> alerts::Reading {
    alerts::Reading::Read(Arc::new(alerts))
}

fn alert_with_markup() -> Alert {
    let mut down = alert("WebDown", "critical", &[("namespace", "shop")]);
    down.summary = Some("<script>alert(1)</script>".into());
    down.link =
        Some("http://alerts.example/#/alerts?filter=%7Balertname%3D%22WebDown%22%7D".into());
    down
}

#[rstest]
#[case::off(
    alerts::Reading::Off,
    &["No Alertmanager to read: set --alertmanager, or a url in the MetricsSource", "Nothing needs attention"]
)]
#[case::undeclared(
    alerts::Reading::Undeclared,
    &["A MetricsSource with an alerts section would list", "Nothing needs attention"]
)]
#[case::quiet(read(vec![]), &["No alerts firing", "Nothing needs attention"])]
#[case::unreachable(
    alerts::Reading::Failed(alerts::Failure::Unreachable),
    &["Alertmanager unreachable", "Nothing needs attention"]
)]
#[case::firing(
    read(vec![alert_with_markup(), alert("DiskHot", "warning", &[("node", "alpha")])]),
    &[
        " ns-tile-failing",
        "alert(1)",
        "shop, firing for",
        "machine alpha, firing for",
        r#"<span class="strip-tag strip-tag-alert" title="DiskHot">1 alert</span>"#,
        r#"href="http://alerts.example/#/alerts?filter=%7Balertname%3D%22WebDown%22%7D""#,
    ]
)]
fn shows_each_alertmanager_reading(#[case] alerts: alerts::Reading, #[case] expected: &[&str]) {
    let html = page(alerts);
    let missing: Vec<&&str> = expected
        .iter()
        .filter(|text| !html.contains(**text))
        .collect();
    assert!(missing.is_empty(), "the page lacks {missing:?}");
}

#[rstest]
fn a_firing_alert_hides_the_all_clear_and_escapes_its_summary() {
    let html = page(read(vec![alert_with_markup()]));
    let found: Vec<&str> = ["Nothing needs attention", "No alerts firing", "<script>"]
        .into_iter()
        .filter(|text| html.contains(text))
        .collect();
    assert_eq!(found, Vec::<&str>::new());
}
