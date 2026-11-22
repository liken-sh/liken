//! Tests of the cluster page when a control-plane `Node` is lost, using
//! `Node` objects built in the test. A lost machine shows the loss in
//! the sidebar. A cordoned machine that is still ready, and a selected
//! namespace, do not.

use std::sync::Arc;

use k8s_openapi::api::core::v1::{Namespace, Node};
use k8s_openapi::jiff::{Timestamp, ToSpan};
use rstest::rstest;

use super::{Focus, cluster_page};
use crate::web::model::Model;
use crate::web::snapshot::Snapshot;
use transect_observe::usage;
use transect_resources::layers::Layers;

/// A control-plane `Node` named `name`, cordoned when `cordoned`, whose
/// `Ready` condition has the status `ready` and changed 200 seconds ago.
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

/// Builds the cluster page of the control-plane machines alpha, beta,
/// and gamma, with `focus` applied and beta's `Ready` status set to
/// `ready`. Returns the loss's title, its majority line, and the tag of
/// each lost machine, or `None` when the page shows no loss.
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
#[case::a_hypothetical_loss_added_to_a_real_one(
    "Unknown",
    false,
    Focus::Lose(&["alpha".to_string()], None),
    lost(
        "If alpha were lost too",
        "1 of 3 control-plane machines remains, which loses the majority (a guess).",
        &["if lost", "Lost 3m"]
    )
)]
#[case::a_selected_namespace_shows_its_card_instead(
    "Unknown",
    false,
    Focus::Select(Some("shop")),
    None
)]
fn shows_the_loss_in_the_sidebar_only_for_a_lost_machine(
    #[case] ready: &str,
    #[case] cordoned: bool,
    #[case] focus: Focus,
    #[case] expected: Option<(String, Option<String>, Vec<String>)>,
) {
    assert_eq!(page(ready, cordoned, focus), expected);
}
