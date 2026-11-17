//! Adding a view's edge kinds to every link it renders, so a click, a
//! link opened in a new tab, and the page's own event stream all keep
//! showing the edge kinds that the reader turned on, and pointing each
//! name that a page of the access lens draws at its access page.
//!
//! The links come from a dozen templates and view models, each of which
//! would otherwise need the view's kinds passed down to it.  Rewriting
//! the rendered page in one place keeps them all plain paths, and a
//! link added later gets the kinds with no change to its template.

use std::collections::HashSet;

/// The attributes that hold a link to another view.
const ATTRIBUTES: [&str; 3] = [" href=\"", " hx-get=\"", " sse-connect=\""];

/// `html` with `edges=<edges>` added to each in-page link: every
/// `href`, `hx-get`, and `sse-connect` whose value starts with `/`, but
/// not static files and not a link that names its edge kinds already.
/// The kinds go before a fragment, so a link to a band keeps its place.
pub fn carry(html: &str, edges: &str) -> String {
    let mut out = String::with_capacity(html.len() + html.len() / 16);
    // Where each attribute next starts at or after `done`.  An attribute
    // is searched for again only once the copy passes it, so each search
    // resumes where its last one stopped and the page is read once per
    // attribute, however many links it holds.
    let mut next = ATTRIBUTES.map(|attribute| html.find(attribute));
    let mut done = 0;
    while let Some((which, at)) = first(&next) {
        let start = at + ATTRIBUTES[which].len();
        out.push_str(&html[done..start]);
        let end = html[start..]
            .find('"')
            .map_or(html.len(), |end| start + end);
        out.push_str(&carried(&html[start..end], edges));
        done = end;
        for (attribute, found) in ATTRIBUTES.iter().zip(&mut next) {
            if found.is_some_and(|found| found < done) {
                *found = html[done..].find(attribute).map(|found| done + found);
            }
        }
    }
    out.push_str(&html[done..]);
    out
}

/// Which attribute starts first, and where.
fn first(next: &[Option<usize>; 3]) -> Option<(usize, usize)> {
    let found = next.iter().enumerate();
    let found = found.filter_map(|(which, at)| Some((which, (*at)?)));
    found.min_by_key(|(_, at)| *at)
}

fn carried(link: &str, edges: &str) -> String {
    let in_page = link.starts_with('/') && !link.starts_with("/static/");
    if !in_page || link.contains("edges=") {
        return link.into();
    }
    let (path, fragment) = match link.find('#') {
        Some(at) => link.split_at(at),
        None => (link, ""),
    };
    let joiner = if path.contains('?') { '&' } else { '?' };
    format!("{path}{joiner}edges={edges}{fragment}")
}

/// `html` with each link that selects a namespace on the cluster map
/// keeping the loss of `lose`, so a click on a parcel in a loss view
/// stays in the loss and opens that namespace's card in it.
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

/// The parts of a page whose links lead out of the access lens: the
/// crumb, to each ancestor's usual page, and the lens tabs, to the
/// object's other lenses.  Each opens with the first string and closes
/// with the second.
const LEAVING: [(&str, &str); 2] = [
    ("<nav class=\"elevation\"", "</nav>"),
    ("<span class=\"lens-tabs\"", "</span>"),
];

/// `html`, a page of the access lens, with each link to an object that
/// has an access page sent to that page, because a reader there is
/// following access.  `paths` holds the usual page of each such object,
/// every namespace and every workload that serves a port.  The crumb and
/// the lens tabs keep their links.  Every card, drawing, list, and
/// switcher row links names by their usual pages, and this function
/// sends them all into the lens in one place.
pub fn carry_access(html: &str, paths: &HashSet<String>) -> String {
    let mut out = String::with_capacity(html.len() + html.len() / 32);
    let mut rest = html;
    while let Some((at, close)) = leaving(rest) {
        out.push_str(&into_access(&rest[..at], paths));
        let end = rest[at..].find(close).map_or(rest.len(), |end| at + end);
        out.push_str(&rest[at..end]);
        rest = &rest[end..];
    }
    out.push_str(&into_access(rest, paths));
    out
}

/// Where the first part of `html` that leads out of the lens starts,
/// and what closes it.
fn leaving(html: &str) -> Option<(usize, &'static str)> {
    let found = LEAVING
        .iter()
        .filter_map(|(open, close)| Some((html.find(open)?, *close)));
    found.min_by_key(|(at, _)| *at)
}

/// `html` with each `href` and `hx-get` in `paths` sent to its access
/// page, keeping its query and fragment.
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
    #[case::a_path(
        r#"<a href="/namespaces/shop">"#,
        r#"<a href="/namespaces/shop?edges=holds">"#
    )]
    #[case::a_query(
        r#"<div hx-get="/?select=shop">"#,
        r#"<div hx-get="/?select=shop&edges=holds">"#
    )]
    #[case::a_fragment(
        r#"<a href="/?select=shop#apps">"#,
        r#"<a href="/?select=shop&edges=holds#apps">"#
    )]
    #[case::the_stream(
        r#"<div sse-connect="/events/">"#,
        r#"<div sse-connect="/events/?edges=holds">"#
    )]
    #[case::a_static_file(
        r#"<link href="/static/web/layout.css">"#,
        r#"<link href="/static/web/layout.css">"#
    )]
    #[case::another_site(
        r#"<a href="https://forge.example.com/x">"#,
        r#"<a href="https://forge.example.com/x">"#
    )]
    #[case::kinds_named_already(r#"<a href="/?edges=runs-on">"#, r#"<a href="/?edges=runs-on">"#)]
    #[case::the_data_prefix(r#"<input data-hx-get="/">"#, r#"<input data-hx-get="/">"#)]
    #[case::two_links(
        r#"<a href="/a" hx-get="/a">x</a>"#,
        r#"<a href="/a?edges=holds" hx-get="/a?edges=holds">x</a>"#
    )]
    #[case::links_of_each_attribute_in_turn(
        r#"<div sse-connect="/events/"><a href="/a">x</a><b hx-get="/b"></b><a href="/c"></a></div>"#,
        r#"<div sse-connect="/events/?edges=holds"><a href="/a?edges=holds">x</a><b hx-get="/b?edges=holds"></b><a href="/c?edges=holds"></a></div>"#
    )]
    #[case::an_unclosed_value(r#"<a href="/a"#, r#"<a href="/a?edges=holds"#)]
    fn adds_the_kinds_to_each_link_to_a_view(#[case] html: &str, #[case] expected: &str) {
        assert_eq!(carry(html, "holds"), expected);
    }

    #[rstest]
    #[case::a_parcel(
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
    #[case::a_place_on_it(
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
    #[case::the_data_prefix(
        r#"<input data-hx-get="/namespaces/shop">"#,
        r#"<input data-hx-get="/namespaces/shop">"#
    )]
    #[case::a_machine_named_in_a_way_in(
        r#"<a href="/machines/node-a">node-a</a><a href="/namespaces/shop/deployments/web">'s address</a>"#,
        r#"<a href="/machines/node-a">node-a</a><a href="/namespaces/shop/deployments/web/access">'s address</a>"#
    )]
    fn carries_the_access_lens_onto_names(#[case] html: &str, #[case] expected: &str) {
        assert_eq!(carry_access(html, &paths()), expected);
    }
}
