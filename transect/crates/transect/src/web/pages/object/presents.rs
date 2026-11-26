//! The section of an object whose kind's knowledge states `templates`:
//! the tokens that each template's pods present, and the workload of
//! each program that accepts one.  The pods of an agent's turn run only
//! while the turn runs, so the section reads the templates, and it adds
//! the pods made from them that run now.

use super::super::super::snapshot::Snapshot;
use super::refer;
use transect_access::access::presents::{Presented, unknown};
use transect_graph::edges::{Endpoint, Graph, Kind, Object, Provenance};
use transect_graph::links::Reference;
use transect_graph::tokens;
use transect_map::objects::{Section, Side};
use transect_resources::knowledge::Names;

/// The section "Presents tokens" of `object`, or none when it holds no
/// template.
pub fn presents(snapshot: &Snapshot, object: &Object, graph: &Graph) -> Option<Section> {
    let held: Vec<Presented> = snapshot
        .presented
        .iter()
        .filter(|p| &p.holder == object)
        .cloned()
        .collect();
    if held.is_empty() {
        return None;
    }
    let here = object.namespace.as_deref();
    let mut section = Section::new("Presents tokens", Side::Out);
    for template in &held {
        let used = template.used.as_deref().unwrap_or("pod");
        let label = format!("{used} pods, from {}", template.path);
        let audiences = template.audiences.iter().map(|a| a.name.clone()).collect();
        section = section.literals(&label, audiences);
    }
    let end = Endpoint::Object(object.clone());
    let mut facts: Vec<(String, Vec<Reference>)> = Vec::new();
    for edge in graph.edges_leaving(Kind::Presents, &end) {
        let Provenance::Program {
            program, audience, ..
        } = &edge.provenance
        else {
            continue;
        };
        let verb = snapshot.programs.iter().find(|p| &p.name == program);
        let verb = verb
            .and_then(|p| p.accepting(audience))
            .map(|a| a.verb.as_str());
        let label = format!("{}, as {audience}", verb.unwrap_or("calls"));
        let callee = refer(snapshot, &edge.to, here);
        match facts.iter_mut().find(|(known, _)| known == &label) {
            Some((_, callees)) if callees.contains(&callee) => {}
            Some((_, callees)) => callees.push(callee),
            None => facts.push((label, vec![callee])),
        }
    }
    for (label, callees) in facts {
        section = section.fact(&label, callees);
    }
    let mut unknowns: Vec<String> = held
        .iter()
        .flat_map(|t| unknown(t, &snapshot.programs))
        .map(String::from)
        .collect();
    unknowns.sort();
    unknowns.dedup();
    section = section.literals("presents a token with no knowledge loaded for it", unknowns);
    let seen = running(snapshot, object, &held);
    Some(
        section.fact(
            "seen on",
            seen.iter()
                .map(|pod| refer(snapshot, &Endpoint::Object(pod.clone()), here))
                .collect(),
        ),
    )
}

/// The pods made for `object` that run now with a token for an audience
/// of one of its templates.  A pod is made for the object when the
/// object owns it, or when an object that names `object` in a reference
/// owns it.  A `Session` names its `Duty` in `spec.duty` and owns the
/// `Job`s of its turns, so a `Duty`'s running turn shows here though the
/// `Duty` owns nothing.
fn running(snapshot: &Snapshot, object: &Object, held: &[Presented]) -> Vec<Object> {
    let naming = snapshot.relations.iter().filter(|(_, relations)| {
        let names = relations.iter().map(|r| &r.names);
        names
            .into_iter()
            .any(|names| names == &Names::Object(object.clone()))
    });
    let mut makers: Vec<&Object> = naming.map(|(naming, _)| naming).collect();
    makers.push(object);
    let wanted: Vec<&str> = held
        .iter()
        .flat_map(|t| t.audiences.iter().map(|a| a.name.as_str()))
        .collect();
    let pods = snapshot
        .pods
        .iter()
        .filter(|pod| !transect_graph::pods::finished(pod));
    let found = pods.filter_map(|pod| {
        let meta = &pod.metadata;
        let pod_object = Object::new("", "Pod", meta.namespace.as_deref(), meta.name.as_deref()?);
        let made = makers
            .iter()
            .any(|maker| snapshot.ownership.is_owned_by(&pod_object, maker));
        if !made {
            return None;
        }
        let audiences = tokens::audiences(pod.spec.as_ref()?);
        audiences
            .iter()
            .any(|a| wanted.contains(&a.as_str()))
            .then_some(pod_object)
    });
    found.collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::super::fixture;
    use super::*;

    #[rstest]
    #[case::a_duty_through_its_session("Duty", "tend", vec!["garden-tend-1-pod"])]
    #[case::a_mandate_through_its_session("Mandate", "garden", vec!["garden-tend-1-pod"])]
    #[case::a_duty_with_no_running_turn("Duty", "prune", vec![])]
    fn finds_the_running_pods_made_for_a_holder(
        #[case] kind: &str,
        #[case] name: &str,
        #[case] expected: Vec<&str>,
    ) {
        let snapshot = fixture::snapshot();
        let holder = Object::new("agents.example.test", kind, Some("acme-garden"), name);
        let held: Vec<Presented> = snapshot
            .presented
            .iter()
            .filter(|p| p.holder == holder)
            .cloned()
            .collect();
        let found = running(&snapshot, &holder, &held);
        let names: Vec<String> = found.into_iter().map(|pod| pod.name).collect();
        assert_eq!(names, expected);
    }
}
