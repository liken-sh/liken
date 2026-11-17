//! The cluster page under a machine that is really lost, from `Node`
//! objects built by hand, so the tests cover the loss taking over the
//! sidebar without an outage.

use std::sync::Arc;

use k8s_openapi::api::core::v1::{Namespace, Node};
use k8s_openapi::jiff::{Timestamp, ToSpan};
use rstest::rstest;

use super::{Focus, cluster_page};
use crate::web::model::Model;
use crate::web::snapshot::Snapshot;
use transect_graph::edges::Shown;
use transect_observe::usage;
use transect_resources::layers::Layers;

/// A control-plane machine named `name`, cordoned when `cordoned`, whose
/// `Ready` condition is `ready` and last changed three minutes ago.
fn node(name: &str, ready: &str, cordoned: bool) -> Arc<Node> {
    let since = Timestamp::now() - 200.seconds();
    Arc::new(
        serde_yaml::from_str(&format!(
            "metadata: {{name: {name}, labels: {{node-role.kubernetes.io/control-plane: 'true'}}}}
spec: {{unschedulable: {cordoned}}}
status: {{conditions: [{{type: Ready, status: '{ready}', lastTransitionTime: '{since}'}}]}}"
        ))
        .unwrap(),
    )
}

/// The cluster page of alpha, beta, and gamma, with beta's `Ready`
/// condition `ready`, as `focus` asks for it: the loss's title, its
/// majority line, and each lost strip's tag.
fn page(
    ready: &str,
    cordoned: bool,
    focus: Focus,
) -> Option<(String, Option<String>, Vec<String>)> {
    let mut snapshot = Snapshot::empty();
    snapshot.namespaces = vec![Arc::new(
        serde_yaml::from_str::<Namespace>("metadata: {name: shop}").unwrap(),
    )];
    snapshot.nodes = vec![
        node("alpha", "True", false),
        node("beta", ready, cordoned),
        node("gamma", "True", false),
    ];
    let model = Model::build(snapshot);
    let page = cluster_page(
        &model,
        &Layers::default(),
        &usage::Reading::Off,
        focus,
        &Shown::default(),
        k8s_openapi::jiff::Timestamp::now(),
    );
    let tags = page
        .machines
        .iter()
        .filter(|m| m.lost)
        .map(|m| m.lost_tag());
    let tags = tags.collect();
    page.loss
        .map(|loss| (loss.title(), loss.majority_text(), tags))
}

const MAJORITY: &str = "2 of 3 control-plane machines remain, which keeps a majority (a guess).";

fn lost(
    title: &str,
    majority: &str,
    tags: &[&str],
) -> Option<(String, Option<String>, Vec<String>)> {
    let tags = tags.iter().map(|tag| tag.to_string()).collect();
    Some((title.into(), Some(majority.into()), tags))
}

#[rstest]
#[case::every_machine_ready("True", false, Focus::Select(None), None)]
#[case::cordoned_but_ready("True", true, Focus::Select(None), None)]
#[case::not_ready(
    "False",
    false,
    Focus::Select(None),
    lost("beta is lost · 3 minutes", MAJORITY, &["Lost 3m"])
)]
#[case::unknown_and_cordoned(
    "Unknown",
    true,
    Focus::Select(None),
    lost("beta is lost · 3 minutes", MAJORITY, &["Lost 3m"])
)]
#[case::a_question_on_top(
    "Unknown",
    false,
    Focus::Lose(&["alpha".to_string()], None),
    lost(
        "If alpha were lost too",
        "1 of 3 control-plane machines remains, which loses the majority (a guess).",
        &["if lost", "Lost 3m"]
    )
)]
#[case::a_selection_shows_its_card("Unknown", false, Focus::Select(Some("shop")), None)]
fn a_machine_really_lost_takes_over_the_sidebar(
    #[case] ready: &str,
    #[case] cordoned: bool,
    #[case] focus: Focus,
    #[case] expected: Option<(String, Option<String>, Vec<String>)>,
) {
    assert_eq!(page(ready, cordoned, focus), expected);
}
