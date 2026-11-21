//! What the page of an object of a known kind reads of the cluster:
//! the object itself, as a `Subject`, and the lookups that the ring of
//! boxes and the sketch ask about other objects. Only the binary reads
//! the stores, so it gives the page each lookup as a function.

use super::Chip;
use super::Entry;
use crate::objects::Condition;
use crate::shapes::text::{Reading, State, Why};
use transect_graph::edges::{Object, Plurals};
use transect_observe::health::Level;
use transect_resources::knowledge::catalog::Judged;
use transect_resources::knowledge::role::{Column, NamedBy};
use transect_resources::knowledge::{Count, KindKnowledge, Reached, ReferenceEnds, Relation};

/// What `KnownPage::build` needs to draw the page of one object.
pub struct Board<'a> {
    /// The page's object, its kind's knowledge, its relations, and its
    /// conditions.
    pub subject: Subject<'a>,
    pub layer: String,
    /// The object's path.
    pub path: String,
    /// The slug of the elevation that the URL names, or none at the
    /// object's own URL.
    pub elevation: Option<&'a str>,
    pub column: Vec<Entry>,
    pub counts: Vec<Count>,
    pub chips: Vec<Chip>,
    pub level: Level,
    pub resting: Option<String>,
    pub plurals: &'a Plurals,
    /// The page URL of an object, when it has a page.
    pub page: &'a dyn Fn(&Object) -> Option<String>,
    /// The URL of the ground page of a system outside the cluster, by
    /// the name of its `Endpoint::Outside`, when it has one.
    pub ground: &'a dyn Fn(&str) -> Option<String>,
    /// The text that names a device on a line.
    pub device: &'a dyn Fn(&transect_graph::edges::Device) -> String,
    /// What the cluster says about an object that a part of the sketch
    /// names.
    pub standing: &'a dyn Fn(&Object) -> Standing,
    /// The readings of an object: each of the columns in the list that
    /// has a value, of the object or of the object at the end of the
    /// column's reference, in the list's order. A sketch reads the
    /// objects that its parts name and the page's own object.
    pub readings: &'a dyn Fn(&Object, &[Column]) -> Vec<Reading>,
    /// What a sketch reads of another object, which a part of the page's
    /// sketch draws as an elevation of the part's kind. It is none for an
    /// object that the cluster does not have or whose kind has no
    /// knowledge.
    pub subjects: &'a dyn Fn(&Object) -> Option<Subject<'a>>,
    /// The objects of the kind of a `namedBy` whose field at its path
    /// names an object, for a role whose parts name the object whose
    /// elevation is drawn.
    pub named_by: &'a dyn Fn(&Object, &NamedBy) -> Vec<Object>,
    /// The ends of the references of objects, for a role that follows
    /// one more reference.
    pub ends: &'a dyn ReferenceEnds,
    /// The pattern of the catalog that the kind of the page's object
    /// fills, as `catalog::judge` read it for that object: the parts of
    /// each derived role, what the pattern expects, and the parts that
    /// those lines mark. It is none for a kind with no pattern.
    pub judged: Option<&'a Judged>,
}

/// An object whose elevation a sketch draws, with what the sketch reads
/// of it: the page's own object, or an object that a part of the page's
/// sketch draws as an elevation of the part's kind.
pub struct Subject<'a> {
    pub object: Object,
    /// The knowledge of the object's kind, which states its roles and
    /// elevations.
    pub knowledge: &'a KindKnowledge,
    /// Each relation read from the object's fields, with what it
    /// resolves to.
    pub reached: Vec<(&'a Relation, Reached)>,
    /// The object's conditions, which say whether a role that is
    /// expected in some state is expected now.
    pub conditions: Vec<Condition>,
}

/// What the cluster says about an object that a part names. Only the
/// binary crate reads the stores, so the page asks through
/// `Board::standing`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Standing {
    /// The object is drawn plainly: it is healthy, its kind has no
    /// knowledge that gives it a health, or `transect` does not watch
    /// its kind and so cannot say whether the object exists.
    Plain,
    /// `transect` watches the kind, and the cluster has no such object.
    Missing,
    Failing(Why),
    Warning(Why),
    /// The object's health condition is `False` with a calm reason.
    Resting(Why),
}

impl Standing {
    /// The state that a part of the object is drawn in, with the reason
    /// of its health.
    pub fn state(self) -> (State, Option<Why>) {
        match self {
            Self::Plain => (State::Plain, None),
            Self::Missing => (State::NoObject, None),
            Self::Failing(why) => (State::Failing, Some(why)),
            Self::Warning(why) => (State::Warning, Some(why)),
            Self::Resting(why) => (State::Resting, Some(why)),
        }
    }
}
