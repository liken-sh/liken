//! Tests that the router of `--fixture` serves each type of page that
//! the fixture cluster has an example of.

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use axum::Router;
use axum::body::Body;
use axum::http::{Request, StatusCode};
use http_body_util::BodyExt;
use rstest::rstest;
use tokio::sync::{Mutex, watch};
use tower::ServiceExt;

use super::fixture;
use super::latest::Latest;

/// Requests `url` from a new router on the fixture cluster.
async fn get(url: &str) -> (StatusCode, String) {
    let (changes, _) = watch::channel(());
    let router = super::router(&changes, super::fixture_sources(&changes).unwrap());
    get_from(router, url).await
}

async fn get_from(router: Router, url: &str) -> (StatusCode, String) {
    send(router, Request::get(url).body(Body::empty()).unwrap()).await
}

async fn send(router: Router, request: Request<Body>) -> (StatusCode, String) {
    let response = router.oneshot(request).await.unwrap();
    let status = response.status();
    let body = response.into_body().collect().await.unwrap().to_bytes();
    (status, String::from_utf8_lossy(&body).into_owned())
}

#[rstest]
#[case::the_map("/", "ground-box")]
#[case::a_namespace_selected("/?select=acme-shop", "namespace-card")]
#[case::a_loss("/?lose=node-alpha", "node-alpha")]
#[case::the_resources("/resources", "resources.yaml")]
#[case::the_icon_link(
    "/",
    "<link rel=\"icon\" type=\"image/svg+xml\" href=\"/static/brand/transect.svg\">"
)]
#[case::an_alerts_markdown_link(
    "/",
    "<a href=\"https://runbooks.example.test/ledger/exporter-down?window=10m&amp;source="
)]
#[case::an_alerts_bare_url("/", "<a href=\"https://dashboards.example.test/d/ledger-exporter/")]
#[case::the_icon("/static/brand/transect.svg", "<svg")]
#[case::alive("/livez", "ok")]
#[case::ready("/readyz", "ok")]
#[case::what_changed("/changed", "<main")]
#[case::search("/search?q=store", "storefront")]
#[case::search_offers_an_access_tab(
    "/search?q=storefront",
    "/namespaces/acme-shop/deployments/storefront/access"
)]
#[case::search_finds_a_group(
    "/search?q=serviceaccounts:acme-ledger",
    "/access/accounts/groups/system:serviceaccounts:acme-ledger"
)]
#[case::search_offers_a_namespaces_access("/search?q=acme-shop", "/namespaces/acme-shop/access")]
#[case::search_finds_an_account(
    "/search?q=ops-console",
    "/namespaces/acme-ops/serviceaccounts/ops-console"
)]
#[case::the_kinds("/kinds", "Promotions")]
#[case::the_ways_in("/access", "shop.example.test")]
#[case::the_vulnerabilities("/vulnerabilities", "CVE-2026-0001")]
#[case::the_vulnerabilities_of_one_severity("/vulnerabilities?severity=HIGH", "CVE-2026-0002")]
#[case::one_finding(
    "/vulnerabilities/CVE-2026-0001",
    "https://advisories.example.test/CVE-2026-0001"
)]
#[case::a_workloads_vulnerabilities(
    "/namespaces/acme-shop/deployments/storefront/vulnerabilities",
    "libexample"
)]
#[case::the_masthead_links_the_vulnerabilities("/", "href=\"/vulnerabilities\"")]
#[case::the_search_page_links_the_vulnerabilities("/search?q=CVE", "href=\"/vulnerabilities\"")]
#[case::search_finds_a_finding("/search?q=CVE-2026-0001", "/vulnerabilities/CVE-2026-0001")]
#[case::the_accounts("/access/accounts", "ops-console")]
#[case::an_account(
    "/namespaces/acme-ops/serviceaccounts/ops-console",
    "Who may run as it"
)]
#[case::a_group(
    "/access/accounts/groups/system:serviceaccounts:acme-ledger",
    "every ServiceAccount in acme-ledger"
)]
#[case::a_user("/access/accounts/users/shop-owner", "signs in as it")]
#[case::a_namespaces_access("/namespaces/acme-shop/access", "What each account may do here")]
#[case::a_workloads_access(
    "/namespaces/acme-shop/deployments/storefront/access",
    "What may reach it"
)]
#[case::no_way_in(
    "/namespaces/acme-ledger/statefulsets/ledger-store/access",
    "no way in"
)]
#[case::a_kind_with_knowledge("/kinds/promotions.promotions.example.test", "spring-sale")]
#[case::a_kind_without_knowledge("/kinds/caches.caches.example.test", "session-cache")]
#[case::a_trimmed_kind("/kinds/gizmos.gizmos.example.test", "gizmo-red")]
#[case::a_machine("/machines/node-alpha", "ledger-db")]
#[case::a_ground_box("/ground/archive-nas", "data-ledger-db-0")]
#[case::a_ground_box_by_an_address_it_lists("/ground/nas.example.test", "data-ledger-db-0")]
#[case::a_namespace("/namespaces/acme-shop", "storefront")]
#[case::a_namespaces_kind("/namespaces/acme-shop/gizmos.gizmos.example.test", "gizmo-blue")]
#[case::a_workload_with_an_entry(
    "/namespaces/acme-shop/deployments/storefront",
    "storefront-route"
)]
#[case::its_section(
    "/namespaces/acme-shop/deployments/storefront/section",
    "shop.example.test"
)]
#[case::its_logs(
    "/namespaces/acme-shop/deployments/storefront/logs",
    "storefront-rs-one"
)]
#[case::a_failing_workload("/namespaces/acme-ledger/deployments/ledger-exporter", "crash looping")]
#[case::an_operator("/namespaces/acme-promoter/deployments/promoter", "clearance")]
#[case::a_custom_workload(
    "/namespaces/acme-shop/caches.caches.example.test/session-cache",
    "session-cache-set"
)]
#[case::a_known_custom_resource(
    "/namespaces/acme-shop/promotions.promotions.example.test/spring-sale",
    "storefront-svc"
)]
#[case::a_machines_named_hardware("/machines/node-beta", "The counter handset")]
#[case::a_machines_hardware_by_pci_bus_id("/machines/node-alpha", "with card-render")]
#[case::a_device_tied_to_what_it_names(
    "/namespaces/acme-shop/tills.tills.example.test/counter-till/section",
    "pairs through"
)]
#[case::a_board_nested_in_what_it_is_part_of(
    "/namespaces/acme-ops/shelves.stock.example.test/top-shelf",
    "Part of</span> <a class=\"object-link board-name\" href=\"/namespaces/acme-ops/rooms.stock.example.test/back-room\""
)]
#[case::its_parts_inside(
    "/namespaces/acme-ops/rooms.stock.example.test/back-room",
    "board-part"
)]
#[case::a_second_whole_as_a_line(
    "/namespaces/acme-ops/bins.stock.example.test/screw-bin",
    "also part of"
)]
#[case::a_reference_by_value(
    "/cluster/handsets.radios.example.test/handset-till",
    "/cluster/radios.radios.example.test/radio-desk"
)]
#[case::a_trimmed_custom_resource(
    "/namespaces/acme-shop/gizmos.gizmos.example.test/gizmo-red",
    "large"
)]
#[case::a_pod("/namespaces/acme-ledger/pods/ledger-db-0", "/var/lib/ledger")]
#[case::a_claim(
    "/namespaces/acme-ledger/persistentvolumeclaims/data-ledger-db-0",
    "ledger-archive"
)]
#[case::a_volume("/volumes/ledger-archive", "3.0Gi · 38%")]
#[case::a_volume_titled_by_its_claim(
    "/volumes/ledger-archive",
    "<title>PersistentVolumeClaim data-ledger-db-0"
)]
#[case::a_storage_class(
    "/cluster/storageclasses.storage.k8s.io/archive-nfs",
    "nfs.csi.example.test"
)]
#[case::a_service("/namespaces/acme-shop/services/storefront-svc", "storefront-route")]
#[case::an_ingress(
    "/namespaces/acme-shop/ingresses.networking.k8s.io/storefront-route",
    "shop.example.test"
)]
#[case::a_service_account("/namespaces/acme-promoter/serviceaccounts/promoter", "promoter")]
#[case::a_cluster_role(
    "/cluster/clusterroles.rbac.authorization.k8s.io/promotion-editor",
    "promotions.example.test"
)]
#[case::a_lease_read_once(
    "/namespaces/acme-shop/leases.coordination.k8s.io/storefront-leader",
    "/namespaces/acme-shop/pods/storefront-rs-one"
)]
#[tokio::test]
async fn serves_every_page_type(#[case] url: &str, #[case] expected: &str) {
    let (status, html) = get(url).await;
    assert_eq!(
        (status, html.contains(expected)),
        (StatusCode::OK, true),
        "GET {url} returned status {status} and a page that {} {expected:?}, want 200 and a page that contains it",
        if html.contains(expected) {
            "contains"
        } else {
            "lacks"
        }
    );
}

/// A `PersistentVolume` requested under `/cluster` redirects to its page
/// under `/volumes`.
#[tokio::test]
async fn sends_a_volume_to_its_own_page() {
    let (status, _) = get("/cluster/persistentvolumes/ledger-archive").await;
    assert_eq!(status, StatusCode::PERMANENT_REDIRECT);
}

/// A page request renders from the last model built and does not wait
/// for a build that a cluster change started, because the cluster can
/// change every few seconds and a build is slow. The page's event stream
/// sends the newer rendering when the build finishes.
#[rstest]
#[tokio::test]
async fn a_page_never_waits_for_a_model_build() {
    let (changes, _) = watch::channel(());
    // Every build after the first waits for `gate`, which the test
    // holds until the page response arrives.
    let gate = Arc::new(Mutex::new(()));
    let held = gate.lock().await;
    let builds = AtomicUsize::new(0);
    let mut sources = super::fixture_sources(&changes).unwrap();
    sources.models = Latest::new(&changes, {
        let gate = gate.clone();
        move |_| {
            if builds.fetch_add(1, Ordering::SeqCst) > 0 {
                drop(gate.blocking_lock());
            }
            fixture::model()
        }
    });
    let router = super::router(&changes, sources);
    get_from(router.clone(), "/").await;
    changes.send_replace(());
    let status = tokio::time::timeout(
        Duration::from_secs(5),
        get_from(router, "/?select=acme-shop"),
    )
    .await
    .map(|(status, _)| status);
    drop(held);
    assert_eq!(status, Ok(StatusCode::OK));
}

/// Quick search asks for its rows with the `HX-Target` header, which
/// htmx writes as the list's tag and id, and receives the rows alone.
#[rstest]
#[case::the_dialogs_list("ol#search-results")]
#[case::the_pages_list("ol#search-page-results")]
#[tokio::test]
async fn search_sends_only_the_rows_to_a_list(#[case] target: &str) {
    let (changes, _) = watch::channel(());
    let router = super::router(&changes, super::fixture_sources(&changes).unwrap());
    let request = Request::get("/search?q=store")
        .header("HX-Target", target)
        .body(Body::empty())
        .unwrap();
    let (status, html) = send(router, request).await;
    assert_eq!(
        (status, html.contains("storefront"), html.contains("<html")),
        (StatusCode::OK, true, false),
        "GET /search?q=store with HX-Target {target} returned {html:?}, want only the rows"
    );
}

/// A page's event stream sends the view as an event with no name,
/// because htmx's `hx-sse` swaps the data of an unnamed event into the
/// page and dispatches a named event as a DOM event.
#[tokio::test]
async fn the_event_stream_sends_the_view_in_an_unnamed_event() {
    let (changes, _) = watch::channel(());
    let router = super::router(&changes, super::fixture_sources(&changes).unwrap());
    let request = Request::get("/events/").body(Body::empty()).unwrap();
    let response = router.oneshot(request).await.unwrap();
    let frame = response.into_body().frame().await.unwrap().unwrap();
    let event = String::from_utf8_lossy(frame.data_ref().unwrap()).into_owned();
    assert!(
        event.starts_with("data: <div class=\"view\" id=\"view\">"),
        "the first event of /events/ is {event:?}, want the view in an unnamed event"
    );
}

/// Each page sends the version of its scripts with every htmx request,
/// so the server can tell a page that loaded older scripts to load
/// again.
#[rstest]
#[case::a_page("/")]
#[case::the_search_page("/search?q=store")]
#[tokio::test]
async fn a_page_names_the_version_of_its_scripts(#[case] url: &str) {
    let (_, html) = get(url).await;
    let body = format!(
        r#"<body hx-headers:inherited='{{"Transect-Scripts": "{}"}}'>"#,
        super::scripts::version()
    );
    assert!(
        html.contains(&body),
        "GET {url} has no {body:?}, so its htmx requests send no version"
    );
}
