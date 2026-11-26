//! The parts that fill a role, before the page draws them: the values of
//! a reference of the object, or the objects that name it, and with
//! `through`, the objects at the end of one more reference of each.
//! `knowledge::follow` follows that reference, through the
//! `ReferenceEnds` that the board gives, so a role follows a reference
//! the way a reading and a health do. A role of a pattern of the catalog
//! that the pattern derives reads the parts that `catalog::judge`
//! derived when the page built its board. A role can also be the
//! controller of the page's object, and `owns` replaces each part with
//! the objects of a kind that it owns.

use super::super::board::Board;
use super::super::board::Subject;
use transect_graph::edges::Object;
use transect_resources::knowledge::catalog::Derived;
use transect_resources::knowledge::role::{First, Role};
use transect_resources::knowledge::{Followed, Names, Reached, Relation, Through};

/// One part that a role's fill reads.
#[derive(Debug)]
pub enum Found<'s> {
    /// A value of the reference of the object whose elevation is drawn.
    Value(&'s Relation, &'s Reached),
    /// An object that names that object, or one at the end of `through`.
    Object(Object),
    /// The value of `through` in `from` matches this many objects, so it
    /// names none of them.
    Ambiguous {
        count: usize,
        from: Object,
        path: String,
    },
}

impl Found<'_> {
    /// The object that the part is, when it is one.
    pub fn object(&self) -> Option<&Object> {
        match self {
            Self::Value(relation, _) => match &relation.names {
                Names::Object(object) => Some(object),
                _ => None,
            },
            Self::Object(object) => Some(object),
            Self::Ambiguous { .. } => None,
        }
    }
}

/// Each part that `role` reads for `subject`, in order. An object that
/// the fill reaches twice, such as a shelf that two bins name, is one
/// part.
pub fn found<'s>(board: &Board, subject: &'s Subject, role: &Role) -> Vec<Found<'s>> {
    let first: Vec<Found<'s>> = match &role.fill.first {
        First::Reference(path) => subject
            .reached
            .iter()
            .filter(|(relation, _)| relation.path == *path)
            .map(|(relation, reached)| Found::Value(relation, reached))
            .collect(),
        First::NamedBy(named) => {
            let named = (board.named_by)(&subject.object, named);
            named.into_iter().map(Found::Object).collect()
        }
        First::Controller => {
            let controller = (board.controller)(&subject.object);
            controller.into_iter().map(Found::Object).collect()
        }
        First::Derived(_) => match derived(board, subject, role) {
            Some(Derived::Parts(parts)) => parts.iter().cloned().map(Found::Object).collect(),
            _ => Vec::new(),
        },
        First::Unfilled => Vec::new(),
    };
    let found = match &role.fill.through {
        None => first,
        Some(through) => first
            .iter()
            .filter_map(Found::object)
            .flat_map(|object| follow(board, object, through))
            .collect(),
    };
    let found = match &role.fill.owns {
        None => found,
        Some(kind) => found
            .iter()
            .filter_map(Found::object)
            .flat_map(|object| (board.owned)(object, kind))
            .map(Found::Object)
            .collect(),
    };
    let mut once: Vec<Found<'s>> = Vec::new();
    for one in found {
        let seen = one.object().is_some_and(|object| {
            let mut earlier = once.iter().filter_map(Found::object);
            earlier.any(|other| other == object)
        });
        if !seen {
            once.push(one);
        }
    }
    once
}

/// The parts that the pattern's module derived for `role`. Only the
/// page's own object has a pattern that the board read, so a role of an
/// elevation drawn inside a part derives nothing.
pub fn derived<'b>(board: &'b Board, subject: &Subject, role: &Role) -> Option<&'b Derived> {
    if subject.object != board.subject.object {
        return None;
    }
    board.judged?.derived.of(&role.name)
}

/// The ends of `through` from `object`. An end is a part whether the
/// cluster has its object or not, because the page draws an object that
/// is missing as plan 13 draws a name with no object. A reference that
/// `Kinds::resolve` could not confirm has no end, and the role shows why.
fn follow<'s>(board: &Board, object: &Object, through: &Through) -> Vec<Found<'s>> {
    if through.unresolved.is_some() {
        return Vec::new();
    }
    let ends = board.ends.ends(object, &through.path).into_iter();
    ends.map(|end| match end {
        Followed::Found(end, _) | Followed::Missing(end) | Followed::Unwatched(end) => {
            Found::Object(end)
        }
        Followed::Ambiguous(count) => Found::Ambiguous {
            count,
            from: object.clone(),
            path: through.path.clone(),
        },
    })
    .collect()
}
