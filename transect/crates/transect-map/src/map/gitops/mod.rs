//! A GitOps object's page: an applier or a source.  These objects are
//! not workloads and run no pods, so the page is the card alone: where
//! the object fetches from, whether its tool reports it ready, the
//! revision it last applied or fetched, and what reconciles from it,
//! grouped by namespace and kind, each linked to its own page.

mod sentence;

use std::collections::BTreeMap;

use askama::Template;

use super::EdgeRow;
use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::Object;
use transect_supply::gitops::Detail;

#[derive(Template)]
#[template(path = "map/gitops/gitops.html", blocks = ["main"])]
pub struct GitOpsPage {
    pub edge_row: EdgeRow,
    pub detail: Detail,
    /// The layer its namespace is in, for the crumb, when the map
    /// draws the namespace.
    pub layer: Option<String>,
    /// Its source, linked to the source's page when it has one.
    pub source: Option<Member>,
    /// What reconciles from it, by namespace and kind.
    pub reconciling: Vec<Group>,
    /// The page's own path, which its event stream's URL includes.
    pub path: String,
    /// The GitOps objects of its kind in its namespace, itself among
    /// them, for the row above the page, as a workload page lists its
    /// namespace's workloads.
    pub siblings: Vec<Sibling>,
}

/// One GitOps object in the row of its kind's objects.
#[derive(Debug, PartialEq)]
pub struct Sibling {
    pub name: String,
    pub href: Option<String>,
    pub current: bool,
}

/// The objects of one kind in one namespace that reconcile from it.
#[derive(Debug, PartialEq)]
pub struct Group {
    pub namespace: Option<String>,
    pub kind: String,
    pub members: Vec<Member>,
}

#[derive(Debug, PartialEq)]
pub struct Member {
    pub text: String,
    pub href: Option<String>,
}

impl GitOpsPage {
    /// The page for `detail`, at `path`, with `reconciling`, the objects
    /// whose reconciles-from edges end at it, and `href`, the page of
    /// any object that has one.
    pub fn build<'a>(
        detail: Detail,
        layer: Option<String>,
        reconciling: impl IntoIterator<Item = &'a Object>,
        href: impl Fn(&Object) -> Option<String>,
        path: String,
    ) -> Self {
        let source = detail.source.as_ref().map(|source| Member {
            text: format!("{} {}", source.kind, named(source, &detail.object)),
            href: href(source),
        });
        Self {
            edge_row: EdgeRow::default(),
            reconciling: groups(reconciling, &href),
            source,
            detail,
            layer,
            path,
            siblings: Vec::new(),
        }
    }

    /// The page with the row of `objects`, the GitOps objects of its
    /// kind in its namespace, each linked by `href`.
    pub fn with_siblings<'a>(
        self,
        objects: impl IntoIterator<Item = &'a Object>,
        href: impl Fn(&Object) -> Option<String>,
    ) -> Self {
        let mut siblings: Vec<Sibling> = objects
            .into_iter()
            .filter(|object| {
                object.kind == self.detail.object.kind
                    && object.namespace == self.detail.object.namespace
            })
            .map(|object| Sibling {
                name: object.name.clone(),
                href: href(object),
                current: *object == self.detail.object,
            })
            .collect();
        siblings.sort_by(|a, b| a.name.cmp(&b.name));
        siblings.dedup();
        Self { siblings, ..self }
    }

    /// The page with the main column's row of edge kinds.
    pub fn with_edge_row(self, edge_row: EdgeRow) -> Self {
        Self { edge_row, ..self }
    }

    pub fn name(&self) -> &str {
        &self.detail.object.name
    }

    pub fn namespace(&self) -> &str {
        self.detail.object.namespace.as_deref().unwrap_or_default()
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        let namespace = self.namespace();
        let select = format!("/?select={namespace}");
        let layer = self
            .layer
            .iter()
            .map(|layer| Crumb::link(layer, format!("{select}#{layer}")));
        [Crumb::home()]
            .into_iter()
            .chain(layer)
            .chain([
                Crumb::link_name(namespace, format!("/namespaces/{namespace}")),
                Crumb::current_name(&self.title()),
            ])
            .collect()
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }
}

/// `objects` by namespace, then kind, each group's names sorted.
fn groups<'a>(
    objects: impl IntoIterator<Item = &'a Object>,
    href: &impl Fn(&Object) -> Option<String>,
) -> Vec<Group> {
    let mut by: BTreeMap<(Option<&str>, &str), Vec<&Object>> = BTreeMap::new();
    for object in objects {
        let key = (object.namespace.as_deref(), object.kind.as_str());
        by.entry(key).or_default().push(object);
    }
    by.into_iter()
        .map(|((namespace, kind), mut objects)| {
            objects.sort_by(|a, b| a.name.cmp(&b.name));
            objects.dedup();
            Group {
                namespace: namespace.map(Into::into),
                kind: kind.into(),
                members: objects
                    .into_iter()
                    .map(|object| Member {
                        text: object.name.clone(),
                        href: href(object),
                    })
                    .collect(),
            }
        })
        .collect()
}

/// `object`'s name, after its namespace when that is not `here`'s.
fn named(object: &Object, here: &Object) -> String {
    transect_graph::links::name_in(object, here.namespace.as_deref())
}

#[cfg(test)]
mod tests;
