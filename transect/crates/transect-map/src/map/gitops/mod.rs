//! The page of a GitOps object, an applier or a source. These objects
//! are not workloads and run no pods, so the page is only the card:
//! where the object fetches from, whether its tool reports it ready, the
//! revision it last applied or fetched, and the objects that reconcile
//! from it, grouped by namespace and kind and linked to their pages.

mod sentence;

use std::collections::BTreeMap;

use askama::Template;

use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::Object;
use transect_supply::gitops::Detail;

#[derive(Template)]
#[template(path = "map/gitops/gitops.html", blocks = ["main"])]
pub struct GitOpsPage {
    pub detail: Detail,
    /// The layer of its namespace, for the crumb, when the map draws
    /// that namespace.
    pub layer: Option<String>,
    /// Its source, linked to the source's page when that page exists.
    pub source: Option<Member>,
    /// The objects that reconcile from it, by namespace and kind.
    pub reconciling: Vec<Group>,
    /// The page's own path, which is part of the URL of its event
    /// stream.
    pub path: String,
    /// The GitOps objects of its kind in its namespace, including
    /// itself, for the row above the page, like the row of workloads on
    /// a workload page.
    pub siblings: Vec<Sibling>,
}

/// One GitOps object in the row of objects of its kind.
#[derive(Debug, PartialEq)]
pub struct Sibling {
    pub name: String,
    pub href: Option<String>,
    pub current: bool,
}

/// The objects of one kind in one namespace that reconcile from the
/// page's object.
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
    /// The page for `detail`, at `path`. `reconciling` is the objects
    /// with a reconciles-from edge to it, and `href` gives the page of
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
            reconciling: groups(reconciling, &href),
            source,
            detail,
            layer,
            path,
            siblings: Vec::new(),
        }
    }

    /// Builds the row of siblings from the GitOps objects of `objects`
    /// that have this object's kind and namespace, each linked by
    /// `href`.
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

/// Groups `objects` by namespace and then kind. Each group's names are
/// sorted, with duplicates removed.
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

/// `object`'s name, with its namespace before it unless the namespace
/// is `here`'s.
fn named(object: &Object, here: &Object) -> String {
    transect_graph::links::name_in(object, here.namespace.as_deref())
}

#[cfg(test)]
mod tests;
