//! Finds the objects of a known kind that an annotation on an object of
//! another kind refers to, and draws the edges that follow. For
//! example, an ingress controller's annotation on an `Ingress` lists
//! middlewares. The applications behind the `Ingress` get edges to what
//! those middlewares reach.
//!
//! Each annotation writes names in its own format, such as
//! `<namespace>-<name>@<provider>`. A name with dashes in it cannot be
//! split back into a namespace and a name. So `transect` fills the
//! knowledge's name template for each object of the kind and compares
//! each entry of the annotation with the results. When two objects give
//! the same string, neither matches: with `{namespace}-{name}`, the name
//! `a-b` in the namespace `c` and the name `b` in the namespace `c-a`
//! both give `c-a-b`. A template without `{namespace}` refers to an
//! object in the annotated object's namespace, or to a cluster-scoped
//! one, as an annotation that gives an issuer by name alone means the
//! issuer in the same namespace.

use std::collections::{BTreeMap, HashMap};

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use super::reach::Reached;
use super::references::Relation;
use super::{Duty, Kinds, ReferencedBy};
use transect_graph::edges::{Edge, Endpoint, Graph, Object, Provenance};

/// An annotation on `referrer` that refers to `target`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Referral {
    /// The annotated object, such as an `Ingress`.
    pub referrer: Object,
    /// The object that its annotation refers to, such as a middleware.
    pub target: Object,
    pub annotation: String,
    /// What the target does for the referrer, as the knowledge declares.
    pub role: Option<Duty>,
}

/// The referrals between `annotated` and `targets`: each object in
/// `targets` that an annotation on an object in `annotated` refers to,
/// found through the `referencedBy` entries of the target kind's
/// knowledge. Results are grouped by kind and `referencedBy` entry.
/// Within a group they follow the order of `annotated`, then of the
/// annotation's entries.
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
                let in_scope = |target: &&&Object| {
                    by.name.contains("{namespace}")
                        || target.namespace.is_none()
                        || target.namespace == referrer.namespace
                };
                for entry in value.split(',').map(str::trim) {
                    let candidates = named.get(entry).into_iter().flatten();
                    let candidates: Vec<&&Object> = candidates.filter(in_scope).collect();
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
pub(super) fn render(template: &str, object: &Object) -> String {
    let namespace = object.namespace.as_deref().unwrap_or_default();
    template
        .replace("{namespace}", namespace)
        .replace("{name}", &object.name)
}

/// The edges that `referrals` imply. For each referral, each
/// application `behind` the referrer gets an edge, of each kind that
/// the target's `relations` draw, to each end that `reached` returns for
/// the relation. An edge is skipped when `owns` reports that the
/// application owns the end, when `graph` already has it, or when it
/// repeats an edge drawn earlier. The edge's provenance records the
/// field, the annotation, and the referrer.
pub fn referred_edges(
    referrals: &[Referral],
    relations: &BTreeMap<Object, Vec<Relation>>,
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
            let provenance = with_referral(&relation.provenance, referral);
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

/// `provenance`, a field of the target's kind knowledge, with the
/// annotation and the referrer that led to the target added.
fn with_referral(provenance: &Provenance, referral: &Referral) -> Provenance {
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
