//! The results of a search query, rendered as the fragment for the
//! search dialog and as the `/search` page. Each row shows a health
//! dot, the object's name and kind, its location, and its lens chips.

use askama::Template;

use super::Entry;
use transect_frame::frame::crumbs::Crumb;
use transect_observe::health::Level;

/// One row of the results.
#[derive(Debug, PartialEq)]
pub struct Row {
    pub name: String,
    pub kind: String,
    pub context: Option<String>,
    pub href: String,
    /// The health of the object that the row links to. `None` for a
    /// kind that transect does not assess, which has no dot.
    pub level: Option<Level>,
    pub lenses: Vec<(&'static str, String)>,
}

impl Row {
    pub fn of(entry: &Entry, level: Option<Level>) -> Self {
        Self {
            name: entry.name.clone(),
            kind: entry.kind.clone(),
            context: entry.context.clone(),
            href: entry.href.clone(),
            level,
            lenses: entry.lenses.clone(),
        }
    }

    /// The dot's CSS class. A row with no level still gets an empty dot
    /// element, so that its name lines up with the names of the other
    /// rows.
    pub fn dot_class(&self) -> &'static str {
        match self.level {
            None => "dot",
            Some(Level::Healthy) => "dot dot-clear",
            Some(Level::Notice) => "dot concern-notice",
            Some(Level::Warning) => "dot concern-warning",
            Some(Level::Failing) => "dot concern-failing",
        }
    }
}

/// The id of the dialog's list of rows. The dialog's search field swaps
/// new rows into the element with this id.
pub const DIALOG_LIST: &str = "search-results";

/// The id of the `/search` page's list. Every page has the search
/// dialog, the search page too, so with one id the page's search field
/// would swap its rows into the hidden dialog's list.
pub const PAGE_LIST: &str = "search-page-results";

/// The rows that a query matched. The search field of the dialog or of
/// the search page swaps them in as the user types.
#[derive(Template)]
#[template(path = "search/results.html")]
pub struct Results {
    pub query: String,
    pub rows: Vec<Row>,
    /// The list's id: `DIALOG_LIST` or `PAGE_LIST`.
    pub list: &'static str,
}

/// `/search?q=` as a full page, for a request that opens the URL
/// directly, such as a link to a query.
#[derive(Template)]
#[template(path = "search/page.html")]
pub struct SearchPage {
    pub results: Results,
}

impl SearchPage {
    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![Crumb::home(), Crumb::current("search")]
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::the_dialogs_list(r#"id="search-results""#)]
    #[case::the_pages_list(r#"id="search-page-results""#)]
    #[case::the_pages_field_targets_its_own_list(r##"hx-target="#search-page-results""##)]
    fn the_search_page_gives_each_list_an_id_of_its_own(#[case] expected: &str) {
        let page = SearchPage {
            results: Results {
                query: "shop".into(),
                rows: Vec::new(),
                list: PAGE_LIST,
            },
        };
        let html = page.render().unwrap();
        assert_eq!(
            html.matches(expected).count(),
            1,
            "the page has {expected:?} other than once"
        );
    }
}
