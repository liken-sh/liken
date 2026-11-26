//! The program knowledge that applies to one reading of the cluster,
//! with the conditions of the checks that need the knowledge of a kind.

use super::program::{Accepts, Injects, KindOf, ProgramKnowledge};
use super::references::target_of;
use super::{Kinds, Reference};
use crate::resources::{Condition, DocId};

/// The loaded programs, in the order of their documents.
#[derive(Debug, Default)]
pub struct Programs {
    programs: Vec<ProgramKnowledge>,
    pub conditions: Vec<(DocId, Condition)>,
}

impl Programs {
    /// Confirms each `grants` and each `names` of `programs` against
    /// `kinds`.  A `grants` must name a kind whose knowledge states a
    /// reference at its path to a `ServiceAccount`, because a page reads
    /// the account from that reference.  A `names` must name a kind that
    /// has knowledge.  Each that does not resolve is left out of the
    /// program, which stays in use, and gives its document the condition
    /// `Unresolved`.
    pub fn resolve(programs: &[ProgramKnowledge], kinds: &Kinds) -> Self {
        let mut resolved = Self::default();
        for program in programs {
            let mut program = program.clone();
            let mut messages = Vec::new();
            for (at, accepts) in program.accepts.iter_mut().enumerate() {
                let Some(grants) = &accepts.grants else {
                    continue;
                };
                let known = kinds.of(&grants.kind.group, &grants.kind.kind);
                let fault = match known {
                    None => Some(no_knowledge(&grants.kind)),
                    Some(known) if !names_accounts(&known.references, &grants.path) => {
                        Some(format!(
                            "the knowledge of {} has no reference at {} to ServiceAccount",
                            grants.kind.kind, grants.path
                        ))
                    }
                    Some(_) => None,
                };
                if let Some(fault) = fault {
                    messages.push(format!(
                        "accepts[{at}] is granted by {}, and {fault}",
                        grants.kind.kind
                    ));
                    accepts.grants = None;
                }
            }
            for (at, injects) in program.injects.iter_mut().enumerate() {
                let Some(names) = &injects.names else {
                    continue;
                };
                if kinds.of(&names.group, &names.kind).is_none() {
                    let fault = no_knowledge(names);
                    messages.push(format!("injects[{at}] names a {}, and {fault}", names.kind));
                    injects.names = None;
                }
            }
            let conditions = messages.into_iter().map(Condition::unresolved);
            resolved
                .conditions
                .extend(conditions.map(|c| (program.doc, c)));
            resolved.programs.push(program);
        }
        resolved
    }

    pub fn iter(&self) -> impl Iterator<Item = &ProgramKnowledge> {
        self.programs.iter()
    }

    /// Each program that accepts `audience`, with its entry of `accepts`.
    pub fn accepting<'a>(
        &'a self,
        audience: &'a str,
    ) -> impl Iterator<Item = (&'a ProgramKnowledge, &'a Accepts)> {
        let accepting = self.programs.iter();
        accepting.filter_map(move |program| Some((program, program.accepting(audience)?)))
    }

    /// Each entry of `injects` whose `podLabel` is a key of `labels`,
    /// with the label's value.
    pub fn injected<'a>(&'a self, labels: &'a [(String, String)]) -> Vec<(&'a Injects, &'a str)> {
        let injects = self.programs.iter().flat_map(|program| &program.injects);
        let found = injects.filter_map(|injects| {
            let (_, value) = labels.iter().find(|(key, _)| *key == injects.pod_label)?;
            Some((injects, value.as_str()))
        });
        found.collect()
    }
}

fn no_knowledge(kind: &KindOf) -> String {
    format!("transect has no knowledge of {}", kind.kind)
}

/// Whether the entry of `references` at `path` names a `ServiceAccount`.
fn names_accounts(references: &[Reference], path: &str) -> bool {
    target_of(references, path) == Some(("", "ServiceAccount"))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::knowledge;
    use super::super::program::parse;
    use super::*;
    use transect_graph::edges::Plurals;

    fn program(spec: &str) -> ProgramKnowledge {
        let text = format!("metadata: {{name: gofer}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap()).unwrap()
    }

    /// The kinds of `profile`, the knowledge of `BrowserProfile`, when
    /// there is any.
    fn kinds(profile: Option<&str>) -> Kinds {
        let plurals = Plurals::from([(
            ("gofer.guid.foo".to_string(), "BrowserProfile".to_string()),
            "browserprofiles".to_string(),
        )]);
        let known: Vec<_> = profile
            .map(|spec| knowledge("browserprofiles.gofer.guid.foo", spec))
            .into_iter()
            .collect();
        Kinds::resolve(&known, &plurals, &[])
    }

    const GOFER: &str = "{image: guid.foo/gofer, accepts: [{audience: gofer, verb: loads, grants: {kind: BrowserProfile, group: gofer.guid.foo, path: 'spec.serviceAccounts[]'}}], injects: [{podLabel: gofer.guid.foo/profile, audience: gofer, names: {kind: BrowserProfile, group: gofer.guid.foo}}]}";

    #[rstest]
    #[case::a_reference_to_accounts(
        Some("{references: [{path: 'spec.serviceAccounts[]', kind: ServiceAccount}]}"),
        vec![]
    )]
    #[case::a_reference_to_another_kind(
        Some("{references: [{path: 'spec.serviceAccounts[]', kind: Secret}]}"),
        vec!["accepts[0] is granted by BrowserProfile, and the knowledge of BrowserProfile has no reference at spec.serviceAccounts[] to ServiceAccount"]
    )]
    #[case::no_knowledge(
        None,
        vec![
            "accepts[0] is granted by BrowserProfile, and transect has no knowledge of BrowserProfile",
            "injects[0] names a BrowserProfile, and transect has no knowledge of BrowserProfile",
        ]
    )]
    fn confirms_grants_and_names_against_the_kinds(
        #[case] profile: Option<&str>,
        #[case] expected: Vec<&str>,
    ) {
        let programs = Programs::resolve(&[program(GOFER)], &kinds(profile));
        let messages: Vec<&str> = programs
            .conditions
            .iter()
            .map(|(_, c)| c.message.as_str())
            .collect();
        assert_eq!(messages, expected);
    }

    #[rstest]
    fn leaves_out_what_does_not_resolve() {
        let programs = Programs::resolve(&[program(GOFER)], &kinds(None));
        let gofer = programs.iter().next().unwrap();
        assert_eq!(
            (
                gofer.accepts[0].grants.clone(),
                gofer.injects[0].names.clone()
            ),
            (None, None)
        );
    }

    #[rstest]
    #[case::the_label("gofer.guid.foo/profile", vec![("gofer", "garden-browser")])]
    #[case::another_label("app", vec![])]
    fn finds_what_a_label_injects(#[case] key: &str, #[case] expected: Vec<(&str, &str)>) {
        let programs = Programs::resolve(&[program(GOFER)], &kinds(None));
        let labels = vec![(key.to_string(), "garden-browser".to_string())];
        let found: Vec<(&str, &str)> = programs
            .injected(&labels)
            .into_iter()
            .map(|(injects, value)| (injects.audience.as_str(), value))
            .collect();
        assert_eq!(found, expected);
    }
}
