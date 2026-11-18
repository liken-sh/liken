//! The page that lists one custom kind's objects in one namespace. It
//! shows a table beside the kind's card. Each row has the object's
//! name, a health dot, the condition that sets the dot, and the kind's
//! printer columns.

use askama::Template;

use super::{KindCard, KindObject};
use crate::map::KnownEntry as Entry;
use transect_frame::frame::crumbs::Crumb;

#[derive(Template)]
#[template(path = "objects/list.html", blocks = ["main"])]
pub struct ListPage {
    pub namespace: String,
    pub layer: String,
    /// The kind's plural, "Certificates".
    pub label: String,
    /// The page's path, which the URL of its event stream includes.
    pub path: String,
    /// The link to the kind's objects in every namespace.
    pub everywhere: String,
    /// The namespace's custom kinds, this one included.
    pub kind_links: Vec<Entry>,
    pub columns: Vec<String>,
    pub rows: Vec<KindObject>,
    pub card: KindCard,
}

impl ListPage {
    /// The table has the name column, the condition column, and the
    /// printer columns.
    pub fn wide(&self) -> bool {
        super::kind::wide_table(self.columns.len() + 2)
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(
                &self.namespace,
                transect_graph::links::namespace(&self.namespace),
            ),
            Crumb::current(&self.label),
        ]
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }

    /// The text for when the namespace has no objects of this kind.
    pub fn none(&self) -> String {
        format!("No {} in {} now.", self.label, self.namespace)
    }
}
