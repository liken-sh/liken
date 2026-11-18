//! The breadcrumb trail in the page header. It runs from the cluster
//! down to the page shown, and every crumb but the last links to its
//! page.

#[derive(Debug, PartialEq)]
pub struct Crumb {
    pub text: String,
    /// The link target, or `None` for the page shown.
    pub href: Option<String>,
    /// Whether the crumb is an object's name, such as a namespace.
    /// `crumbs.html` draws a current crumb with `name` set in monospace,
    /// and ignores `name` on a link.
    pub name: bool,
}

impl Crumb {
    /// The first crumb of every page: "cluster", linking to the map with
    /// nothing selected.
    pub fn home() -> Self {
        Self::link("cluster", "/".into())
    }

    pub fn link(text: &str, href: String) -> Self {
        Self {
            text: text.into(),
            href: Some(href),
            name: false,
        }
    }

    /// A link to an object's page, such as the namespace above a
    /// workload.
    pub fn link_name(text: &str, href: String) -> Self {
        Self {
            name: true,
            ..Self::link(text, href)
        }
    }

    pub fn current(text: &str) -> Self {
        Self {
            text: text.into(),
            href: None,
            name: false,
        }
    }

    pub fn current_name(text: &str) -> Self {
        Self {
            name: true,
            ..Self::current(text)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_home_crumb_links_to_the_map_with_nothing_selected() {
        assert_eq!(Crumb::home().href.as_deref(), Some("/"));
    }
}
