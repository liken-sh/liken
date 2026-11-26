//! The pod templates that mount `ServiceAccount` tokens for programs,
//! and the edges from the object that holds each template to the
//! workloads that run those programs.
//!
//! A pod presents a token for an audience, and a program that accepts
//! the audience checks the token with a `TokenReview`.  The pods of an
//! agent's turn run only while the turn runs, so the edges come from
//! the templates, which exist all the time.  A template presents the
//! audiences of its own projected volumes, the audiences that the
//! holder's controller adds, the audiences of a template that the
//! controller merges into it, and the audiences that a program adds at
//! admission to a pod with a label.

use transect_graph::edges::{Edge, Endpoint, Kind, Object};
use transect_resources::knowledge::Programs;

/// One pod template and the audiences that its pods present.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Presented {
    /// The object whose field holds the template: a custom resource,
    /// or a built-in workload such as a `Deployment`.
    pub holder: Object,
    /// The field, such as `spec.template`.
    pub path: String,
    /// The word that the holder's kind knowledge gives the pods made
    /// from the field, such as `turn`.  A built-in workload's template
    /// has none.
    pub used: Option<String>,
    /// The `ServiceAccount` that the pods run as, when the template or
    /// its knowledge names it.
    pub runs_as: Option<String>,
    /// Sorted by name.
    pub audiences: Vec<Audience>,
    /// The objects whose templates the holder's controller merges into
    /// this one, such as a `Duty`'s `Mandate`.  The pods of this
    /// template run that object's work too, so its pages count these
    /// audiences as its own.
    pub merges: Vec<Object>,
}

/// One audience that a template presents.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Audience {
    pub name: String,
    /// The objects that a label of the template names for the program
    /// that injects this audience, such as a browser profile.
    pub names: Vec<Object>,
}

/// A workload and the images that its pods run, from its containers and
/// its native sidecars.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Runner {
    pub workload: Object,
    pub images: Vec<String>,
}

/// An edge of `Kind::Presents` from each template's holder to each
/// runner of a program that accepts one of the template's audiences.
/// A holder is never its own callee, so an operator's resource whose
/// pods run the operator's image does not call itself.  A runner is a
/// top owner, so `top` gives the holder's top owner to compare with it.
pub fn edges(
    presented: &[Presented],
    programs: &Programs,
    runners: &[Runner],
    top: impl Fn(&Object) -> Object,
) -> Vec<Edge> {
    let mut edges = Vec::new();
    for template in presented {
        let holder = top(&template.holder);
        for audience in &template.audiences {
            for (program, _) in programs.accepting(&audience.name) {
                let running = runners.iter().filter(|runner| {
                    runner.workload != holder
                        && runner.images.iter().any(|image| program.runs(image))
                });
                edges.extend(running.map(|runner| Edge {
                    kind: Kind::Presents,
                    from: Endpoint::Object(template.holder.clone()),
                    to: Endpoint::Object(runner.workload.clone()),
                    guessed: false,
                    provenance: program.provenance(&audience.name, &template.path),
                }));
            }
        }
    }
    edges.sort_by(|a, b| (&a.from, &a.to, &a.provenance).cmp(&(&b.from, &b.to, &b.provenance)));
    edges.dedup();
    edges
}

/// The audiences of `template` that no loaded program accepts.
pub fn unknown<'a>(template: &'a Presented, programs: &Programs) -> Vec<&'a str> {
    let audiences = template.audiences.iter().map(|a| a.name.as_str());
    let unknown = audiences.filter(|name| programs.accepting(name).next().is_none());
    unknown.collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_resources::knowledge::Kinds;
    use transect_resources::knowledge::program::parse;
    use transect_resources::resources::DocId;

    fn programs() -> Programs {
        let lanyard = "metadata: {name: lanyard}\nspec: {image: guid.foo/lanyard, accepts: [{audience: lanyard, verb: asks for a credential}]}";
        let midden = "metadata: {name: midden}\nspec: {image: guid.foo/midden, accepts: [{audience: midden, verb: ships its transcript}]}";
        let read =
            |text: &str| parse(DocId::default(), serde_yaml_ng::from_str(text).unwrap()).unwrap();
        Programs::resolve(&[read(lanyard), read(midden)], &Kinds::default())
    }

    fn mandate() -> Object {
        Object::new("simmer.foo", "Mandate", Some("garden"), "garden")
    }

    fn deployment(namespace: &str, name: &str) -> Object {
        Object::new("apps", "Deployment", Some(namespace), name)
    }

    fn template(audiences: &[&str]) -> Presented {
        let audiences = audiences.iter().map(|name| Audience {
            name: name.to_string(),
            names: Vec::new(),
        });
        Presented {
            holder: mandate(),
            path: "spec.template".into(),
            used: Some("turn".into()),
            runs_as: Some("simmer-garden".into()),
            audiences: audiences.collect(),
            merges: Vec::new(),
        }
    }

    fn runners() -> Vec<Runner> {
        let runner = |namespace: &str, name: &str, image: &str| Runner {
            workload: deployment(namespace, name),
            images: vec![image.into()],
        };
        vec![
            runner("credentials", "lanyard", "atcr.io/guid.foo/lanyard:1"),
            runner("transcripts", "midden", "atcr.io/guid.foo/midden:2"),
            runner("transcripts", "midden-mcp", "atcr.io/guid.foo/midden:2"),
            runner("shop", "web", "example.test/web:1"),
            Runner {
                workload: mandate(),
                images: vec!["atcr.io/guid.foo/lanyard:1".into()],
            },
        ]
    }

    /// The `to` end of each edge, as `namespace/name`.
    fn callees(audiences: &[&str]) -> Vec<String> {
        let found = edges(
            &[template(audiences)],
            &programs(),
            &runners(),
            Object::clone,
        );
        let ends = found.into_iter().map(|edge| match edge.to {
            Endpoint::Object(o) => format!("{}/{}", o.namespace.unwrap_or_default(), o.name),
            other => format!("{other:?}"),
        });
        ends.collect()
    }

    #[rstest]
    #[case::one_program(&["lanyard"], vec!["credentials/lanyard"])]
    #[case::a_program_that_two_workloads_run(&["midden"], vec!["transcripts/midden", "transcripts/midden-mcp"])]
    #[case::an_audience_nothing_accepts(&["transom"], vec![])]
    #[case::no_audience(&[], vec![])]
    fn draws_an_edge_to_each_workload_of_an_accepting_program(
        #[case] audiences: &[&str],
        #[case] expected: Vec<&str>,
    ) {
        assert_eq!(callees(audiences), expected);
    }

    #[rstest]
    fn labels_the_edge_with_the_program_and_the_template() {
        let found = edges(
            &[template(&["lanyard"])],
            &programs(),
            &runners(),
            Object::clone,
        );
        let provenance = found.into_iter().map(|edge| edge.provenance.title());
        assert_eq!(
            provenance.collect::<Vec<_>>(),
            vec![
                "from the ProgramKnowledge lanyard, which accepts lanyard, and the template at spec.template"
            ]
        );
    }

    #[rstest]
    #[case::all_known(&["lanyard", "midden"], vec![])]
    #[case::one_unknown(&["lanyard", "transom"], vec!["transom"])]
    fn names_the_audiences_nothing_accepts(
        #[case] audiences: &[&str],
        #[case] expected: Vec<&str>,
    ) {
        assert_eq!(unknown(&template(audiences), &programs()), expected);
    }
}
