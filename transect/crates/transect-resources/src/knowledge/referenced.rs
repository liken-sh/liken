//! Objects of a known kind that an annotation on another kind names,
//! such as the middlewares an ingress controller's annotation lists on
//! an `Ingress`, and the edges that this draws from the applications
//! behind it.
//!
//! The annotation writes each name in its own form, such as
//! `<namespace>-<name>@<provider>`, where a namespace and a name that
//! both hold dashes do not show where one ends.  So `transect` renders
//! the kind knowledge's template for each object of the kind and
//! matches the annotation's entries against those, and never splits an
//! entry to guess.  Two objects whose names render alike, as `a-b` in
//! `c` and `a` in `b-c` can, are both left unnamed.  A template without
//! `{namespace}` names an object of a namespaced kind in the annotated
//! object's namespace, as an annotation that names an issuer by its
//! name alone means the issuer in the same namespace.

use std::collections::HashMap;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use super::reach::Reached;
use super::references::Relation;
use super::{Kinds, ReferencedBy, Role};
use transect_graph::edges::{Edge, Endpoint, Graph, Object, Provenance};

/// One object of a known kind that an annotation on another object
/// names.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Referral {
    /// The annotated object, such as an `Ingress`.
    pub referrer: Object,
    /// The object its annotation names, such as a middleware.
    pub target: Object,
    pub annotation: String,
    /// What the target does for the referrer, as the knowledge declares.
    pub role: Option<Role>,
}

/// Each object among `targets` that an annotation among `annotated`
/// names, through the `referencedBy` of its kind's knowledge, in the
/// order of `annotated` and then of the annotation's entries.
pub fn referrals(
    kinds: &Kinds,
    targets: &[Object],
    annotated: &[(Object, &ObjectMeta)],
) -> Vec<Referral> {
    let mut found = Vec::new();
    for (group, kind) in kinds.kinds() {
        let Some(knowledge) = kinds.of(group, kind) else {
            continue;
        };
        let of_kind: Vec<&Object> = targets
            .iter()
            .filter(|target| &target.group == group && &target.kind == kind)
            .collect();
        for by in &knowledge.referenced_by {
            let mut named: HashMap<String, Vec<&Object>> = HashMap::new();
            for target in &of_kind {
                named
                    .entry(render(&by.name, target))
                    .or_default()
                    .push(target);
            }
            for (referrer, meta) in annotated.iter().filter(|(o, _)| annotates(by, o)) {
                let annotations = meta.annotations.as_ref();
                let Some(value) = annotations.and_then(|a| a.get(&by.annotation)) else {
                    continue;
                };
                let beside = |target: &&&Object| {
                    by.name.contains("{namespace}")
                        || target.namespace.is_none()
                        || target.namespace == referrer.namespace
                };
                for entry in value.split(',').map(str::trim) {
                    let candidates = named.get(entry).into_iter().flatten();
                    let candidates: Vec<&&Object> = candidates.filter(beside).collect();
                    if let [target] = candidates.as_slice() {
                        found.push(Referral {
                            referrer: referrer.clone(),
                            target: (**target).clone(),
                            annotation: by.annotation.clone(),
                            role: by.role,
                        });
                    }
                }
            }
        }
    }
    found
}

fn annotates(by: &ReferencedBy, object: &Object) -> bool {
    object.group == by.group && object.kind == by.kind
}

/// `template` with `{namespace}` and `{name}` filled from `object`.
fn render(template: &str, object: &Object) -> String {
    let namespace = object.namespace.as_deref().unwrap_or_default();
    template
        .replace("{namespace}", namespace)
        .replace("{name}", &object.name)
}

/// The edges each of `referrals` draws: from each application `behind`
/// its referrer, an edge of each kind its target's `relations` draw, to
/// each end `reached` returns for the relation.  An application gets no
/// edge to itself, as `owns` reports, nor one `graph` already holds, and
/// each edge's provenance names the field, the annotation, and the
/// referrer.
pub fn referred_edges(
    referrals: &[Referral],
    relations: &HashMap<Object, Vec<Relation>>,
    reached: impl Fn(&Object, &Relation) -> Reached,
    behind: impl Fn(&Object) -> Vec<Object>,
    owns: impl Fn(&Object, &Endpoint) -> bool,
    graph: &Graph,
) -> Vec<Edge> {
    let mut drawn: Vec<Edge> = Vec::new();
    for referral in referrals {
        let Some(relations) = relations.get(&referral.target) else {
            continue;
        };
        let applications = behind(&referral.referrer);
        for relation in relations {
            let Some(kind) = relation.edge else {
                continue;
            };
            let found = reached(&referral.target, relation);
            let provenance = through(&relation.provenance, referral);
            for application in &applications {
                let from = Endpoint::Object(application.clone());
                let known = graph.leaving(kind, &from);
                for to in &found.ends {
                    let seen = drawn
                        .iter()
                        .any(|e| e.kind == kind && e.from == from && &e.to == to);
                    if owns(application, to) || known.contains(&to) || seen {
                        continue;
                    }
                    drawn.push(Edge {
                        kind,
                        from: from.clone(),
                        to: to.clone(),
                        guessed: found.guessed,
                        provenance: provenance.clone(),
                    });
                }
            }
        }
    }
    drawn
}

/// `provenance`, a field of the target's kind knowledge, read through
/// the annotation on the referrer that names the target.
fn through(provenance: &Provenance, referral: &Referral) -> Provenance {
    match provenance {
        Provenance::Kinds { knowledge, field } => Provenance::Kinds {
            knowledge: knowledge.clone(),
            field: format!(
                "{field} of {} {}, named by {} on {} {}",
                referral.target.kind,
                referral.target.name,
                referral.annotation,
                referral.referrer.kind,
                referral.referrer.name
            ),
        },
        other => other.clone(),
    }
}

#[cfg(test)]
mod tests;
