//! Rewrites the links of a rendered page so the next page keeps the
//! reader's view. `carry_loss` keeps the loss view, and `carry_access`
//! keeps the access lens.
//!
//! The rewrite runs on the rendered HTML so that the templates write
//! plain paths and need no view state passed down to them, and a link
//! added to a template later is rewritten with no change.

use std::collections::HashSet;

/// `html` with `lose=<lose>` added to each `href` and `hx-get` that
/// starts with `/?select=`, so a namespace clicked on the cluster map
/// opens in the same loss view.
pub fn carry_loss(html: &str, lose: &[String]) -> String {
    let lose = lose.join(",");
    let mut out = html.to_string();
    for attribute in [" href=\"", " hx-get=\""] {
        let from = format!("{attribute}/?select=");
        let to = format!("{attribute}/?lose={lose}&select=");
        out = out.replace(&from, &to);
    }
    out
}

/// The parts of a page whose links `carry_access` leaves alone, as the
/// text that opens each part and the text that closes it. The breadcrumb
/// links to each ancestor's overview page, and the lens tabs link to the
/// object's other lenses.
const UNCHANGED_PARTS: [(&str, &str); 2] = [
    ("<nav class=\"elevation\"", "</nav>"),
    ("<span class=\"lens-tabs\"", "</span>"),
];

/// `html`, a page of the access lens, with each link to an object that
/// has an access page pointed at that access page instead, so the reader
/// stays in the lens. `paths` holds the overview path of each such
/// object: every namespace and every workload that serves a port.
pub fn carry_access(html: &str, paths: &HashSet<String>) -> String {
    let mut out = String::with_capacity(html.len() + html.len() / 32);
    let mut rest = html;
    while let Some((at, close)) = next_unchanged_part(rest) {
        out.push_str(&into_access(&rest[..at], paths));
        let end = rest[at..].find(close).map_or(rest.len(), |end| at + end);
        out.push_str(&rest[at..end]);
        rest = &rest[end..];
    }
    out.push_str(&into_access(rest, paths));
    out
}

/// The position of the first part of `html` listed in
/// `UNCHANGED_PARTS`, and the text that closes it.
fn next_unchanged_part(html: &str) -> Option<(usize, &'static str)> {
    let found = UNCHANGED_PARTS
        .iter()
        .filter_map(|(open, close)| Some((html.find(open)?, *close)));
    found.min_by_key(|(at, _)| *at)
}

/// `html` with each `href` and `hx-get` whose path is in `paths`
/// changed to that path plus `/access`, keeping its query and fragment.
fn into_access(html: &str, paths: &HashSet<String>) -> String {
    let mut out = String::with_capacity(html.len());
    let mut done = 0;
    for (at, _) in html.match_indices("=\"/") {
        let start = at + "=\"".len();
        if start < done || !(html[..at].ends_with(" href") || html[..at].ends_with(" hx-get")) {
            continue;
        }
        let end = html[start..]
            .find('"')
            .map_or(html.len(), |end| start + end);
        let link = &html[start..end];
        let path_end = link.find(['?', '#']).unwrap_or(link.len());
        let (path, tail) = link.split_at(path_end);
        if paths.contains(path) {
            out.push_str(&html[done..start]);
            out.push_str(path);
            out.push_str("/access");
            out.push_str(tail);
            done = end;
        }
    }
    out.push_str(&html[done..]);
    out
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::a_namespace_on_the_map(
        r#"<a href="/?select=shop" hx-get="/?select=shop">"#,
        r#"<a href="/?lose=alpha,beta&select=shop" hx-get="/?lose=alpha,beta&select=shop">"#
    )]
    #[case::another_page(r#"<a href="/namespaces/shop">"#, r#"<a href="/namespaces/shop">"#)]
    #[case::the_plain_cluster(r#"<a href="/">"#, r#"<a href="/">"#)]
    fn keeps_the_loss_on_links_that_select_on_the_map(#[case] html: &str, #[case] expected: &str) {
        let lose = ["alpha".to_string(), "beta".to_string()];
        assert_eq!(carry_loss(html, &lose), expected);
    }

    fn paths() -> HashSet<String> {
        HashSet::from([
            "/namespaces/shop".to_string(),
            "/namespaces/shop/deployments/web".to_string(),
        ])
    }

    #[rstest]
    #[case::a_namespace(
        r#"<a href="/namespaces/shop" hx-get="/namespaces/shop">"#,
        r#"<a href="/namespaces/shop/access" hx-get="/namespaces/shop/access">"#
    )]
    #[case::a_workload_that_serves_a_port(
        r#"<a class="object-link" href="/namespaces/shop/deployments/web">"#,
        r#"<a class="object-link" href="/namespaces/shop/deployments/web/access">"#
    )]
    #[case::a_fragment(
        r#"<a href="/namespaces/shop/deployments/web#changes">"#,
        r#"<a href="/namespaces/shop/deployments/web/access#changes">"#
    )]
    #[case::a_workload_that_serves_none(
        r#"<a href="/namespaces/shop/deployments/batch">"#,
        r#"<a href="/namespaces/shop/deployments/batch">"#
    )]
    #[case::another_lens(
        r#"<a href="/namespaces/shop/deployments/web/section">"#,
        r#"<a href="/namespaces/shop/deployments/web/section">"#
    )]
    #[case::the_crumb(
        r#"<nav class="elevation"><a href="/namespaces/shop">shop</a></nav><a href="/namespaces/shop">"#,
        r#"<nav class="elevation"><a href="/namespaces/shop">shop</a></nav><a href="/namespaces/shop/access">"#
    )]
    #[case::the_lens_tabs(
        r#"<span class="lens-tabs"><a href="/namespaces/shop/deployments/web">overview</a></span>"#,
        r#"<span class="lens-tabs"><a href="/namespaces/shop/deployments/web">overview</a></span>"#
    )]
    #[case::a_machine_link_beside_a_workload_link(
        r#"<a href="/machines/node-a">node-a</a><a href="/namespaces/shop/deployments/web">'s address</a>"#,
        r#"<a href="/machines/node-a">node-a</a><a href="/namespaces/shop/deployments/web/access">'s address</a>"#
    )]
    fn points_links_at_access_pages(#[case] html: &str, #[case] expected: &str) {
        assert_eq!(carry_access(html, &paths()), expected);
    }
}
