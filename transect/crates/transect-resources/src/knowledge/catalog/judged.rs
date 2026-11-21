//! What a pattern's module reads of the cluster, and what it returns:
//! the parts of each derived role, the lines of what the pattern
//! expects, and the parts that those lines mark. The page draws the
//! derived parts and the marks with the code that draws every sketch,
//! so a pattern's module writes no markup.

use super::super::KindKnowledge;
use super::super::role::First;
use super::super::through::{Followed, ReferenceEnds};
use super::Pattern;
use transect_graph::edges::Object;
use transect_graph::links::{Piece, text};

/// What a pattern's module reads of the cluster beyond the references
/// of the page's object, which it follows through `ReferenceEnds`. Only
/// the binary reads the stores, so it gives the module each fact.
pub trait Cluster: ReferenceEnds {
    /// The object that controls `object`, when exactly one does.
    fn controller(&self, object: &Object) -> Option<Object>;
    /// Whether the `Pod` is ready, or none when the cluster has no such
    /// `Pod`.
    fn ready(&self, pod: &Object) -> Option<bool>;
    /// The pods behind the ready endpoints of the `Service`'s
    /// `EndpointSlice`s, or none when the cluster has no such `Service`.
    fn endpoints(&self, service: &Object) -> Option<Vec<Object>>;
}

/// The parts of a derived role, or no part and the reason.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Derived {
    Parts(Vec<Object>),
    /// The role has no part now, and it is not missing one, so the page
    /// draws its place at rest with this reason.
    Nothing(String),
    /// The knowledge names the role's part, and the cluster has nothing
    /// that matches it, so the page draws the place as missing its part,
    /// with this reason.
    Missing(String),
}

/// The parts of each derived role of one object, by the role's name.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Derivations(Vec<(String, Derived)>);

impl Derivations {
    /// What the pattern's module derived for `role`.
    pub fn of(&self, role: &str) -> Option<&Derived> {
        let found = self.0.iter().find(|(name, _)| name == role);
        found.map(|(_, derived)| derived)
    }
}

impl FromIterator<(String, Derived)> for Derivations {
    fn from_iter<I: IntoIterator<Item = (String, Derived)>>(iter: I) -> Self {
        Self(iter.into_iter().collect())
    }
}

/// One object whose kind fills a pattern, as the pattern's module reads
/// it: the object, the knowledge of its kind, and the cluster.
pub struct Occurrence<'a> {
    pub object: &'a Object,
    pub knowledge: &'a KindKnowledge,
    pub pattern: &'a Pattern,
    pub cluster: &'a dyn Cluster,
    /// The parts of each derived role, once `judge` has derived them.
    pub derived: Derivations,
}

impl<'a> Occurrence<'a> {
    /// The objects that the reference filling `role` names, in its
    /// order, or none when the kind does not fill the role.
    pub fn filled(&self, role: &str) -> Option<Vec<Object>> {
        let path = self.path(role)?;
        let ends = self.cluster.ends(self.object, path).into_iter();
        let objects = ends.filter_map(|end| match end {
            Followed::Found(object, _)
            | Followed::Missing(object)
            | Followed::Unwatched(object) => Some(object),
            Followed::Ambiguous(_) => None,
        });
        Some(objects.collect())
    }

    /// The path of the reference that fills `role`, when the kind fills
    /// it.
    pub fn path(&self, role: &str) -> Option<&str> {
        let found = self.pattern.roles.iter().find(|r| r.name == role)?;
        match &found.fill.first {
            First::Reference(path) => Some(path),
            _ => None,
        }
    }

    /// The field that fills `role`, as a page names it for the object:
    /// a name template is the name that it makes.
    pub fn field(&self, role: &str) -> Option<String> {
        let path = self.path(role)?;
        let references = &self.knowledge.references;
        let found = references.iter().find(|reference| reference.path == path);
        Some(found.map_or_else(|| path.into(), |reference| reference.field_of(self.object)))
    }
}

/// The pattern as `transect` reads it for one object.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Judged {
    /// The parts of each derived role, by the role's name.
    pub derived: Derivations,
    /// What the pattern expects, in the catalog's order.
    pub lines: Vec<Line>,
    /// The parts that the lines mark.
    pub marks: Marks,
}

/// One line of what a pattern expects.
#[derive(Clone, Debug, PartialEq)]
pub struct Line {
    /// What the pattern expects, such as "writes reach only the
    /// primary".
    pub expects: String,
    pub said: Said,
    /// What the cluster shows, such as "orders-rw reaches orders-1", or
    /// why the line is unmet, has no signal, or does not apply, with each
    /// object that it names, so the page links it.
    pub detail: Vec<Piece>,
    /// The fields that the line read, and that the catalog states it.
    pub source: String,
}

impl Line {
    /// The detail as plain text.
    pub fn detail_text(&self) -> String {
        text(&self.detail)
    }
}

/// What the cluster says about one line.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Said {
    Met,
    Unmet,
    /// The line reads a field that the kind does not fill or that names
    /// nothing now.
    NoSignal,
    /// The line does not apply to this object, such as a line about
    /// parts that the object has none of.
    Moot,
}

/// A part that a line of the pattern marks.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Mark {
    pub object: Object,
    pub marked: Marked,
    /// The words that the part shows as its reason. They name no other
    /// object, because the part's line has no link but its own name.
    pub why: String,
}

/// How a line marks a part.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Marked {
    /// An expectation about the part is unmet, so it is drawn as a
    /// warning, unless its health is failing.
    Unmet,
    /// The part has nothing to do for this object, such as a `Service`
    /// that selects replicas when there is none, so it is drawn at rest.
    Rests,
}

/// The marks of one object's parts: at most one for each part. An unmet
/// line says more than a part at rest, so its mark replaces a rest, and
/// the first unmet line that names a part marks it.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Marks(Vec<Mark>);

impl Marks {
    /// Marks `object` as the part that an unmet line names.
    pub fn unmet(&mut self, object: &Object, why: &str) {
        match self.0.iter_mut().find(|mark| mark.object == *object) {
            Some(mark) if mark.marked == Marked::Unmet => {}
            Some(mark) => {
                mark.marked = Marked::Unmet;
                mark.why = why.into();
            }
            None => self.0.push(Mark {
                object: object.clone(),
                marked: Marked::Unmet,
                why: why.into(),
            }),
        }
    }

    /// Marks `object` at rest, unless a line marks it already.
    pub fn rests(&mut self, object: &Object, why: &str) {
        if self.of(object).is_none() {
            self.0.push(Mark {
                object: object.clone(),
                marked: Marked::Rests,
                why: why.into(),
            });
        }
    }

    /// The mark on `object`, when one marks it.
    pub fn of(&self, object: &Object) -> Option<&Mark> {
        self.0.iter().find(|mark| mark.object == *object)
    }

    /// Each mark, in the order of the lines that made it.
    pub fn all(&self) -> &[Mark] {
        &self.0
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn pod(name: &str) -> Object {
        Object::new("", "Pod", Some("books"), name)
    }

    /// Each mark as its part's name, how it is marked, and its words.
    fn marked(marks: &Marks) -> Vec<(String, Marked, String)> {
        let one = |mark: &Mark| (mark.object.name.clone(), mark.marked, mark.why.clone());
        marks.all().iter().map(one).collect()
    }

    #[rstest]
    fn an_unmet_line_replaces_a_rest_and_keeps_the_first_unmet() {
        let mut marks = Marks::default();
        marks.rests(&pod("a"), "idle");
        marks.unmet(&pod("a"), "not ready");
        marks.unmet(&pod("a"), "not the target");
        marks.unmet(&pod("b"), "not ready");
        marks.rests(&pod("b"), "idle");
        assert_eq!(
            marked(&marks),
            vec![
                ("a".to_string(), Marked::Unmet, "not ready".to_string()),
                ("b".to_string(), Marked::Unmet, "not ready".to_string())
            ]
        );
    }
}
