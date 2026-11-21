//! Tests that crawl the pages of the access lens: a name on an access
//! page opens its access, a way in links its machine, and no access
//! drawing crosses its own lines.

use std::collections::BTreeSet;

use super::super::fixture;
use super::{crawl, links, render, without};

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
