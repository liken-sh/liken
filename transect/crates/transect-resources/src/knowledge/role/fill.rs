//! Where the parts of a role come from. A role reads a reference of the
//! page's own object, such as the screen that a kiosk names, or the
//! objects of another kind that name the page's object, such as the
//! shelves that name their room. Either first step can follow one more
//! reference, so a role can draw the shelf that a bin names.
//!
//! The objects that name a page's object are read from the same
//! relations as every reference: `namedBy` names a reference that the
//! knowledge of the other kind states, so its rules of matching apply,
//! and that knowledge makes `transect` keep the other kind's spec in its
//! watch.

use serde::Deserialize;

use super::super::Reference;
use super::super::references::{Names, Relation, target_of};
use super::super::through::Through;
use transect_graph::edges::Object;

/// Where a role's parts come from: a first step, and one more reference
/// to follow from each object that it reads.
#[derive(Clone, Debug, PartialEq)]
pub struct Fill {
    pub first: First,
    /// The reference to follow from each object of the first step. Its
    /// path is a reference of the first step's kind, so only
    /// `Kinds::resolve` can confirm it, and it finds the kind of the
    /// parts there.
    pub through: Option<Through>,
    /// The kind whose objects replace each part: the objects of that
    /// kind that the part owns, by the ownership index. A `Session` owns
    /// the `Job` of each turn, and the `Job`s are the parts that run.
    pub owns: Option<String>,
}

/// The first step of a role's parts.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum First {
    /// The values of the reference at this path of the page's object.
    Reference(String),
    /// The objects of another kind whose field names the page's object.
    NamedBy(NamedBy),
    /// The workload that controls the page's object, which the
    /// knowledge of the object's kind finds by its controller's image.
    Controller,
    /// The parts that a function of a pattern's module derives, such as
    /// the instances other than the primary. It holds the name that the
    /// catalog writes in `derive`.
    Derived(String),
    /// A role of a pattern that the kind's `pattern` does not fill. It
    /// has no part, and the role's `unresolved` says why.
    Unfilled,
}

/// The objects of `kind` whose field at `path` names the page's object.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NamedBy {
    pub kind: String,
    /// The kind's group, which is the group of the page's kind unless
    /// the role states another.
    pub group: String,
    pub path: String,
}

/// The fields of a role that say where its parts come from, as written.
#[derive(Default)]
pub struct Written {
    pub reference: Option<String>,
    pub named_by: Option<WrittenNamedBy>,
    pub controller: bool,
    pub through: Option<String>,
    pub owns: Option<String>,
}

/// `namedBy`, as written. Each field is optional here so that the
/// parser can name the one that is missing.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct WrittenNamedBy {
    kind: Option<String>,
    group: Option<String>,
    path: Option<String>,
}

impl Fill {
    /// The fill whose first step is `first`, with no `through`, for a
    /// role of a pattern of the catalog.
    pub fn of(first: First) -> Self {
        Self {
            first,
            through: None,
            owns: None,
        }
    }

    /// The fill of a role that writes one of `reference`, `named_by`,
    /// and `controller`, and `through` and `owns`, checked against
    /// `references`, the document's own. A `namedBy` with no group takes
    /// `group`, the group of the document's kind.
    pub fn read(written: Written, references: &[Reference], group: &str) -> Result<Self, String> {
        let Written {
            reference,
            named_by,
            controller,
            through,
            owns,
        } = written;
        let first = match (reference, named_by, controller) {
            (None, None, false) => {
                return Err(
                    "the role has none of reference, namedBy, and controller, so nothing gives it parts".into(),
                );
            }
            (Some(_), Some(_), _) | (Some(_), _, true) | (_, Some(_), true) => {
                return Err(
                    "the role has more than one of reference, namedBy, and controller, and its parts come from one of them"
                        .into(),
                );
            }
            (None, None, true) => First::Controller,
            (Some(path), None, false) => {
                if !references.iter().any(|r| r.path == path) {
                    return Err(format!(
                        "reference {path} is the path of no entry of references"
                    ));
                }
                First::Reference(path)
            }
            (None, Some(written), false) => First::NamedBy(NamedBy::read(written, group)?),
        };
        let through = match through {
            Some(path) if path.trim().is_empty() => return Err("through is empty".into()),
            Some(path) => Some(Through::new(path)),
            None => None,
        };
        if owns.as_deref().is_some_and(|kind| kind.trim().is_empty()) {
            return Err("owns is empty".into());
        }
        let fill = Self {
            first,
            through,
            owns,
        };
        if let (Some(through), None) = (&fill.through, fill.first_kind(references)) {
            return Err(format!(
                "the role follows {} from {}, which names no kind",
                through.path,
                fill.first.fields()
            ));
        }
        Ok(fill)
    }

    /// The group and the kind of the objects of the first step, or none
    /// when its reference reads values that name no kind.
    pub fn first_kind<'a>(&'a self, references: &'a [Reference]) -> Option<(&'a str, &'a str)> {
        match &self.first {
            First::Reference(path) => target_of(references, path),
            First::NamedBy(named) => Some((&named.group, &named.kind)),
            First::Controller | First::Derived(_) | First::Unfilled => None,
        }
    }

    /// The group and the kind of the role's parts: none with `owns`, the
    /// kind at the end of `through`, once `Kinds::resolve` has found it,
    /// or the kind of the first step.
    pub fn parts_kind<'a>(&'a self, references: &'a [Reference]) -> Option<(&'a str, &'a str)> {
        // `owns` names a kind by its name alone, as an owner reference's
        // child is listed in the ownership index, so its group is not
        // known here and no knowledge of the kind can be found.
        if self.owns.is_some() {
            return None;
        }
        match &self.through {
            Some(through) => through
                .target
                .as_ref()
                .map(|(group, kind)| (group.as_str(), kind.as_str())),
            None => self.first_kind(references),
        }
    }

    /// The path of the page's own reference that the role reads, when
    /// its first step is one.
    pub fn reference(&self) -> Option<&str> {
        match &self.first {
            First::Reference(path) => Some(path),
            First::NamedBy(_) | First::Controller | First::Derived(_) | First::Unfilled => None,
        }
    }

    /// The fields that the parts are read from, such as
    /// "Shelf spec.room, then spec.label".
    pub fn fields(&self) -> String {
        self.then(self.first.fields())
    }

    /// The fields as a page names them for the page's object `object`,
    /// whose knowledge has `references`: a name template is the name
    /// that it makes for the object.
    pub fn fields_of(&self, references: &[Reference], object: &Object) -> String {
        let first = match &self.first {
            First::Reference(path) => references
                .iter()
                .find(|reference| reference.path == *path)
                .map_or_else(|| path.clone(), |reference| reference.field_of(object)),
            other => other.fields(),
        };
        self.then(first)
    }

    /// `first`, followed by the reference that `through` follows and the
    /// kind that `owns` takes.
    fn then(&self, first: String) -> String {
        let first = match &self.through {
            Some(through) => format!("{first}, then {}", through.path),
            None => first,
        };
        match &self.owns {
            Some(kind) => format!("{first}, then each {kind} it owns"),
            None => first,
        }
    }
}

impl First {
    /// The field of the first step: the page's own path, or the kind and
    /// the path of the objects that name the page's object.
    pub fn fields(&self) -> String {
        match self {
            Self::Reference(path) => path.clone(),
            Self::NamedBy(named) => format!("{} {}", named.kind, named.path),
            Self::Controller => "the controller".into(),
            Self::Derived(derive) => format!("{derive}, which transect derives"),
            Self::Unfilled => "no reference".into(),
        }
    }
}

impl NamedBy {
    fn read(written: WrittenNamedBy, group: &str) -> Result<Self, String> {
        let present = |field: Option<String>, name: &str| {
            field
                .filter(|value| !value.trim().is_empty())
                .ok_or_else(|| format!("namedBy has no {name}"))
        };
        Ok(Self {
            kind: present(written.kind, "kind")?,
            path: present(written.path, "path")?,
            group: written.group.unwrap_or_else(|| group.to_string()),
        })
    }

    /// Whether `referrer`, whose relations are `relations`, is an object
    /// of the kind that names `named` at the path.
    pub fn names(&self, referrer: &Object, relations: &[Relation], named: &Object) -> bool {
        let kind = (referrer.group.as_str(), referrer.kind.as_str());
        kind == (self.group.as_str(), self.kind.as_str())
            && relations.iter().any(|relation| {
                relation.path == self.path && relation.names == Names::Object(named.clone())
            })
    }
}

#[cfg(test)]
mod tests;
