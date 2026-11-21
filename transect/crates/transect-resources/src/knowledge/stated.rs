//! The elevations that the page of an object can draw: the ones that the
//! knowledge of its kind states, then the ones of the pattern of the
//! catalog that the kind fills. Each comes with the roles that it places
//! and who states it, so one function draws an elevation of either
//! author.

use super::catalog::Pattern;
use super::role::Role;
use super::{Elevation, KindKnowledge};

/// The suffix of the slug of an elevation of the catalog whose slug the
/// kind's own elevations already have.
const BY_TRANSECT: &str = "-by-transect";

/// One elevation that the page of an object can draw.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Stated<'a> {
    pub elevation: &'a Elevation,
    /// The roles that the elevation places.
    pub roles: &'a [Role],
    /// The pattern of the catalog that states the elevation, or none for
    /// an elevation of the kind's own knowledge.
    pub pattern: Option<&'a Pattern>,
}

impl KindKnowledge {
    /// Each elevation that the page of an object of the kind can draw,
    /// with its slug: the kind's own, then its pattern's.
    pub fn stated(&self) -> Vec<(String, Stated<'_>)> {
        let own = self.elevations.iter().map(|elevation| {
            let stated = Stated {
                elevation,
                roles: &self.roles,
                pattern: None,
            };
            (elevation.slug.clone(), stated)
        });
        let mut stated: Vec<(String, Stated)> = own.collect();
        let Some(pattern) = &self.pattern else {
            return stated;
        };
        for elevation in &pattern.elevations {
            let taken = self.elevations.iter().any(|own| own.slug == elevation.slug);
            let slug = match taken {
                true => format!("{}{BY_TRANSECT}", elevation.slug),
                false => elevation.slug.clone(),
            };
            let one = Stated {
                elevation,
                roles: &pattern.roles,
                pattern: Some(pattern),
            };
            stated.push((slug, one));
        }
        stated
    }

    /// The elevation of the kind's own knowledge named `name`, which a
    /// role with `elevation` draws each part as.
    pub fn own_elevation(&self, name: &str) -> Option<Stated<'_>> {
        let elevation = self.elevations.iter().find(|e| e.name == name)?;
        Some(Stated {
            elevation,
            roles: &self.roles,
            pattern: None,
        })
    }
}

impl Stated<'_> {
    /// Who states the elevation's places and wires, as a wire's source
    /// names it: "the KindKnowledge <name>", or "transect's pattern
    /// <title>" for a pattern of the catalog.
    pub fn states(&self, knowledge: &str) -> String {
        match self.pattern {
            Some(pattern) => format!("transect's pattern {}", pattern.title),
            None => format!("the KindKnowledge {knowledge}"),
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::knowledge;

    const PATTERN: &str = "references: [{path: status.currentPrimary, kind: Pod}, {path: 'status.instances[]', kind: Pod}], pattern: {name: primary-with-replicas, roles: {instances: {reference: 'status.instances[]'}, primary: {reference: status.currentPrimary}}}";

    #[rstest]
    #[case::the_patterns_alone(PATTERN.to_string(), vec![("the-store", Some("primary-with-replicas"))])]
    #[case::the_kinds_first(
        format!("{PATTERN}, roles: {{lead: {{reference: status.currentPrimary}}}}, elevations: [{{name: the lead, question: Who leads?, sketch: lead}}]"),
        vec![("the-lead", None), ("the-store", Some("primary-with-replicas"))]
    )]
    #[case::a_slug_that_the_kind_has(
        format!("{PATTERN}, roles: {{lead: {{reference: status.currentPrimary}}}}, elevations: [{{name: The store, question: Who leads?, sketch: lead}}]"),
        vec![("the-store", None), ("the-store-by-transect", Some("primary-with-replicas"))]
    )]
    #[case::no_pattern(
        "roles: {lead: {reference: spec.a}}, references: [{path: spec.a}], elevations: [{name: the lead, question: Who?, sketch: lead}]".to_string(),
        vec![("the-lead", None)]
    )]
    fn lists_the_kinds_elevations_then_its_patterns(
        #[case] spec: String,
        #[case] expected: Vec<(&str, Option<&str>)>,
    ) {
        let known = knowledge("ledgers.ledgers.example.test", &format!("{{{spec}}}"));
        let stated: Vec<(String, Option<String>)> = known
            .stated()
            .into_iter()
            .map(|(slug, stated)| (slug, stated.pattern.map(|p| p.name.clone())))
            .collect();
        let expected: Vec<(String, Option<String>)> = expected
            .into_iter()
            .map(|(slug, pattern)| (slug.to_string(), pattern.map(String::from)))
            .collect();
        assert_eq!(stated, expected);
    }

    #[rstest]
    #[case::the_kinds_own(false, "the KindKnowledge ledgers.ledgers.example.test")]
    #[case::the_catalogs(true, "transect's pattern primary with replicas")]
    fn names_who_states_an_elevation(#[case] catalog: bool, #[case] expected: &str) {
        let known = knowledge(
            "ledgers.ledgers.example.test",
            &format!(
                "{{{PATTERN}, roles: {{lead: {{reference: status.currentPrimary}}}}, elevations: [{{name: the lead, question: Who?, sketch: lead}}]}}"
            ),
        );
        let stated = known.stated();
        let found = stated.iter().find(|(_, s)| s.pattern.is_some() == catalog);
        assert_eq!(found.unwrap().1.states(&known.name), expected);
    }
}
