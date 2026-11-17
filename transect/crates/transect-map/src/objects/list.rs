//! One kind's objects in one namespace: a table of names, each with its
//! dot, the condition that gave it, and its printer columns, beside the
//! kind's card.

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
    /// The page's own path, which its event stream's URL includes.
    pub path: String,
    /// The kind's objects in every namespace.
    pub everywhere: String,
    /// The namespace's custom kinds, this one among them.
    pub strip: Vec<Entry>,
    pub columns: Vec<String>,
    pub rows: Vec<KindObject>,
    pub card: KindCard,
}

impl ListPage {
    /// Its table has the name, the condition, and each printer column.
    pub fn wide(&self) -> bool {
        super::kind::wide_table(self.columns.len() + 2)
    }

    /// The crumb: the cluster, the layer with this namespace selected,
    /// the namespace, and the kind.
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

    /// The line in the box when the namespace has none of them.
    pub fn none(&self) -> String {
        format!("No {} in {} now.", self.label, self.namespace)
    }
}
