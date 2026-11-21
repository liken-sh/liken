//! `through` follows one more reference. A role's reading can name a
//! printer column of the object at the end of a reference of the part's
//! kind, and a kind's health can be the health of the object at the end
//! of one of its own references, for a kind that has no condition of
//! its own.
//!
//! `follow` is the one place that follows a reference. A page reads the
//! relations of an object and the stores of the cluster, so the binary
//! gives `follow` both through `ReferenceEnds`. `Kinds::resolve` checks
//! each `through` that only the knowledge of another kind can confirm.

use kube::core::DynamicObject;

use super::references::{Names, Relation};
use transect_graph::edges::Object;

/// One more reference to follow.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Through {
    /// The `path` of an entry of `references` that names a kind.
    pub path: String,
    /// Why `Kinds::resolve` could not confirm the reference in the
    /// knowledge of the kind it starts from. The page shows it in place
    /// of the reading, the health, or the parts that the reference leads
    /// to.
    pub unresolved: Option<String>,
    /// The group and the kind that the reference names, which
    /// `Kinds::resolve` finds in the knowledge of the kind it starts
    /// from. A role draws its parts in the shape of this kind.
    pub target: Option<(String, String)>,
}

impl Through {
    /// The reference at `path`, before `Kinds::resolve` checks it.
    pub fn new(path: impl Into<String>) -> Self {
        Self {
            path: path.into(),
            unresolved: None,
            target: None,
        }
    }
}

/// One end of a reference of an object: where following one of the
/// values at the reference's path leads.
#[derive(Debug, PartialEq)]
pub enum Followed<'a> {
    /// The object that the value names, as the cluster stores it.
    Found(Object, &'a DynamicObject),
    /// The value matches this many objects, so it names none of them.
    Ambiguous(usize),
    /// The value names an object of a kind that `transect` watches,
    /// and the cluster has no such object.
    Missing(Object),
    /// The value names an object of a kind that `transect` does not
    /// watch, so it cannot say whether the object exists.
    Unwatched(Object),
}

/// The ends of the references of objects in the cluster. Following a
/// reference needs the relations of an object and the stores, and only
/// the binary reads both, so a page asks through this.
pub trait ReferenceEnds {
    /// Each end of the reference at `path` of `object`'s kind, one for
    /// each value at the path that names an object, in the order of the
    /// values. It is empty when the path names nothing.
    fn ends(&self, object: &Object, path: &str) -> Vec<Followed<'_>>;
}

/// Each end of the reference at `path` among `relations`, the relations
/// of one object. `stored` returns an object from the store of its kind,
/// with the outer `None` when no store holds the kind. A value that is
/// not an object's name, such as an address, has no end.
pub fn follow<'a>(
    relations: &[Relation],
    path: &str,
    stored: impl Fn(&Object) -> Option<Option<&'a DynamicObject>>,
) -> Vec<Followed<'a>> {
    let at = relations.iter().filter(|relation| relation.path == path);
    let end = |relation: &Relation| match &relation.names {
        Names::Object(object) => Some(match stored(object) {
            None => Followed::Unwatched(object.clone()),
            Some(None) => Followed::Missing(object.clone()),
            Some(Some(found)) => Followed::Found(object.clone(), found),
        }),
        Names::Ambiguous(count) => Some(Followed::Ambiguous(*count)),
        Names::Address | Names::Value => None,
    };
    at.filter_map(end).collect()
}

#[cfg(test)]
mod tests;
