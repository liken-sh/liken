//! The card beside an object's board, in the sidebar every elevation
//! has: what its kind is, its health, the workload whose controller
//! reconciles it, where its code is, and its kind's knowledge.

use transect_graph::edges::provenance::legend;
use transect_graph::links::Reference;
use transect_observe::health::Level;
use transect_resources::intent::Line;
use transect_resources::knowledge::KnownThrough;

#[derive(Debug, PartialEq)]
pub struct ObjectCard {
    pub kind: String,
    pub name: String,
    pub level: Option<Level>,
    pub resting: bool,
    /// The description of its kind from its CRD.
    pub why: Option<Line>,
    pub controllers: Vec<Reference>,
    /// The GitOps objects that apply it.
    pub code: Vec<Reference>,
    /// Its kind's knowledge, for a kind that has knowledge.
    pub known: Option<KnownThrough>,
    /// Its kind and group, namespace, and age, each a label and a value,
    /// which the card shows when it would otherwise show only a title.
    pub facts: Vec<(&'static str, String)>,
}

impl ObjectCard {
    /// Beside the dot: its health in a word, or "at rest", or none for
    /// an object that reports no condition, which shows no dot.
    pub fn status_text(&self) -> Option<&'static str> {
        Some(match (self.level?, self.resting) {
            (_, true) => "at rest",
            (Level::Healthy, false) => "healthy",
            (Level::Notice, false) => "worth noting",
            (Level::Warning, false) => "warning",
            (Level::Failing, false) => "failing",
        })
    }

    pub fn dot_class(&self) -> &'static str {
        match (self.level, self.resting) {
            (Some(level), false) => super::dot_class(level),
            _ => "dot dot-gray",
        }
    }

    /// Whether the card has nothing but its title, and so shows its
    /// plain facts.
    pub fn bare(&self) -> bool {
        self.why.is_none()
            && self.controllers.is_empty()
            && self.code.is_empty()
            && self.known.is_none()
    }

    /// The legend line, when any line on the card is marked.
    pub fn legend(&self) -> Option<String> {
        let why = self.why.iter().map(|line| &line.provenance);
        let known = self.known.iter().flat_map(|known| known.provenances());
        legend(why.chain(known))
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn card(level: Option<Level>, resting: bool) -> ObjectCard {
        ObjectCard {
            kind: "Gizmo".into(),
            name: "red".into(),
            level,
            resting,
            why: None,
            controllers: Vec::new(),
            code: Vec::new(),
            known: None,
            facts: Vec::new(),
        }
    }

    #[rstest]
    #[case::healthy(Some(Level::Healthy), false, Some("healthy"), "dot dot-clear")]
    #[case::at_rest(Some(Level::Warning), true, Some("at rest"), "dot dot-gray")]
    #[case::failing(Some(Level::Failing), false, Some("failing"), "dot concern-failing")]
    #[case::no_condition(None, false, None, "dot dot-gray")]
    fn says_its_health_in_a_word_beside_its_dot(
        #[case] level: Option<Level>,
        #[case] resting: bool,
        #[case] text: Option<&str>,
        #[case] dot: &str,
    ) {
        let card = card(level, resting);
        assert_eq!((card.status_text(), card.dot_class()), (text, dot));
    }
}
