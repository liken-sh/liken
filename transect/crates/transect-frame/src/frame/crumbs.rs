//! The elevation crumb in the masthead.  It lists each elevation from
//! the cluster down to the one shown, and every crumb but the last is
//! a link to its elevation.

#[derive(Debug, PartialEq)]
pub struct Crumb {
    pub text: String,
    /// Where the crumb leads, or none for the elevation shown.
    pub href: Option<String>,
    /// Whether the crumb names an object, such as a namespace, which the
    /// crumb draws in the map's monospace, as the object's parcel is.
    pub name: bool,
}

impl Crumb {
    /// The first crumb on every page: the cluster, a link to the map with
    /// nothing selected, so every page has a link back to the whole map.
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

    /// A link up to an object, such as the namespace above a workload.
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
    fn home_links_to_the_unselected_map() {
        assert_eq!(Crumb::home().href.as_deref(), Some("/"));
    }
}
