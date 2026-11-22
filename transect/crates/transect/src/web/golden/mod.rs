//! Golden files of the visible text of pages, rendered from the fixture
//! cluster and its signals at the fixture's `NOW` and stored in
//! `pages/` beside this file.  The pinned time keeps every age the same
//! on every run, so no age needs masking.
//!
//! A golden holds only the words that a reader sees, so a change to
//! markup or style does not affect it.  A change to what a page says
//! fails `cargo test` and prints the lines that changed.  When the
//! change is intended, rewrite the goldens and review their diff in the
//! commit:
//!
//! ```bash
//! TRANSECT_GOLDEN=rewrite cargo test golden
//! ```

mod lens;
mod masthead;
mod matched;
mod text;
mod unloaded;

use std::path::PathBuf;

use axum::extract::Query;
use axum::http::Uri;
use rstest::rstest;

use super::fixture;
use super::render::{Part, Rendered, draw};
use super::view::{self, Page};

/// `url` drawn whole from the fixture cluster, with its usage.
fn render(url: &str) -> String {
    render_from(&fixture::model(), &fixture::usage(), url)
}

/// `url` drawn whole from `model`, with `usage` on its bars.
fn render_from(
    model: &super::model::Model,
    usage: &transect_observe::usage::Reading,
    url: &str,
) -> String {
    let uri: Uri = url.parse().unwrap();
    let Query(query) = Query::<view::Query>::try_from_uri(&uri).unwrap();
    // The server's router decodes percent-escapes in the path before it
    // matches it.
    let path = transect_graph::links::percent_decoded(uri.path().trim_start_matches('/'));
    let page = Page::at(&path, &query).unwrap();
    let reads = transect_map::objects::reads::Reads::default();
    let now = fixture::NOW.parse().unwrap();
    match draw(
        model,
        &page,
        Part::Whole,
        usage,
        &reads,
        fixture::tails,
        now,
    ) {
        Ok(Rendered { html, found: true }) => html,
        _ => panic!("{url} draws no page"),
    }
}

fn golden(name: &str) -> PathBuf {
    let here = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("src/web/golden/pages");
    here.join(format!("{name}.txt"))
}

/// A line diff of `then` against `now`, built from the longest common
/// subsequence of their lines.  `-` marks a line only in `then`, and
/// `+` marks a line only in `now`.
fn diff(then: &str, now: &str) -> String {
    let (a, b): (Vec<&str>, Vec<&str>) = (then.lines().collect(), now.lines().collect());
    let mut shared = vec![vec![0usize; b.len() + 1]; a.len() + 1];
    for i in (0..a.len()).rev() {
        for j in (0..b.len()).rev() {
            shared[i][j] = match a[i] == b[j] {
                true => shared[i + 1][j + 1] + 1,
                false => shared[i + 1][j].max(shared[i][j + 1]),
            };
        }
    }
    let (mut i, mut j, mut out) = (0, 0, String::new());
    while i < a.len() || j < b.len() {
        if i < a.len() && j < b.len() && a[i] == b[j] {
            (i, j) = (i + 1, j + 1);
        } else if j < b.len() && (i == a.len() || shared[i][j + 1] >= shared[i + 1][j]) {
            out.push_str(&format!("+ {}\n", b[j]));
            j += 1;
        } else {
            out.push_str(&format!("- {}\n", a[i]));
            i += 1;
        }
    }
    out
}

#[rstest]
#[case::the_map("map", "/")]
#[case::a_namespace_selected("namespace-selected", "/?select=acme-shop")]
#[case::a_loss("loss", "/?lose=node-alpha")]
#[case::a_machine("machine-alpha", "/machines/node-alpha")]
#[case::a_machine_with_an_alert("machine-beta", "/machines/node-beta")]
#[case::a_namespace("namespace", "/namespaces/acme-ledger")]
#[case::a_workload("workload", "/namespaces/acme-shop/deployments/storefront")]
#[case::its_section("section", "/namespaces/acme-shop/deployments/storefront/section")]
#[case::a_workload_on_its_machines_network(
    "machine-network",
    "/namespaces/acme-ops/deployments/device-hub"
)]
#[case::a_pod("pod", "/namespaces/acme-shop/pods/storefront-rs-one")]
#[case::its_logs("logs", "/namespaces/acme-shop/deployments/storefront/logs")]
#[case::an_ingress(
    "ingress",
    "/namespaces/acme-shop/ingresses.networking.k8s.io/storefront-route"
)]
#[case::a_volume("volume", "/volumes/ledger-archive")]
#[case::a_ground_box("ground", "/ground/archive-nas")]
#[case::a_csi_driver(
    "csi-driver",
    "/cluster/csidrivers.storage.k8s.io/block.csi.example.test"
)]
#[case::what_changed("changed", "/changed")]
#[case::the_resources("resources", "/resources")]
#[case::the_shapes("shapes", "/shapes")]
#[case::the_ways_in("ways-in", "/access")]
#[case::a_workloads_access(
    "workload-access",
    "/namespaces/acme-shop/deployments/storefront/access"
)]
#[case::no_way_in(
    "no-way-in",
    "/namespaces/acme-ledger/statefulsets/ledger-store/access"
)]
#[case::access_on_a_machines_network(
    "machine-network-access",
    "/namespaces/acme-ops/deployments/device-hub/access"
)]
#[case::the_accounts("accounts", "/access/accounts")]
#[case::an_account("account", "/namespaces/acme-ops/serviceaccounts/ops-console")]
#[case::an_account_nothing_follows_for(
    "account-nothing-follows",
    "/namespaces/acme-shop/serviceaccounts/storefront"
)]
#[case::a_group("group", "/access/accounts/groups/system:serviceaccounts:acme-ledger")]
#[case::a_namespaces_access("namespace-access", "/namespaces/acme-shop/access")]
#[case::the_vulnerabilities("vulnerabilities", "/vulnerabilities")]
#[case::the_vulnerabilities_of_one_severity(
    "vulnerabilities-unknown",
    "/vulnerabilities?severity=UNKNOWN"
)]
#[case::one_finding("vulnerability", "/vulnerabilities/CVE-2026-0001")]
#[case::a_workloads_vulnerabilities(
    "workload-vulnerabilities",
    "/namespaces/acme-shop/deployments/listings/vulnerabilities"
)]
#[case::only_a_stale_report(
    "stale-vulnerabilities",
    "/namespaces/acme-ledger/statefulsets/ledger-db/vulnerabilities"
)]
#[case::a_scanners_report_kind("image-reports", "/kinds/imagereports.scan.example.test")]
#[case::a_known_custom_resource(
    "known",
    "/namespaces/acme-shop/promotions.promotions.example.test/spring-sale"
)]
#[case::a_sketch("sketch", "/namespaces/acme-ops/shelves.stock.example.test/top-shelf")]
#[case::another_elevation(
    "sketch-restock",
    "/namespaces/acme-ops/shelves.stock.example.test/top-shelf/elevations/the-restock"
)]
#[case::a_store_with_replicas(
    "store",
    "/namespaces/acme-ledger/ledgers.ledgers.example.test/journal"
)]
#[case::a_store_with_one_instance(
    "store-alone",
    "/namespaces/acme-ledger/ledgers.ledgers.example.test/receipts"
)]
#[case::the_objects_that_name_this_one(
    "room",
    "/namespaces/acme-ops/rooms.stock.example.test/back-room"
)]
#[case::a_role_expected_in_another_state(
    "room-closed",
    "/namespaces/acme-ops/rooms.stock.example.test/front-room"
)]
#[case::a_health_through_a_reference(
    "vent",
    "/namespaces/acme-ops/vents.stock.example.test/west-vent"
)]
#[case::a_kustomization(
    "kustomization",
    "/namespaces/acme-ops/kustomizations.kustomize.toolkit.fluxcd.io/shop-apps"
)]
#[case::a_helm_release(
    "helm-release",
    "/namespaces/acme-shop/helmreleases.helm.toolkit.fluxcd.io/storefront-release"
)]
#[case::a_pinned_source(
    "git-repository",
    "/namespaces/acme-ops/gitrepositories.source.toolkit.fluxcd.io/ledger-git"
)]
fn golden_text(#[case] name: &str, #[case] url: &str) {
    let now = text::visible(&render(url));
    let path = golden(name);
    if std::env::var("TRANSECT_GOLDEN").as_deref() == Ok("rewrite") {
        std::fs::write(&path, &now).unwrap();
        return;
    }
    let then = std::fs::read_to_string(&path).unwrap_or_default();
    assert!(
        then == now,
        "the text of {url} differs from {}. If the change is intended, run TRANSECT_GOLDEN=rewrite cargo test golden. The diff is:\n{}",
        path.display(),
        diff(&then, &now)
    );
}

#[rstest]
#[case::the_same("a\nb\n", "a\nb\n", "")]
#[case::a_line_changed("a\nb\nc\n", "a\nB\nc\n", "+ B\n- b\n")]
#[case::a_line_added("a\nc\n", "a\nb\nc\n", "+ b\n")]
#[case::a_line_gone("a\nb\nc\n", "a\nc\n", "- b\n")]
fn diffs_by_line(#[case] then: &str, #[case] now: &str, #[case] expected: &str) {
    assert_eq!(diff(then, now), expected);
}

/// A user's name comes from a binding's subject and can hold anything,
/// so a page must escape it as text and never write it as markup.
#[rstest]
#[case::the_accounts("/access/accounts")]
#[case::its_own_page(&transect_graph::links::user(r#"<b>"odd" & co"#))]
fn escapes_a_name_a_binding_gives(#[case] url: &str) {
    let html = render(url);
    assert_eq!(
        (
            html.contains("<b>"),
            html.contains("&#60;b&#62;&#34;odd&#34; &#38; co"),
        ),
        (false, true)
    );
}
