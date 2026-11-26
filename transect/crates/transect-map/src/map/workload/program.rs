//! The "Program" rows of the workload page, for a workload whose image
//! is a program that a `ProgramKnowledge` describes: what the program
//! is for, each audience whose tokens it accepts, and its callers, the
//! objects whose pod templates present a token for that audience.  The
//! callers come from the `Presents` edges that reach the workload.
//!
//! A program can have dozens of callers, most of them the `Duty`
//! objects of a few `Mandate`s.  So the callers are grouped by the
//! object whose template theirs merges, which the page names first,
//! followed by the rest, as "and its Duties: review, triage".  A caller
//! that merges no
//! other object's template and that no other caller merges, such as a
//! `CronJob`, is in the last group, which has no head.

use transect_graph::edges::{Edge, Endpoint, Object, Provenance};
use transect_graph::links::Reference;
use transect_resources::knowledge::ProgramKnowledge;

/// One program that the workload runs.
#[derive(Debug, PartialEq)]
pub struct ProgramRows {
    pub name: String,
    pub summary: Option<String>,
    /// The mark of the program's knowledge.
    pub mark: Provenance,
    pub accepts: Vec<Accepted>,
}

/// One audience that the program accepts, with its callers.
#[derive(Debug, PartialEq)]
pub struct Accepted {
    pub audience: String,
    pub verb: String,
    pub groups: Vec<Callers>,
}

/// The callers that share a head: a `Mandate` and the `Duty` objects
/// whose turns merge its template.
#[derive(Debug, PartialEq)]
pub struct Callers {
    /// The object that the members merge.  It is listed whether or not
    /// it calls the program itself.  The group of the callers that
    /// merge nothing has none.
    pub head: Option<Reference>,
    /// The words before the members of a group with a head, such as
    /// "and its Duties:".
    pub lead: Option<String>,
    pub members: Vec<Reference>,
}

impl Accepted {
    /// Whether no template presents the audience.
    pub fn uncalled(&self) -> bool {
        self.groups.is_empty()
    }
}

impl ProgramRows {
    /// The rows of `program`, with a caller for each edge of `reaching`,
    /// the `Presents` edges that reach the workload, whose provenance
    /// names the program and the audience.  `merged` gives the object
    /// whose template a caller's template merges.  `refer` names an
    /// object, and `short` names a member of a group by its name alone.
    pub fn build(
        program: &ProgramKnowledge,
        reaching: &[Edge],
        merged: impl Fn(&Object) -> Option<Object>,
        refer: impl Fn(&Object) -> Reference,
        short: impl Fn(&Object) -> Reference,
        plural: impl Fn(&Object) -> String,
    ) -> Self {
        let accepts = program.accepts.iter().map(|accepts| {
            let mut callers: Vec<Object> = reaching
                .iter()
                .filter(|edge| match &edge.provenance {
                    Provenance::Program {
                        program: name,
                        audience,
                        ..
                    } => name == &program.name && audience == &accepts.audience,
                    _ => false,
                })
                .filter_map(|edge| match &edge.from {
                    Endpoint::Object(object) => Some(object.clone()),
                    _ => None,
                })
                .collect();
            callers.sort();
            callers.dedup();
            Accepted {
                audience: accepts.audience.clone(),
                verb: accepts.verb.clone(),
                groups: group(&callers, &merged, &refer, &short, &plural),
            }
        });
        Self {
            name: program.name.clone(),
            summary: program.summary.clone(),
            mark: Provenance::Program {
                program: program.name.clone(),
                audience: String::new(),
                template: String::new(),
            },
            accepts: accepts.collect(),
        }
    }
}

/// `callers`, sorted, in groups by the object that each merges.
fn group(
    callers: &[Object],
    merged: &impl Fn(&Object) -> Option<Object>,
    refer: &impl Fn(&Object) -> Reference,
    short: &impl Fn(&Object) -> Reference,
    plural: &impl Fn(&Object) -> String,
) -> Vec<Callers> {
    let mut heads: Vec<Object> = callers.iter().filter_map(merged).collect();
    heads.sort();
    heads.dedup();
    let mut groups: Vec<Callers> = heads
        .iter()
        .map(|head| {
            let members: Vec<&Object> = callers
                .iter()
                .filter(|caller| merged(caller).as_ref() == Some(head))
                .collect();
            let mut kinds: Vec<String> = members.iter().map(|m| plural(m)).collect();
            kinds.dedup();
            Callers {
                head: Some(refer(head)),
                lead: Some(format!("and its {}:", kinds.join(" and "))),
                members: members.into_iter().map(short).collect(),
            }
        })
        .collect();
    let alone: Vec<Reference> = callers
        .iter()
        .filter(|caller| merged(caller).is_none() && !heads.contains(caller))
        .map(refer)
        .collect();
    if !alone.is_empty() {
        groups.push(Callers {
            head: None,
            lead: None,
            members: alone,
        });
    }
    groups
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn object(kind: &str, name: &str) -> Object {
        Object::new("simmer.foo", kind, Some("ops"), name)
    }

    fn text(reference: Reference) -> String {
        reference.text
    }

    #[rstest]
    fn groups_duties_under_their_mandate_and_the_rest_alone() {
        let callers = vec![
            object("Duty", "review"),
            object("Duty", "triage"),
            object("Mandate", "ops"),
            Object::new("batch", "CronJob", Some("ops"), "changes"),
            object("Duty", "digest"),
        ];
        let merged = |caller: &Object| match (caller.kind.as_str(), caller.name.as_str()) {
            ("Duty", "digest") => Some(object("Mandate", "github")),
            ("Duty", _) => Some(object("Mandate", "ops")),
            _ => None,
        };
        let refer = |o: &Object| Reference {
            text: format!("{} {}", o.kind, o.name),
            href: None,
        };
        let short = |o: &Object| Reference {
            text: o.name.clone(),
            href: None,
        };
        let plural =
            |o: &Object| transect_resources::knowledge::plural_label(&o.kind, Some("duties"));
        let mut callers = callers;
        callers.sort();
        let groups = group(&callers, &merged, &refer, &short, &plural);
        let shown: Vec<(Option<String>, Option<String>, Vec<String>)> = groups
            .into_iter()
            .map(|g| {
                (
                    g.head.map(text),
                    g.lead,
                    g.members.into_iter().map(text).collect(),
                )
            })
            .collect();
        assert_eq!(
            shown,
            vec![
                (
                    Some("Mandate github".into()),
                    Some("and its Duties:".into()),
                    vec!["digest".into()]
                ),
                (
                    Some("Mandate ops".into()),
                    Some("and its Duties:".into()),
                    vec!["review".into(), "triage".into()]
                ),
                (None, None, vec!["CronJob changes".into()]),
            ]
        );
    }
}
