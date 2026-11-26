//! Tests that crawl every page of the fixture cluster, from the map
//! along each link. Every link must open a page, and the name of every
//! object that has a page must be a link wherever it appears, except on
//! that object's own page.

use std::collections::{BTreeSet, VecDeque};

use axum::extract::Query;
use axum::http::Uri;

use super::fixture;
use super::model::Model;
use super::render::{Part, Rendered, draw};
use super::view::{self, Page};
use transect_graph::edges::Object;
use transect_observe::usage;

/// The crawl fails at this many pages, so links that keep generating
/// new URLs fail the test instead of running forever.
const MOST: usize = 600;

/// Renders `url` as a whole page from `model`, or `None` for a URL that
/// no view serves.
fn render(model: &Model, url: &str) -> Option<Rendered> {
    let uri: Uri = url.parse().ok()?;
    let Query(query) = Query::<view::Query>::try_from_uri(&uri).ok()?;
    // The router percent-decodes the path before it matches a view.
    let path = transect_graph::links::percent_decoded(uri.path().trim_start_matches('/'));
    let page = Page::at(&path, &query)?;
    let reads = transect_map::objects::reads::Reads::default();
    draw(
        model,
        &page,
        Part::Whole,
        &usage::Reading::Off,
        &reads,
        |_, _| None,
        k8s_openapi::jiff::Timestamp::now(),
    )
    .ok()
}

/// The links in `html` that start with `/`, except links to static
/// files.
fn links(html: &str) -> Vec<String> {
    let mut found = Vec::new();
    let mut rest = html;
    while let Some(at) = rest.find("href=\"/") {
        rest = &rest[at + "href=\"".len()..];
        let end = rest.find('"').unwrap_or(rest.len());
        let link = rest[..end].replace("&amp;", "&");
        if !link.starts_with("/static/") {
            found.push(link);
        }
    }
    found
}

/// Renders every page reachable from `/`. Returns each page's URL with
/// its HTML, and the URLs that did not render a page. A link to `#id`
/// on a page counts as a link to the page.
fn crawl(model: &Model) -> (Vec<(String, String)>, BTreeSet<String>) {
    let mut seen = BTreeSet::from(["/".to_string()]);
    let mut queue = VecDeque::from(["/".to_string()]);
    let (mut pages, mut broken) = (Vec::new(), BTreeSet::new());
    while let Some(url) = queue.pop_front() {
        assert!(
            pages.len() < MOST,
            "the crawl rendered {MOST} pages and has more to visit"
        );
        match render(model, &url) {
            Some(Rendered { html, found: true }) => {
                for link in links(&html) {
                    let page = link.split('#').next().unwrap_or_default().to_string();
                    if seen.insert(page.clone()) {
                        queue.push_back(page);
                    }
                }
                pages.push((url, html));
            }
            _ => {
                broken.insert(url);
            }
        }
    }
    (pages, broken)
}

/// The visible text of `html`'s body outside links and literal text
/// such as a label selector. It removes every `<a>`, `<code>`, and
/// `<title>` element, because a `<title>` is an SVG drawing's tooltip,
/// and then every tag.
fn outside_links(html: &str) -> String {
    let body = html.split_once("<body>").map_or(html, |(_, body)| body);
    let text = without(&without(body, "<a", "</a>"), "<code", "</code>");
    let text = without(&text, "<title", "</title>");
    let mut plain = String::new();
    let mut in_tag = false;
    let text = text.as_str();
    for c in text.chars() {
        match c {
            '<' => in_tag = true,
            '>' if in_tag => {
                in_tag = false;
                plain.push(' ');
            }
            c if !in_tag => plain.push(c),
            _ => {}
        }
    }
    plain
}

/// `html` with each element removed that starts with `open` and ends at
/// the next `close`. The `open` tag must be followed by a space or `>`,
/// so `<a` does not match `<abbr`.
fn without(html: &str, open: &str, close: &str) -> String {
    let mut text = String::new();
    let mut rest = html;
    let (spaced, closed) = (format!("{open} "), format!("{open}>"));
    while let Some(at) = [rest.find(&spaced), rest.find(&closed)]
        .into_iter()
        .flatten()
        .min()
    {
        text.push_str(&rest[..at]);
        rest = rest[at..].split_once(close).map_or("", |(_, after)| after);
    }
    text.push_str(rest);
    text
}

/// Whether `name` appears in `text` and not as part of a longer name
/// made of letters, digits, `-`, `.`, and `_`.
fn mentions(text: &str, name: &str) -> bool {
    let part = |c: char| c.is_ascii_alphanumeric() || matches!(c, '-' | '.' | '_');
    text.match_indices(name).any(|(at, _)| {
        let before = text[..at].chars().next_back();
        let after = text[at + name.len()..].chars().next();
        !before.is_some_and(part) && !after.is_some_and(part)
    })
}

/// The name of the object a page is about, which the page shows
/// without a link. For a loss view it is the machine in `lose=`, and
/// for any other page the last segment of the path before a lens
/// suffix.
fn subject(url: &str) -> &str {
    if let Some((_, lose)) = url.split_once("lose=") {
        return lose.split('&').next().unwrap_or_default();
    }
    let path = url.split('?').next().unwrap_or_default();
    // An elevation's URL is the object's followed by
    // `/elevations/<slug>`, and its subject is the object.
    if let ["", "namespaces", _, _, name, "elevations", _] = path.split('/').collect::<Vec<_>>()[..]
    {
        return name;
    }
    let path = path
        .trim_end_matches("/section")
        .trim_end_matches("/logs")
        .trim_end_matches("/access")
        .trim_end_matches("/vulnerabilities");
    path.rsplit('/').next().unwrap_or_default()
}

#[rstest::rstest]
#[case::an_overview("/namespaces/acme-shop/deployments/storefront", "storefront")]
#[case::a_section("/namespaces/acme-shop/deployments/storefront/section", "storefront")]
#[case::a_logs_lens(
    "/namespaces/acme-shop/deployments/storefront/logs?pod=storefront-rs-one",
    "storefront"
)]
#[case::its_vulnerabilities(
    "/namespaces/acme-shop/deployments/storefront/vulnerabilities?severity=HIGH",
    "storefront"
)]
#[case::an_elevation(
    "/namespaces/acme-ops/shelves.stock.example.test/top-shelf/elevations/the-restock",
    "top-shelf"
)]
#[case::an_object_named_elevations(
    "/namespaces/acme-ops/shelves.stock.example.test/elevations",
    "elevations"
)]
#[case::a_loss("/?lose=node-alpha", "node-alpha")]
fn a_page_is_about_its_subject(#[case] url: &str, #[case] expected: &str) {
    assert_eq!(subject(url), expected);
}

/// An elevation's URL draws the page of a kind that states elevations,
/// the first elevation for a slug the kind does not state, and the page
/// that says it was not found for a kind with no elevation.
#[rstest::rstest]
#[case::stated(
    "/namespaces/acme-ops/shelves.stock.example.test/top-shelf/elevations/the-restock",
    true
)]
#[case::not_stated(
    "/namespaces/acme-ops/shelves.stock.example.test/top-shelf/elevations/the-cellar",
    true
)]
#[case::a_kind_with_no_elevation(
    "/namespaces/acme-ops/thermostats.stock.example.test/back-thermostat/elevations/the-shelf",
    false
)]
fn finds_an_elevation_only_of_a_kind_that_states_elevations(
    #[case] url: &str,
    #[case] found: bool,
) {
    assert_eq!(render(&fixture::model(), url).unwrap().found, found);
}

#[test]
fn without_removes_an_element_before_one_with_attributes() {
    let html = "<p><code>x</code> and <code class=\"y\">z</code></p>";
    let text = without(html, "<code", "</code>");
    assert_eq!(
        text, "<p> and </p>",
        "without({html:?}) = {text:?}, want both code elements removed"
    );
}

#[test]
fn every_link_opens_a_page() {
    let (pages, broken) = crawl(&fixture::model());
    assert!(
        pages.len() > 10,
        "the crawl reached {} pages, want more than 10",
        pages.len()
    );
    assert_eq!(broken, BTreeSet::new());
}

#[test]
fn every_link_to_an_element_id_points_at_an_element_with_that_id() {
    let model = fixture::model();
    let (pages, _) = crawl(&model);
    let placed: BTreeSet<String> = pages
        .iter()
        .flat_map(|(_, html)| links(html))
        // A query string can write `&` as `&#38;`, whose `#` is not an
        // element id.
        .map(|link| link.replace("&#38;", "&"))
        .filter(|link| link.contains('#'))
        .collect();
    assert!(
        !placed.is_empty(),
        "no page links to an element id, want at least one"
    );
    let missed: BTreeSet<&String> = placed
        .iter()
        .filter(|link| {
            let (page, id) = link.split_once('#').unwrap_or_default();
            let html = pages
                .iter()
                .find(|(url, _)| url == page)
                .map(|(_, html)| html);
            !html.is_some_and(|html| html.contains(&format!("id=\"{id}\"")))
        })
        .collect();
    assert_eq!(missed, BTreeSet::new());
}

#[test]
fn every_name_with_a_page_is_a_link() {
    let model = fixture::model();
    let objects: Vec<Object> = fixture::objects();
    let (pages, _) = crawl(&model);
    let unlinked: BTreeSet<(String, String)> = pages
        .iter()
        .flat_map(|(url, html)| {
            let text = outside_links(html);
            let named = objects
                .iter()
                .filter(move |object| object.name != subject(url) && mentions(&text, &object.name));
            named.map(|object| (url.clone(), object.name.clone()))
        })
        .collect();
    assert_eq!(unlinked, BTreeSet::new());
}

#[test]
fn a_namespace_lists_its_custom_resources_by_kind() {
    let (pages, _) = crawl(&fixture::model());
    let list = "/namespaces/acme-shop/gizmos.gizmos.example.test";
    let reached: BTreeSet<&str> = pages
        .iter()
        .filter(|(_, html)| html.contains("gizmo-red") && html.contains("gizmo-blue"))
        .map(|(url, _)| url.as_str())
        .collect();
    assert_eq!(
        reached,
        BTreeSet::from([
            "/namespaces/acme-shop",
            list,
            "/kinds/gizmos.gizmos.example.test"
        ])
    );
}

#[rstest::rstest]
#[case::a_service("/namespaces/acme-shop/services/storefront-svc", &["selects pods labeled", "storefront-route"])]
#[case::an_ingress("/namespaces/acme-shop/ingresses.networking.k8s.io/storefront-route", &["shop.example.test", "storefront-svc:80", "3.2 requests/s · p95 120 ms · no 5xx, over 5m"])]
#[case::an_entry_box("/namespaces/acme-shop/deployments/storefront", &["3.2 requests/s · p95 120 ms · no 5xx, over 5m"])]
#[case::a_section_entry_box("/namespaces/acme-shop/deployments/storefront/section", &["3.2 requests/s · p95 120 ms · no 5xx, over 5m"])]
#[case::a_custom_resource("/namespaces/acme-shop/gizmos.gizmos.example.test/gizmo-blue", &["known-chip", "Not ready"])]
#[case::the_kinds("/kinds", &["gizmos.example.test", "Gizmos", "1 not ready</span> of 2"])]
#[case::one_kind("/kinds/gizmos.gizmos.example.test", &["gizmo-red", "gizmo-blue", "acme-shop", "<th>Ready</th>"])]
#[case::an_outside_system("/ground/archive-nas", &["The archive NAS", "https://nas.example.test:5001", "data-ledger-db-0", "nas.example.test"])]
#[case::an_outside_address_only_kind_knowledge_reaches("/ground/sign-in.example.test", &["Configured to reach it", "partner-gate"])]
#[case::an_address_of_a_service_in_the_cluster("/namespaces/acme-ops/gates.proxy.example.test/shop-gate", &["href=\"/namespaces/acme-shop/deployments/storefront\""])]
#[case::an_address_of_a_service_not_there("/namespaces/acme-ops/gates.proxy.example.test/retired-gate", &["reaches <span class=\"object-name\">Service <span class=\"name-part\">acme-shop/</span><wbr><span class=\"name-part\">retired-checker</span></span>"])]
#[case::a_volume("/namespaces/acme-ledger/persistentvolumeclaims/data-ledger-db-0", &["ledger-archive", "8Gi", "3.0Gi · 38%", "writes", "ledger-db-0", "node-alpha", "ledger-backup", "The archive NAS", "nas.example.test:/exports/ledger"])]
#[case::its_printer_columns("/namespaces/acme-shop/gizmos.gizmos.example.test/gizmo-red", &["Size", "<code class=\"board-literal\">True</code>", "First, whose path uses [0:1]"])]
fn every_object_has_a_page(#[case] url: &str, #[case] shows: &[&str]) {
    let (pages, _) = crawl(&fixture::model());
    let html = pages
        .iter()
        .find(|(found, _)| found == url)
        .map(|(_, html)| html);
    let html = html.unwrap_or_else(|| panic!("the crawl did not reach {url}"));
    let missing: Vec<&&str> = shows.iter().filter(|text| !html.contains(**text)).collect();
    assert!(missing.is_empty(), "{url} lacks the text {missing:?}");
}

/// The namespace card beside the map lists the namespace's workloads as
/// chips, because the map's box for the namespace shows only their
/// count. The namespace's own page draws each workload, so its card has
/// no chips.
#[rstest::rstest]
#[case::beside_the_map("/?select=acme-shop", true)]
#[case::on_its_page("/namespaces/acme-shop", false)]
fn the_namespace_card_lists_workloads_only_beside_the_map(#[case] url: &str, #[case] listed: bool) {
    let page = render(&fixture::model(), url).unwrap();
    assert_eq!(
        page.html.contains("<div class=\"chips\">"),
        listed,
        "chips on {url}"
    );
}

mod access;
