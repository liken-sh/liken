//! What a query shows: its rows, as the dialog's fragment and as the
//! `/search` page, each row with a health dot, its name, its kind,
//! where it is, and its lens chips.

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
    /// The health of what it opens, or none for a kind transect does
    /// not assess, which shows no dot.
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

    /// The dot's class: clear when healthy, as a card's dot is.  A row
    /// with no assessed health keeps the dot's space with nothing drawn
    /// in it, so its name lines up with the others and shows no health.
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

/// The id of the dialog's list of rows, which its field targets.
pub const DIALOG_LIST: &str = "search-results";

/// The id of the `/search` page's own list.  The dialog is on every
/// page, the search page too, so the two lists need ids of their own,
/// or the page's field would swap its rows into the hidden dialog.
pub const PAGE_LIST: &str = "search-page-results";

/// The rows a query found, which the dialog swaps in as it is typed.
#[derive(Template)]
#[template(path = "search/results.html")]
pub struct Results {
    pub query: String,
    pub rows: Vec<Row>,
    /// The list's id: `DIALOG_LIST` or `PAGE_LIST`.
    pub list: &'static str,
}

/// `/search?q=` as a page of its own, for a browser without the dialog
/// or a link to a query.
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
        assert_eq!(html.matches(expected).count(), 1, "{expected}");
    }
}
