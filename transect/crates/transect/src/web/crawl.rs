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
use super::view::{self, View};
use transect_graph::edges::Object;
use transect_observe::usage;

/// The crawl fails at this many pages, so links that keep generating
/// new URLs fail the test instead of running forever.
const MOST: usize = 400;

/// Renders `url` as a whole page from `model`, or `None` for a URL that
/// no view serves.
fn render(model: &Model, url: &str) -> Option<Rendered> {
    let uri: Uri = url.parse().ok()?;
    let Query(query) = Query::<view::Query>::try_from_uri(&uri).ok()?;
    // The router percent-decodes the path before it matches a view.
    let path = transect_graph::links::percent_decoded(uri.path().trim_start_matches('/'));
    let view = View::at(&path, query)?;
    let reads = transect_map::objects::reads::Reads::default();
    draw(
        model,
        &view,
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
    while let Some(at) = rest
        .find(&format!("{open} "))
        .or_else(|| rest.find(&format!("{open}>")))
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
#[case::a_loss("/?lose=node-alpha", "node-alpha")]
fn a_page_is_about_its_subject(#[case] url: &str, #[case] expected: &str) {
    assert_eq!(subject(url), expected);
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

/// The edge-kind menu appears only on pages where the edge kinds change
/// the drawing, and never in the masthead.
#[rstest::rstest]
#[case::the_map("/", true)]
#[case::a_workload("/namespaces/acme-shop/deployments/storefront", true)]
#[case::its_section("/namespaces/acme-shop/deployments/storefront/section", true)]
#[case::its_access("/namespaces/acme-shop/deployments/storefront/access", false)]
#[case::the_ways_in("/access", false)]
#[case::the_kinds("/kinds", false)]
#[case::one_kind("/kinds/gizmos.gizmos.example.test", false)]
#[case::a_namespaces_list_of_a_kind("/namespaces/acme-shop/gizmos.gizmos.example.test", false)]
#[case::a_generic_page("/namespaces/acme-shop/services/storefront-svc", false)]
#[case::the_logs_lens("/namespaces/acme-shop/deployments/storefront/logs", false)]
#[case::an_outside_system("/ground/archive-nas", false)]
#[case::a_machine("/machines/node-alpha", false)]
#[case::a_volume(
    "/namespaces/acme-ledger/persistentvolumeclaims/data-ledger-db-0",
    false
)]
fn shows_the_edge_menu_only_where_edge_kinds_change_the_drawing(
    #[case] url: &str,
    #[case] shown: bool,
) {
    let (pages, _) = crawl(&fixture::model());
    let html = pages
        .iter()
        .find(|(found, _)| found == url)
        .map(|(_, html)| html);
    let html = html.unwrap_or_else(|| panic!("the crawl did not reach {url}"));
    assert_eq!(html.contains("edge-menu"), shown, "edge menu on {url}");
    let masthead = html.split("</header>").next().unwrap_or_default();
    assert!(
        !masthead.contains("edge-menu"),
        "the masthead of {url} has the edge menu"
    );
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

/// Whether `url` is a page of the access lens: an access tab, a page
/// under `/access`, or a `ServiceAccount` page, which draws the
/// account's access.
fn is_access(url: &str) -> bool {
    let path = url.split('?').next().unwrap_or_default();
    path.ends_with("/access") || path.starts_with("/access") || path.contains("/serviceaccounts/")
}

/// The links in the body of `html`, outside the masthead and the lens
/// tabs. The breadcrumb links to each ancestor's page and the tabs to
/// the object's other lenses, so both leave the access lens.
fn lens_links(html: &str) -> Vec<String> {
    let body = html.split_once("</header>").map_or("", |(_, body)| body);
    links(&without(body, "<span class=\"lens-tabs\"", "</span>"))
}

/// On an access page, a link to a namespace or to a workload that
/// serves a port opens its access tab and not its overview.
#[test]
fn on_an_access_page_a_name_opens_its_access() {
    let model = fixture::model();
    let (pages, _) = crawl(&model);
    let reached: BTreeSet<&str> = pages.iter().map(|(url, _)| url.as_str()).collect();
    let served: BTreeSet<String> = reached
        .iter()
        .filter_map(|url| url.strip_suffix("/access"))
        .map(String::from)
        .collect();
    let wrong: BTreeSet<(String, String)> = pages
        .iter()
        .filter(|(url, _)| is_access(url))
        .flat_map(|(url, html)| {
            let served = &served;
            lens_links(html).into_iter().filter_map(move |link| {
                let page = link.split(['?', '#']).next().unwrap_or_default();
                served.contains(page).then(|| (url.clone(), link))
            })
        })
        .collect();
    assert!(
        reached
            .iter()
            .any(|url| url.ends_with("/access") && url.starts_with("/namespaces/acme-shop"))
    );
    assert_eq!(wrong, BTreeSet::new());
}

/// A way in that reaches one machine links the machine's name to its
/// machine page, because the access lens has no page for a machine.
/// After the machine's link comes a link to the workload that the way
/// in reaches, or the text `'s address` when `way` is empty.
#[rstest::rstest]
#[case::the_ways_in("/access", "/namespaces/acme-promoter/deployments/promoter/access")]
#[case::a_namespaces_ports("/namespaces/acme-ops/access", "")]
fn a_way_in_on_one_machine_links_the_machine(#[case] url: &str, #[case] way: &str) {
    let page = render(&fixture::model(), url).unwrap();
    let html = page.html;
    let machine = r#"href="/machines/node-alpha" hx-get="/machines/node-alpha""#;
    let named = html.find(&format!(
        "{machine} hx-target=\"body\" hx-swap=\"innerHTML\" hx-push-url=\"true\">node-alpha</a>"
    ));
    let after = named.map(|at| &html[at..]).unwrap_or_default();
    let rest = after.split_once("</a>").map_or("", |(_, rest)| rest);
    let expected = match way {
        "" => r#"<span class="ax-way-name">&#39;s address"#.to_string(),
        way => format!(r#"<a class="object-link ax-name" href="{way}""#),
    };
    assert!(
        rest.starts_with(&expected),
        "after the machine's link on {url} the page has {}, want it to start with {expected}",
        &rest[..rest.len().min(200)]
    );
}

/// The lines of each access drawing in `html`, each as the points of
/// its SVG path. A `Q` curve adds only its end point, so a rounded
/// corner counts as a straight line from where the turn starts to
/// where it ends.
fn drawn_lines(html: &str) -> Vec<Vec<Vec<(f64, f64)>>> {
    let drawings = html
        .split("<section class=\"section-drawing ax-drawing\"")
        .skip(1);
    let svg = |drawing: &str| {
        let svg = drawing.split("</svg>").next().unwrap_or_default();
        svg.split_once("</defs>")
            .map_or("", |(_, lines)| lines)
            .to_string()
    };
    let points = |d: &str| {
        let words: Vec<&str> = d.split_whitespace().collect();
        let mut points = Vec::new();
        let mut at = 0;
        while at < words.len() {
            let skip = match words[at] {
                "M" | "L" => 1,
                "Q" => 3,
                _ => {
                    at += 1;
                    continue;
                }
            };
            let number = |offset: usize| words.get(at + offset).and_then(|w| w.parse::<f64>().ok());
            if let (Some(x), Some(y)) = (number(skip), number(skip + 1)) {
                points.push((x, y));
            }
            at += skip + 2;
        }
        points
    };
    drawings
        .map(|drawing| {
            let lines = svg(drawing);
            let ds = lines.split(" d=\"").skip(1);
            ds.map(|rest| points(rest.split('"').next().unwrap_or_default()))
                .collect()
        })
        .collect()
}

/// How many times a segment of one line passes through a segment of
/// another. Lines that only touch where one of them ends do not cross.
fn crossings(lines: &[Vec<(f64, f64)>]) -> usize {
    type P = (f64, f64);
    // The side of the line through `p` and `q` that `r` is on: 1, -1,
    // or 0 on the line. `f64::signum` would return 1 for 0.0.
    let side = |p: P, q: P, r: P| {
        let turn = (q.0 - p.0) * (r.1 - p.1) - (q.1 - p.1) * (r.0 - p.0);
        (turn > 0.0) as i8 - (turn < 0.0) as i8
    };
    let cross = |a: P, b: P, c: P, d: P| {
        let apart = |x: i8, y: i8| x * y < 0;
        apart(side(a, b, c), side(a, b, d)) && apart(side(c, d, a), side(c, d, b))
    };
    let mut count = 0;
    for (index, one) in lines.iter().enumerate() {
        for other in &lines[index + 1..] {
            for ab in one.windows(2) {
                for cd in other.windows(2) {
                    count += usize::from(cross(ab[0], ab[1], cd[0], cd[1]));
                }
            }
        }
    }
    count
}

/// No two lines of one access drawing cross. A workload's access page
/// has two drawings, of who reaches the workload and of who may change
/// it, and a `ServiceAccount`'s page has one.
#[test]
fn no_access_drawing_crosses_its_own_lines() {
    let (pages, _) = crawl(&fixture::model());
    let mut drawn = BTreeSet::new();
    let crossed: BTreeSet<(String, usize, usize)> = pages
        .iter()
        .filter(|(url, _)| is_access(url))
        .flat_map(|(url, html)| {
            let drawings = drawn_lines(html);
            if !drawings.is_empty() {
                drawn.insert((url.clone(), drawings.len()));
            }
            let counted = drawings.into_iter().enumerate();
            counted
                .map(|(at, lines)| (url.clone(), at, crossings(&lines)))
                .filter(|(_, _, count)| *count > 0)
                .collect::<Vec<_>>()
        })
        .collect();
    let workloads = drawn.iter().filter(|(_, count)| *count == 2).count();
    let accounts = drawn
        .iter()
        .filter(|(url, _)| url.contains("/serviceaccounts/"))
        .count();
    // Without this, a fixture that draws nothing would pass.
    assert!(
        workloads >= 3 && accounts >= 2,
        "the fixture draws {drawn:#?}, want at least 3 workloads and 2 accounts with two lines"
    );
    assert_eq!(crossed, BTreeSet::new());
}

#[rstest::rstest]
#[case::a_cross(vec![vec![(0.0, 5.0), (10.0, 5.0)], vec![(5.0, 0.0), (5.0, 10.0)]], 1)]
#[case::a_join_at_an_end(vec![vec![(0.0, 5.0), (5.0, 5.0)], vec![(5.0, 0.0), (5.0, 10.0)]], 0)]
#[case::a_shared_lane(vec![vec![(0.0, 0.0), (5.0, 0.0), (5.0, 10.0)], vec![(0.0, 4.0), (5.0, 4.0), (5.0, 10.0)]], 0)]
fn counts_a_crossing_only_where_lines_pass_through(
    #[case] lines: Vec<Vec<(f64, f64)>>,
    #[case] expected: usize,
) {
    assert_eq!(crossings(&lines), expected);
}
